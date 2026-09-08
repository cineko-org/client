package webui

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/cineko-org/client/internal/testsupport/memoryrepo"
	clientpb "github.com/cineko-org/contracts/v3/gen/go/cineko/client"
	"google.golang.org/protobuf/encoding/protojson"
)

func TestRuntimeKeepsAccountAndMonitoringCoherentDuringChanges(t *testing.T) {
	server := &Server{monitoringRunning: true}
	checking := accountStateMessage("checking", "", time.Now())
	ready := accountStateMessage("authenticated", "", time.Now())
	var workers sync.WaitGroup
	workers.Go(func() {
		for range 1000 {
			for _, account := range []*clientpb.WebUIAccountState{checking, ready} {
				server.accountMu.Lock()
				server.account = account
				server.accountMu.Unlock()
			}
		}
	})
	defer workers.Wait()
	for range 100 {
		response := httptest.NewRecorder()
		server.monitoringRuntime(response, httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/api/runtime", nil))
		var snapshot struct {
			State   string
			Reason  string
			Account json.RawMessage
		}
		if err := json.Unmarshal(response.Body.Bytes(), &snapshot); err != nil {
			t.Fatal(err)
		}
		account := &clientpb.WebUIAccountState{}
		if err := protojson.Unmarshal(snapshot.Account, account); err != nil {
			t.Fatal(err)
		}
		if (snapshot.State == "checking") != (account.GetChecking() != nil) {
			t.Fatalf("mismatched snapshot: %s", response.Body.String())
		}
		if snapshot.State == "ready" && (account.GetAuthenticated() == nil || snapshot.Reason != "") {
			t.Fatalf("invalid ready snapshot: %s", response.Body.String())
		}
	}
}

func TestRuntimeSamplesScannerOnceAndReportsFailure(t *testing.T) {
	calls := 0
	server := &Server{monitoringRunning: true, account: accountStateMessage("authenticated", "", time.Now()), scannerStatus: func() string {
		calls++
		if calls == 1 {
			return "failed"
		}
		return "ready"
	}}
	response := httptest.NewRecorder()
	server.monitoringRuntime(response, httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/api/runtime", nil))
	var state struct{ State, Scanner string }
	if err := json.Unmarshal(response.Body.Bytes(), &state); err != nil {
		t.Fatal(err)
	}
	if calls != 1 || state.State != "scan_failed" || state.Scanner != "failed" {
		t.Fatalf("calls=%d state=%+v", calls, state)
	}
}

func TestMonitoringRuntimeReflectsWorkerNotSavedIntent(t *testing.T) {
	server := &Server{}
	check := func(want string) {
		t.Helper()
		response := httptest.NewRecorder()
		server.apiRoutes().ServeHTTP(response, httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/api/runtime", nil))
		var got monitoringRuntimeState
		if err := json.Unmarshal(response.Body.Bytes(), &got); err != nil {
			t.Fatal(err)
		}
		if response.Code != 200 || got.State != want {
			t.Fatalf("got %d %s, want %s", response.Code, response.Body.String(), want)
		}
		if want != "ready" && got.Reason == "" {
			t.Fatal("missing blocked reason")
		}
		var snapshot struct {
			Account json.RawMessage
			Tasks   json.RawMessage
		}
		if err := json.Unmarshal(response.Body.Bytes(), &snapshot); err != nil {
			t.Fatal(err)
		}
		account := &clientpb.WebUIAccountState{}
		if err := protojson.Unmarshal(snapshot.Account, account); err != nil {
			t.Fatal(err)
		}
		if want == "checking" && account.GetChecking() == nil {
			t.Fatal("checking reason with non-checking account")
		}
		if want == "ready" && account.GetAuthenticated() == nil {
			t.Fatal("ready with unauthenticated account")
		}
		if err := protojson.Unmarshal(snapshot.Tasks, &clientpb.WebUITaskStatusResponse{}); err != nil {
			t.Fatal(err)
		}
	}
	// No repository, browser factory or clock: runtime reads must be memory-only.
	for range 100 {
		check("stopped")
	}
	check("stopped")
	server.SetMonitoringRunning(true)
	check("checking")
	server.account = accountStateMessage("unauthenticated", "", time.Now())
	check("login_required")
	server.account = accountStateMessage("authenticated", "", time.Now())
	check("ready")
	server.bookingPreparationError = "failed"
	check("preparation_failed")
	server.monitoringBlockReason = func() string { return "Retry-After deadline" }
	check("rate_limited")
	server.monitoringBlockReason = nil
	server.bookingPreparationError = ""
	check("ready")
	server.SetMonitoringRunning(false)
	check("stopped")
}

func TestMonitorChangeCancelsScanAndWakesWorker(t *testing.T) {
	canceled := 0
	server := &Server{monitoringChanged: func() { canceled++ }, executionReady: make(chan struct{}, 1)}
	for range 2 {
		server.monitorConfigurationChanged(t.Context())
		select {
		case <-server.ExecutionAvailable():
		default:
			t.Fatal("worker was not woken after monitor toggle")
		}
	}
	if canceled != 2 {
		t.Fatalf("scan invalidations = %d", canceled)
	}
}

func TestMonitorHTTPEnableStopTwiceWithoutRestart(t *testing.T) {
	store := memoryrepo.New()
	monitor := monitorProtoFixture("preset", "movie", "Movie", []string{"2026-09-11"}, clientpb.MonitorState_builder{Stopped: clientpb.MonitorStopped_builder{}.Build()}.Build(), "")
	if err := store.PutMonitor(t.Context(), resourceFromMonitor(monitor)); err != nil {
		t.Fatal(err)
	}
	var demand bool
	changes := 0
	server := &Server{
		repository: store, userID: "user", clock: webTestClock{time.Now()}, ids: &webAtomicIDs{}, rootContext: t.Context(),
		tasks: make(map[string]*clientpb.WebUITaskState), taskCancels: make(map[string]context.CancelFunc),
		account:           accountStateMessage("authenticated", "", time.Now()),
		monitoringChanged: func() { changes++ }, bookingDemandChanged: func(active bool) { demand = active },
		executionReady: make(chan struct{}, 1),
	}
	for range 2 {
		for _, enable := range []bool{true, false} {
			resource, err := store.GetMonitor(t.Context(), "monitor")
			if err != nil {
				t.Fatal(err)
			}
			payload, err := protojson.Marshal(clientpb.WebUIMonitorRetryRequest_builder{Monitor: resource.GetMonitor()}.Build())
			if err != nil {
				t.Fatal(err)
			}
			path := "/api/monitors/stop"
			if enable {
				path = "/api/monitors/retry"
			}
			response := httptest.NewRecorder()
			server.apiRoutes().ServeHTTP(response, httptest.NewRequestWithContext(t.Context(), http.MethodPost, path, bytes.NewReader(payload)))
			if response.Code < 200 || response.Code >= 300 {
				t.Fatalf("%s: %d %s", path, response.Code, response.Body.String())
			}
			resource, err = store.GetMonitor(t.Context(), "monitor")
			if err != nil {
				t.Fatal(err)
			}
			if (resource.GetMonitor().GetState().GetPending() != nil) != enable || demand != enable {
				t.Fatalf("enable=%v: state=%v demand=%v", enable, resource.GetMonitor().GetState(), demand)
			}
		}
	}
	if changes != 4 {
		t.Fatalf("monitor changes = %d", changes)
	}
}
