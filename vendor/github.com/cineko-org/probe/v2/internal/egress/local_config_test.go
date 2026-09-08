package egress

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

func TestRequiredScannerProxyNeverFallsBack(t *testing.T) {
	for _, purpose := range []Purpose{PurposeScan, PurposeSession} {
		manager, err := New(Config{RequireProxy: true})
		if err != nil {
			t.Fatal(err)
		}
		lease, err := manager.Acquire(t.Context(), purpose)
		if err == nil || lease != nil {
			t.Fatal("missing proxy acquired a direct lease")
		}
	}
}

func TestScannerSoxyFailureNeverFallsBack(t *testing.T) {
	for _, status := range []int{401, 429, 503} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			calls := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls++
				w.WriteHeader(status)
			}))
			defer server.Close()
			manager, err := New(Config{RequireProxy: true, SoxyURL: server.URL, SoxyToken: "test"})
			if err != nil {
				t.Fatal(err)
			}
			lease, err := manager.Acquire(t.Context(), PurposeScan)
			if err == nil || lease != nil || calls != 1 {
				t.Fatalf("lease=%v err=%v calls=%d", lease != nil, err, calls)
			}
		})
	}
}

func TestScannerSoxyLeaseUsesReturnedProxyAndReleases(t *testing.T) {
	released := false
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer test" {
			t.Error("missing authentication")
		}
		switch r.Method + " " + r.URL.Path {
		case "GET /v1/slots":
			_, _ = w.Write([]byte(`{"slots":[{"id":"slot","status":"available","current_ip":"192.0.2.1"}]}`))
		case "POST /v1/sessions":
			w.WriteHeader(http.StatusCreated)
			_, _ = w.Write([]byte(`{"id":"lease","status":"active","ready":true,"proxy":{"scheme":"http","host":"127.0.0.1","port":19001,"username":"u","password":"p"}}`))
		case "DELETE /v1/sessions/lease":
			released = true
			w.WriteHeader(http.StatusNoContent)
		default:
			t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
			w.WriteHeader(500)
		}
	}))
	defer server.Close()
	manager, err := New(Config{RequireProxy: true, SoxyURL: server.URL, SoxyToken: "test"})
	if err != nil {
		t.Fatal(err)
	}
	lease, err := manager.Acquire(t.Context(), PurposeScan)
	if err != nil {
		t.Fatal(err)
	}
	proxy := lease.Proxy()
	if proxy == nil || proxy.Server != "http://127.0.0.1:19001" || proxy.Username != "u" {
		t.Fatal("wrong proxy")
	}
	if err := lease.Close(); err != nil {
		t.Fatal(err)
	}
	if !released {
		t.Fatal("lease was not released")
	}
}

func TestLocalScanConfigurationPersistsWithoutEnvironment(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "token"), []byte("test-token"), 0600); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "egress.json")
	if err := os.WriteFile(path, []byte(`{"soxy_url":"http://127.0.0.1:8080","token_file":"token"}`), 0600); err != nil {
		t.Fatal(err)
	}
	config, err := LocalScanConfig(path)
	if err != nil || !config.RequireProxy || config.SoxyToken != "test-token" {
		t.Fatalf("local config failed: %v", err)
	}
	if err := os.Remove(filepath.Join(dir, "token")); err != nil {
		t.Fatal(err)
	}
	_, err = LocalScanConfig(path)
	if err == nil || !errors.Is(err, os.ErrNotExist) {
		t.Fatal("missing token ignored")
	}
}
