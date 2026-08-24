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

	"github.com/cineko-org/client/internal/logging"
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
