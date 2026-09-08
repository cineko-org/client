package cgv

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/cineko-org/probe/v2/networkcapture"
)

func TestBrowser429SurvivesRecreationAndLogClear(t *testing.T) {
	if testing.Short() {
		t.Skip("launches installed Chromium against localhost only")
	}
	var hits atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		w.Header().Set("Retry-After", "60")
		w.Header().Set("Content-Type", "text/html")
		w.WriteHeader(429)
		_, _ = fmt.Fprint(w, "rate limited")
	}))
	defer server.Close()
	store, err := networkcapture.NewStore(t.TempDir(), nil)
	if err != nil {
		t.Fatal(err)
	}
	newBrowser := func() *Adapter {
		config := localBrowserTestConfig(t)
		config.NetworkCapture = store
		adapter, err := NewAdapter(t.Context(), config)
		if err != nil {
			t.Fatal(err)
		}
		return adapter
	}
	first := newBrowser()
	_ = first.navigate(server.URL + "/initial")
	deadline := time.Now().Add(3 * time.Second)
	for {
		blocked, _ := store.RateLimit().Blocked(browserRequestHost(server.URL))
		if blocked {
			break
		}
		if time.Now().After(deadline) {
			first.Close()
			t.Fatal("429 did not open circuit")
		}
		time.Sleep(10 * time.Millisecond)
	}
	first.Close()
	if err := store.Clear(); err != nil {
		t.Fatal(err)
	}
	store, err = networkcapture.NewStore(store.Root(), nil)
	if err != nil {
		t.Fatal(err)
	}
	second := newBrowser()
	defer second.Close()
	for i := range 12 {
		_ = second.navigate(fmt.Sprintf("%s/retry/%d", server.URL, i))
	}
	if hits.Load() != 1 {
		t.Fatalf("server received %d requests during Retry-After, want exactly the initial 429", hits.Load())
	}
	t.Logf("actual Chromium: initial 429=%d, browser and Store recreated, logs cleared, 12 retries => additional server requests=0", hits.Load())
}

func TestBrowserHalfOpenIgnoresLocallyBlockedOtherTab(t *testing.T) {
	if testing.Short() {
		t.Skip("launches installed Chromium against localhost only")
	}
	reached, release := make(chan struct{}), make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/probe" {
			close(reached)
			select {
			case <-release:
			case <-r.Context().Done():
				return
			}
		}
		w.Header().Set("Content-Type", "text/html")
		_, _ = fmt.Fprint(w, "ok")
	}))
	defer server.Close()
	adapter, err := NewAdapter(t.Context(), localBrowserTestConfig(t))
	if err != nil {
		t.Fatal(err)
	}
	defer adapter.Close()
	other, err := adapter.browserContext.NewPage()
	if err != nil {
		t.Fatal(err)
	}
	key := browserRequestHost(server.URL)
	adapter.rateLimit.Observe429(key, []networkcapture.Header{{Name: "Retry-After", Value: "1"}})
	time.Sleep(1100 * time.Millisecond)
	done := make(chan error, 1)
	go func() { done <- adapter.navigate(server.URL + "/probe") }()
	select {
	case <-reached:
	case <-time.After(5 * time.Second):
		close(release)
		t.Fatal("half-open request not sent")
	}
	_, _ = other.Goto(server.URL + "/other")
	close(release)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(time.Second)
	for {
		blocked, decision := adapter.rateLimit.Blocked(key)
		if !blocked {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("other tab's local abort poisoned successful probe: %+v", decision)
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Log("one half-open HTTP success restores monitoring even when another tab was locally blocked")
}
