package webui

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/cineko-org/client/internal/logging"
	"github.com/cineko-org/probe/v2/networkcapture"
)

func TestLogsReturnsFilteredLocalSnapshot(t *testing.T) {
	path := filepath.Join(t.TempDir(), "client.log")
	if err := os.WriteFile(path, []byte(
		`{"time":"now","level":"WARN","msg":"poster","event":"poster.missing","scenario":"poster_collection"}`+"\n"+
			`{"time":"later","level":"ERROR","msg":"seat","event":"seat.failed","scenario":"seat_selection"}`+"\n",
	), 0o600); err != nil {
		t.Fatal(err)
	}
	server := &Server{logPath: path}
	request := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/api/logs?min_level=error", nil)
	response := httptest.NewRecorder()
	server.logs(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", response.Code, response.Body.String())
	}
	var snapshot logging.Snapshot
	if err := json.Unmarshal(response.Body.Bytes(), &snapshot); err != nil {
		t.Fatal(err)
	}
	if snapshot.Matching != 1 || len(snapshot.Entries) != 1 || snapshot.Entries[0].Event != "seat.failed" {
		t.Fatalf("snapshot = %+v", snapshot)
	}
}

func TestRecordClientLogPersistsStructuredWarning(t *testing.T) {
	var output strings.Builder
	restore := logging.SetOutput(&output)
	defer restore()
	server := &Server{}
	request := httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/api/logs/client", strings.NewReader(
		`{"level":"warn","event":"ui.contract.changed","scenario":"seat_selection","operation":"render","expected":"seat map","observed":"empty","fields":{"route":"/presets"}}`,
	))
	response := httptest.NewRecorder()
	server.recordClientLog(response, request)
	if response.Code != http.StatusNoContent {
		t.Fatalf("status = %d, body = %s", response.Code, response.Body.String())
	}
	if value := output.String(); !strings.Contains(value, `"level":"WARN"`) || !strings.Contains(value, `"scenario":"seat_selection"`) || !strings.Contains(value, `"route":"/presets"`) {
		t.Fatalf("log = %s", value)
	}
}

func TestNetworkLogsExposeManifestAndBody(t *testing.T) {
	root := t.TempDir()
	store, err := networkcapture.NewStore(root, nil)
	if err != nil {
		t.Fatal(err)
	}
	result, err := store.Save(context.Background(), networkcapture.Record{
		Exchange: networkcapture.Exchange{
			ID: "exchange-1", Service: "client", Scenario: "booking_browser", Transport: "chromium",
			StartedAt: time.Now().Add(-time.Second), CompletedAt: time.Now(), Outcome: "failed", Error: "HTTP 429",
			Request:  networkcapture.Request{Method: http.MethodPost, URL: "https://cgv.test/schedule?date=20260829"},
			Response: &networkcapture.Response{Status: http.StatusTooManyRequests, Headers: []networkcapture.Header{{Name: "Retry-After", Value: "60"}}},
		},
		RequestBody: []byte("request-body"), ResponseBody: []byte("response-body"),
		RequestContentType: "text/plain", ResponseContentType: "text/plain",
	})
	if err != nil {
		t.Fatal(err)
	}
	server := &Server{networkCaptureDir: root}

	listRequest := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/api/logs/network?status=429", nil)
	listResponse := httptest.NewRecorder()
	server.networkLogs(listResponse, listRequest)
	if listResponse.Code != http.StatusOK || !strings.Contains(listResponse.Body.String(), result.ID) {
		t.Fatalf("list status/body = %d/%s", listResponse.Code, listResponse.Body.String())
	}

	detailRequest := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/api/logs/network/"+result.ID, nil)
	detailRequest.SetPathValue("id", result.ID)
	detailResponse := httptest.NewRecorder()
	server.networkLog(detailResponse, detailRequest)
	if detailResponse.Code != http.StatusOK || !strings.Contains(detailResponse.Body.String(), `"status":429`) {
		t.Fatalf("detail status/body = %d/%s", detailResponse.Code, detailResponse.Body.String())
	}

	bodyRequest := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/api/logs/network/"+result.ID+"/body/response", nil)
	bodyRequest.SetPathValue("id", result.ID)
	bodyRequest.SetPathValue("side", "response")
	bodyResponse := httptest.NewRecorder()
	server.networkLogBody(bodyResponse, bodyRequest)
	if bodyResponse.Code != http.StatusOK || bodyResponse.Body.String() != "response-body" {
		t.Fatalf("body status/body = %d/%q", bodyResponse.Code, bodyResponse.Body.String())
	}
}

func TestClearOperationLogsUsesConfiguredBoundary(t *testing.T) {
	called := false
	now := time.Date(2026, 8, 25, 15, 0, 0, 0, time.Local)
	server := &Server{
		clock:     webTestClock{now: now},
		clearLogs: func(context.Context) error { called = true; return nil },
	}
	request := httptest.NewRequestWithContext(t.Context(), http.MethodDelete, "/api/logs", nil)
	response := httptest.NewRecorder()
	server.clearOperationLogs(response, request)
	if response.Code != http.StatusNoContent || !called {
		t.Fatalf("status/called = %d/%v, body = %s", response.Code, called, response.Body.String())
	}
	if got := server.observabilityStart(); !got.Equal(now) {
		t.Fatalf("observability start = %s", got)
	}
}
