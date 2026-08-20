package egress

import (
	"bytes"
	"context"
	"errors"
	"io"
	"reflect"
	"testing"
)

type errorReader struct{ err error }

func (reader errorReader) Read([]byte) (int, error) { return 0, reader.err }

func TestNewFromEnvironmentIgnoresRetiredSoxyVariables(t *testing.T) {
	t.Setenv("CINEKO_SOXY_URL", "https://retired.example.test")
	t.Setenv("CINEKO_SOXY_API_TOKEN", "retired")
	t.Setenv("CINEKO_SOXY_SESSION_TTL", "invalid")
	t.Setenv("CINEKO_SCAN_PROXIES", "")
	manager, err := NewFromEnvironment()
	if err != nil {
		t.Fatalf("NewFromEnvironment() error = %v", err)
	}
	lease, err := manager.Acquire(context.Background(), PurposeScan)
	if err != nil || lease.Proxy() != nil {
		t.Fatalf("retired Soxy variables affected Client: proxy=%+v error=%v", lease.Proxy(), err)
	}
}

func TestNewFromEnvironmentRejectsInvalidStandardProxy(t *testing.T) {
	t.Setenv("CINEKO_SCAN_PROXIES", "ftp://127.0.0.1:21")
	if _, err := NewFromEnvironment(); err == nil {
		t.Fatal("NewFromEnvironment(invalid proxy) error = nil")
	}
}

func TestConfigFromLookup(t *testing.T) {
	t.Parallel()
	values := map[string]string{
		"CINEKO_SOXY_URL":       "https://retired.example.test",
		"CINEKO_SOXY_API_TOKEN": "retired",
		"CINEKO_SCAN_PROXIES":   "http://alpha:one@127.0.0.1:11001, socks5://127.0.0.1:11002/",
	}
	config, err := configFromLookup(func(key string) (string, bool) {
		value, exists := values[key]
		return value, exists
	})
	if err != nil {
		t.Fatalf("configFromLookup() error = %v", err)
	}
	want := []Proxy{
		{Server: "http://127.0.0.1:11001", Username: "alpha", Password: "one"},
		{Server: "socks5://127.0.0.1:11002"},
	}
	if !reflect.DeepEqual(config.ScanProxies, want) {
		t.Fatalf("scan proxies = %+v, want %+v", config.ScanProxies, want)
	}
}

func TestConfigFromLookupRejectsInvalidProxy(t *testing.T) {
	t.Parallel()
	_, err := configFromLookup(func(key string) (string, bool) {
		if key == "CINEKO_SCAN_PROXIES" {
			return "ftp://127.0.0.1:21", true
		}
		return "", false
	})
	if err == nil {
		t.Fatal("configFromLookup() error = nil")
	}
}

func TestParseProxy(t *testing.T) {
	t.Parallel()
	for _, rawURL := range []string{
		"", "%", "ftp://proxy.example:21", "http://proxy.example", "http://proxy.example:0",
		"http://proxy.example:65536", "http://proxy.example:8080/path", "http://proxy.example:8080?q=1",
		"http://proxy.example:8080#fragment",
	} {
		if _, err := ParseProxy(rawURL); err == nil {
			t.Errorf("ParseProxy(%q) error = nil", rawURL)
		}
	}
	proxy, err := ParseProxy("https://user:p%40ss@[2001:db8::1]:8443/")
	if err != nil {
		t.Fatalf("ParseProxy() error = %v", err)
	}
	if proxy.Server != "https://[2001:db8::1]:8443" || proxy.Username != "user" || proxy.Password != "p@ss" {
		t.Fatalf("ParseProxy() = %+v", proxy)
	}
}

func TestManagerDirectAndProxyPolicies(t *testing.T) {
	t.Parallel()
	direct, err := New(Config{})
	if err != nil {
		t.Fatal(err)
	}
	lease, err := direct.Acquire(context.Background(), PurposeSession)
	if err != nil || lease.Proxy() != nil {
		t.Fatalf("direct lease = %+v, %v", lease.Proxy(), err)
	}
	if err := lease.Close(); err != nil {
		t.Fatal(err)
	}
	if !errors.Is(lease.Context().Err(), context.Canceled) {
		t.Fatalf("lease context error = %v", lease.Context().Err())
	}
	if err := lease.Close(); err != nil {
		t.Fatal(err)
	}

	manager, err := New(Config{
		Proxies: []Proxy{
			{Server: "http://embedded:secret@one:1"}, // #nosec G101 -- synthetic credential verifies parsing.
			{Server: "socks5://two:2", Username: "override", Password: "password"},
		},
		Random: bytes.NewReader([]byte{0, 1}),
	})
	if err != nil {
		t.Fatal(err)
	}
	session, err := manager.Acquire(context.Background(), PurposeSession)
	if err != nil || session.Proxy() == nil || session.Proxy().Username != "embedded" {
		t.Fatalf("session = %+v, error = %v", session.Proxy(), err)
	}
	_ = session.Close()
	scan, err := manager.Acquire(context.Background(), PurposeScan)
	if err != nil || scan.Proxy() == nil || scan.Proxy().Server != "socks5://two:2" || scan.Proxy().Username != "override" {
		t.Fatalf("scan = %+v, error = %v", scan.Proxy(), err)
	}
	_ = scan.Close()
	manager.random = errorReader{err: io.ErrUnexpectedEOF}
	if _, err := manager.Acquire(context.Background(), PurposeSession); !errors.Is(err, io.ErrUnexpectedEOF) {
		t.Fatalf("Acquire(session random failure) error = %v", err)
	}
}

func TestManagerLegacyScanPoolAndInvalidBoundaries(t *testing.T) {
	t.Parallel()
	manager, err := New(Config{
		ScanProxies: []Proxy{{Server: "http://one:1"}, {Server: "http://two:2"}},
		Random:      bytes.NewReader([]byte{1}),
	})
	if err != nil {
		t.Fatal(err)
	}
	lease, err := manager.Acquire(context.Background(), PurposeScan)
	if err != nil || lease.Proxy() == nil || lease.Proxy().Server != "http://two:2" {
		t.Fatalf("scan lease = %+v, %v", lease.Proxy(), err)
	}
	manager.random = errorReader{err: io.ErrUnexpectedEOF}
	if _, err := manager.Acquire(context.Background(), PurposeScan); !errors.Is(err, io.ErrUnexpectedEOF) {
		t.Fatalf("Acquire(random failure) error = %v", err)
	}
	for _, config := range []Config{
		{Proxies: []Proxy{{Server: "ftp://bad:1"}}},
		{ScanProxies: []Proxy{{Server: "ftp://bad:1"}}},
	} {
		if _, err := New(config); err == nil {
			t.Errorf("New(%+v) error = nil", config)
		}
	}
	if _, err := manager.Acquire(nil, PurposeSession); err == nil { //nolint:staticcheck // verifies nil boundary.
		t.Fatal("Acquire(nil) error = nil")
	}
	if _, err := manager.Acquire(context.Background(), Purpose("other")); err == nil {
		t.Fatal("Acquire(other) error = nil")
	}
}

func TestRandomIndexErrors(t *testing.T) {
	t.Parallel()
	if _, err := randomIndex(bytes.NewReader(nil), 0); err == nil {
		t.Fatal("randomIndex(empty) error = nil")
	}
	if _, err := randomIndex(errorReader{err: io.ErrClosedPipe}, 2); !errors.Is(err, io.ErrClosedPipe) {
		t.Fatalf("randomIndex(error) = %v", err)
	}
}
