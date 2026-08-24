package egress

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

func TestValidateConfigStandardProxies(t *testing.T) {
	t.Parallel()
	var calls atomic.Int32
	config := Config{
		Proxies: []Proxy{{Server: "http://one:1"}, {Server: "socks5://two:2"}},
		Probe: func(_ context.Context, proxy Proxy) error {
			calls.Add(1)
			if proxy.Server == "socks5://two:2" {
				return errors.New("offline")
			}
			return nil
		},
	}
	if err := ValidateConfig(context.Background(), config); err == nil || !strings.Contains(err.Error(), "configured proxy 2") {
		t.Fatalf("ValidateConfig() error = %v", err)
	}
	if calls.Load() != 2 {
		t.Fatalf("probe calls = %d", calls.Load())
	}
	config.Probe = func(context.Context, Proxy) error { return nil }
	if err := ValidateConfig(context.Background(), config); err != nil {
		t.Fatalf("ValidateConfig(success) error = %v", err)
	}
	if err := ValidateConfig(context.Background(), Config{Proxies: []Proxy{{Server: "ftp://bad:1"}}}); err == nil {
		t.Fatal("ValidateConfig(invalid) error = nil")
	}
}

func TestProbeProxy(t *testing.T) {
	oldURL := defaultProbeURL
	t.Cleanup(func() { defaultProbeURL = oldURL })
	proxyServer := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.Header.Get("Proxy-Authorization") == "" {
			http.Error(writer, "auth required", http.StatusProxyAuthRequired)
			return
		}
		writer.WriteHeader(http.StatusNoContent)
	}))
	defer proxyServer.Close()
	defaultProbeURL = "http://health.example.test/status"
	parsed, err := ParseProxy(proxyServer.URL)
	if err != nil {
		t.Fatal(err)
	}
	parsed.Username, parsed.Password = "user", "password"
	if err := probeProxy(context.Background(), parsed); err != nil {
		t.Fatalf("probeProxy() error = %v", err)
	}
	if err := ValidateConfig(context.Background(), Config{Proxies: []Proxy{parsed}}); err != nil {
		t.Fatalf("ValidateConfig(default probe) error = %v", err)
	}
	parsed.Username = ""
	if err := probeProxy(context.Background(), parsed); err == nil || !strings.Contains(err.Error(), "407") {
		t.Fatalf("probeProxy(status) error = %v", err)
	}
	defaultProbeURL = "://invalid"
	if err := probeProxy(context.Background(), parsed); err == nil {
		t.Fatal("probeProxy(invalid request) error = nil")
	}
	if err := probeProxy(context.Background(), Proxy{Server: "://invalid"}); err == nil {
		t.Fatal("probeProxy(invalid proxy) error = nil")
	}
	closedProxy := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	closedURL := closedProxy.URL
	closedProxy.Close()
	defaultProbeURL = "http://health.example.test/status"
	closed, err := ParseProxy(closedURL)
	if err != nil {
		t.Fatal(err)
	}
	if err := probeProxy(context.Background(), closed); err == nil {
		t.Fatal("probeProxy(connection failure) error = nil")
	}
}

func TestDefaultProbeURLIsProviderNeutral(t *testing.T) {
	if defaultProbeURL != "https://api.ipify.org" {
		t.Fatalf("defaultProbeURL = %q", defaultProbeURL)
	}
}
