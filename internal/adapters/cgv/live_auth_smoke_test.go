package cgv

import (
	"context"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/cineko-org/client/internal/logging"
	"github.com/cineko-org/probe/v2/networkcapture"
	"github.com/mxschmitt/playwright-go"
)

// Opt-in, single home-page visit. Never participates in automatic test runs,
// never navigates to booking/payment, and never modifies the source session.
func TestLiveSavedSessionAuthenticationOnce(t *testing.T) {
	source := os.Getenv("CINEKO_LIVE_SESSION_DIR")
	if source == "" || os.Getenv("CINEKO_TEST_LIVE_AUTH") != "1" {
		t.Skip("explicit live authentication smoke test only")
	}
	restoreLogging := logging.SetOutput(io.Discard)
	defer restoreLogging()
	config := localBrowserTestConfig(t)
	config.UserAgentMode = UserAgentSession
	config.SessionStatePath = filepath.Join(config.ProfileDir, "cgv-storage-state.json")
	for _, name := range []string{"cgv-storage-state.json", sessionIdentityFilename} {
		contents, err := os.ReadFile(filepath.Join(source, name)) // #nosec G304 G703 -- Explicit opt-in test reads fixed filenames from the supplied session directory.
		if err != nil {
			t.Fatal("read saved session:", err)
		}
		if err := os.WriteFile(filepath.Join(config.ProfileDir, name), contents, 0600); err != nil { // #nosec G703 -- Destination is an owned test TempDir and a fixed filename.
			t.Fatal(err)
		}
	}
	networkDir := filepath.Join(config.ArtifactsDir, "network")
	if err := os.MkdirAll(networkDir, 0700); err != nil {
		t.Fatal(err)
	}
	if cooldown := os.Getenv("CINEKO_LIVE_COOLDOWN_FILE"); cooldown != "" {
		contents, err := os.ReadFile(cooldown) // #nosec G304 G703 -- Explicit opt-in test preserves the supplied provider cooldown.
		if err == nil {
			if err := os.WriteFile(filepath.Join(networkDir, "rate-limit.json"), contents, 0600); err != nil { // #nosec G703 -- Fixed filename within an owned test TempDir.
				t.Fatal(err)
			}
		} else if !os.IsNotExist(err) {
			t.Fatal(err)
		}
	}
	store, err := networkcapture.NewStore(networkDir, nil)
	if err != nil {
		t.Fatal(err)
	}
	if blocked, _ := store.RateLimit().Blocked("cgv.co.kr"); blocked {
		t.Fatal("existing provider cooldown prevents live smoke test")
	}
	config.NetworkCapture = store
	ctx, cancel := context.WithTimeout(t.Context(), 45*time.Second)
	defer cancel()
	adapter, err := NewAdapter(ctx, config)
	if err != nil {
		t.Fatal(err)
	}
	defer adapter.Close()
	var admitted, capped atomic.Int32
	// Upper bound for this one diagnostic navigation. Resource filtering still
	// happens before these counters; no retry loop or manual API replay.
	if err := adapter.browserContext.Route("**/*", func(route playwright.Route) {
		request := route.Request()
		parsed, err := url.Parse(request.URL())
		if err != nil || (parsed.Hostname() != "cgv.co.kr" && !strings.HasSuffix(parsed.Hostname(), ".cgv.co.kr")) || shouldBlockResource(request.URL(), request.ResourceType()) {
			_ = route.Abort("blockedbyclient")
			return
		}
		if admitted.Add(1) > 60 {
			capped.Add(1)
			_ = route.Abort("blockedbyclient")
			return
		}
		_ = route.Fallback()
	}); err != nil {
		t.Fatal(err)
	}
	var mu sync.Mutex
	statuses := make(map[int]int)
	adapter.browserContext.OnResponse(func(response playwright.Response) { mu.Lock(); statuses[response.Status()]++; mu.Unlock() })
	authenticated, authErr := adapter.IsAuthenticated(ctx)
	adapter.Close()
	known, valid := adapter.authEvidence.snapshot()
	mu.Lock()
	t.Logf("single CGV home visit: authenticated=%v evidence_known=%v evidence_valid=%v status_counts=%v budget_rejections=%d error=%v", authenticated, known, valid, statuses, capped.Load(), authErr)
	mu.Unlock()
	if capped.Load() > 0 {
		t.Fatal("diagnostic request budget exhausted; authentication inconclusive")
	}
	if authErr != nil {
		t.Fatal("live authentication check:", authErr)
	}
	if !authenticated {
		t.Fatal("saved CGV session is not authenticated; manual login required")
	}
}
