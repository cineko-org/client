package probe

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/cineko-org/probe/v2/internal/egress"
)

func TestScannerSettingsSaveValidateRedactAndPreserveOnFailure(t *testing.T) {
	for _, key := range []string{"CINEKO_SOXY_URL", "CINEKO_SOXY_API_TOKEN_FILE", "CINEKO_SCAN_PROXIES_FILE", "CINEKO_SOXY_API_TOKEN", "CINEKO_SCAN_PROXIES"} {
		t.Setenv(key, "")
	}
	var status atomic.Int32
	status.Store(200)
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if r.Method != "GET" || r.URL.Path != "/v1/slots" {
			t.Error("settings validation created a session")
		}
		if r.Header.Get("Authorization") != "Bearer test-token" {
			t.Error("wrong token")
		}
		w.WriteHeader(int(status.Load()))
		_, _ = w.Write([]byte(`{"slots":[{"id":"one","status":"available","current_ip":"192.0.2.1"}]}`))
	}))
	defer server.Close()
	dir := t.TempDir()
	path := filepath.Join(dir, "scanner", "egress.json")
	scanner, err := NewLocalScanner(LocalScannerConfig{DataDir: dir, EgressConfigPath: path})
	if err != nil {
		t.Fatal(err)
	}
	defer scanner.Close()
	empty, err := scanner.GetSoxySettings()
	if err != nil || empty.HasToken || empty.URL != "" {
		t.Fatal("missing configuration not editable")
	}
	saved, err := scanner.SaveSoxySettings(t.Context(), server.URL, "test-token")
	if err != nil {
		t.Fatal(err)
	}
	encoded, _ := json.Marshal(saved)
	if !saved.HasToken || strings.Contains(string(encoded), "test-token") {
		t.Fatal("token exposed")
	}
	info, _ := os.Stat(path)
	if info.Mode().Perm() != 0600 {
		t.Fatal("settings permissions")
	}
	before, _ := os.ReadFile(path)
	_, err = scanner.SaveSoxySettings(t.Context(), server.URL, "")
	if err != nil {
		t.Fatal("existing token was not preserved:", err)
	}
	after, _ := os.ReadFile(path)
	if string(before) != string(after) {
		t.Fatal("unchanged settings rewritten")
	}
	status.Store(401)
	_, err = scanner.SaveSoxySettings(t.Context(), server.URL, "test-token")
	if err == nil {
		t.Fatal("invalid authentication accepted")
	}
	after, _ = os.ReadFile(path)
	if string(before) != string(after) {
		t.Fatal("failed save changed settings")
	}
	config, err := egress.LocalScanConfig(path)
	if err != nil || config.SoxyToken != "test-token" || !config.RequireProxy {
		t.Fatal("persisted settings cannot be loaded")
	}
	priorCalls := calls.Load()
	_, err = scanner.SaveSoxySettings(t.Context(), "http://127.0.0.1:1", "")
	if err == nil || calls.Load() != priorCalls {
		t.Fatal("old token reused with changed URL")
	}
}

func TestScannerSettingsBrokenFileRemainsRepairable(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "egress.json")
	if err := os.WriteFile(path, []byte("broken JSON"), 0600); err != nil {
		t.Fatal(err)
	}
	scanner, err := NewLocalScanner(LocalScannerConfig{DataDir: dir, EgressConfigPath: path})
	if err != nil {
		t.Fatal("settings UI cannot open:", err)
	}
	defer scanner.Close()
	if _, err := scanner.GetSoxySettings(); err == nil {
		t.Fatal("broken settings hidden")
	}
}
