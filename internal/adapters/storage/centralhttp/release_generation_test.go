package centralhttp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	central "github.com/cineko-org/contracts/v3"
)

func TestStoreObservesReleaseGenerationOnOrdinaryResponses(t *testing.T) {
	now := time.Now().UTC()
	var generation atomic.Int64
	generation.Store(17)
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set(central.ReleaseGenerationHeader, fmt.Sprintf("%d", generation.Load()))
		switch request.URL.Path {
		case "/v1/auth/exchange":
			writeSession(t, writer, "access", "refresh", now)
		case "/v1/devices/install":
			_ = json.NewEncoder(writer).Encode(central.ClientDevice{InstallationID: "install"})
		default:
			http.NotFound(writer, request)
		}
	}))
	t.Cleanup(server.Close)
	store, err := Open(t.Context(), Config{
		BaseURL: server.URL, UserID: "user", AccessToken: "credential", HTTPClient: server.Client(),
	})
	if err != nil || store.ReleaseGeneration() != 17 {
		t.Fatalf("Open() generation = %d, %v", store.ReleaseGeneration(), err)
	}
	generation.Store(18)
	if _, err := store.RegisterDevice(t.Context(), central.ClientDevice{InstallationID: "install"}); err != nil {
		t.Fatal(err)
	}
	select {
	case <-store.UpdateRequired():
	default:
		t.Fatal("release generation change did not request an update")
	}
}

func TestStoreObservesReleaseGenerationOnEventStreamHeartbeat(t *testing.T) {
	now := time.Now().UTC()
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set(central.ReleaseGenerationHeader, "17")
		switch request.URL.Path {
		case "/v1/auth/exchange":
			writeSession(t, writer, "access", "refresh", now)
		case "/v1/events/stream":
			writer.Header().Set("Content-Type", "text/event-stream")
			_, _ = fmt.Fprintf(writer, "event: cineko.control\ndata: {\"protocol\":%d,\"releaseGeneration\":18,\"cursor\":0,\"action\":\"heartbeat\"}\n\n", central.ProtocolVersion)
		default:
			http.NotFound(writer, request)
		}
	}))
	t.Cleanup(server.Close)
	store, err := Open(t.Context(), Config{
		BaseURL: server.URL, UserID: "user", AccessToken: "credential", HTTPClient: server.Client(),
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), time.Second)
	defer cancel()
	if err := store.WatchEvents(ctx); err != nil {
		t.Fatal(err)
	}
	select {
	case <-store.UpdateRequired():
	default:
		t.Fatal("event stream heartbeat did not request an update")
	}
}

func TestStoreRejectsMalformedReleaseGeneration(t *testing.T) {
	store, err := newStore("http://localhost", "user", nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.observeReleaseGeneration("broken"); err == nil {
		t.Fatal("malformed release generation accepted")
	}
	if err := store.observeReleaseGeneration("0"); err == nil {
		t.Fatal("zero release generation accepted")
	}
	if err := store.observeReleaseGeneration(""); err == nil {
		t.Fatal("missing release generation accepted")
	}
}

func TestStoreRejectsCentralResponseWithoutReleaseGeneration(t *testing.T) {
	now := time.Now().UTC()
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writeSession(t, writer, "access", "refresh", now)
	}))
	t.Cleanup(server.Close)
	if _, err := Open(t.Context(), Config{
		BaseURL: server.URL, UserID: "user", AccessToken: "credential", HTTPClient: server.Client(),
	}); err == nil {
		t.Fatal("Central response without a release generation accepted")
	}
}

func TestEventStreamRefreshesUnauthorizedSessionOnce(t *testing.T) {
	now := time.Now().UTC()
	var refreshes atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set(central.ReleaseGenerationHeader, "17")
		switch request.URL.Path {
		case "/v1/auth/exchange":
			writeSession(t, writer, "access-1", "refresh-1", now)
		case "/v1/auth/refresh":
			refreshes.Add(1)
			writeSession(t, writer, "access-2", "refresh-2", now)
		case "/v1/events/stream":
			if request.Header.Get("Authorization") == "Bearer access-1" {
				writer.WriteHeader(http.StatusUnauthorized)
				_ = json.NewEncoder(writer).Encode(map[string]any{
					"error": map[string]any{"code": "unauthorized", "message": "expired"},
				})
				return
			}
			writer.Header().Set("Content-Type", "text/event-stream")
			_, _ = fmt.Fprintf(writer, "event: cineko.control\ndata: {\"protocol\":%d,\"releaseGeneration\":18,\"cursor\":0,\"action\":\"heartbeat\"}\n\n", central.ProtocolVersion)
		default:
			http.NotFound(writer, request)
		}
	}))
	t.Cleanup(server.Close)
	store, err := Open(t.Context(), Config{
		BaseURL: server.URL, UserID: "user", AccessToken: "credential", HTTPClient: server.Client(),
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.WatchEvents(t.Context()); err != nil {
		t.Fatal(err)
	}
	if refreshes.Load() != 1 {
		t.Fatalf("event stream refreshes = %d", refreshes.Load())
	}
}

func TestEventStreamRejectsMissingReleaseGenerationHeader(t *testing.T) {
	now := time.Now().UTC()
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		switch request.URL.Path {
		case "/v1/auth/exchange":
			writer.Header().Set(central.ReleaseGenerationHeader, "17")
			writeSession(t, writer, "access", "refresh", now)
		case "/v1/events/stream":
			writer.Header().Set("Content-Type", "text/event-stream")
			_, _ = writer.Write([]byte(": heartbeat\n\n"))
		default:
			http.NotFound(writer, request)
		}
	}))
	t.Cleanup(server.Close)
	store, err := Open(t.Context(), Config{
		BaseURL: server.URL, UserID: "user", AccessToken: "credential", HTTPClient: server.Client(),
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.watchEventsOnce(t.Context()); err == nil {
		t.Fatal("event stream without a release generation accepted")
	}
}

func TestLaunchedStoreBindsCentralSessionToReleaseGeneration(t *testing.T) {
	now := time.Now().UTC()
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set(central.ReleaseGenerationHeader, "17")
		if request.URL.Path != "/v1/client-sessions" {
			http.NotFound(writer, request)
			return
		}
		var input central.ClientSessionExchangeRequest
		decodeRequest(t, request, &input)
		if input.LaunchTicket != "ticket" || input.ClientNonce != "0123456789abcdef" {
			t.Errorf("session exchange = %+v", input)
		}
		_ = json.NewEncoder(writer).Encode(central.AuthExchangeResponse{ // #nosec G117 -- test-only session fixture.
			AccessToken: "access", ExpiresAt: now.Add(time.Hour),
			RefreshToken: "refresh", RefreshExpiresAt: now.Add(24 * time.Hour),
			User: central.ClientUser{ID: "user"},
			Launch: &central.ClientLaunchContext{
				InstallationID: "install", DeviceID: "device", ReleaseGeneration: 17,
				ClientVersion: "2.3.4", ArtifactSHA256: "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef",
				Protocol: central.ProtocolVersion, BrowserRevision: "1228",
				BrowserArtifactSHA256:    "1123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef",
				PlaywrightVersion:        "1.60.0",
				PlaywrightArtifactSHA256: "2123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef",
			},
		})
	}))
	t.Cleanup(server.Close)
	config := LaunchConfig{
		BaseURL: server.URL, LaunchTicket: "ticket", ClientNonce: "0123456789abcdef",
		InstallationID: "install", DeviceID: "device", ReleaseGeneration: 17,
		ClientVersion: "2.3.4", ArtifactSHA256: "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef",
		Protocol: central.ProtocolVersion, BrowserRevision: "1228",
		BrowserArtifactSHA256:    "1123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef",
		PlaywrightVersion:        "1.60.0",
		PlaywrightArtifactSHA256: "2123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef",
		HTTPClient:               server.Client(),
	}
	store, err := OpenLaunched(t.Context(), config)
	if err != nil || store.ReleaseGeneration() != 17 {
		t.Fatalf("OpenLaunched() = %+v, %v", store, err)
	}
	for name, mutate := range map[string]func(*LaunchConfig){
		"browser artifact": func(value *LaunchConfig) {
			value.BrowserArtifactSHA256 = "3123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
		},
		"playwright version": func(value *LaunchConfig) { value.PlaywrightVersion = "1.61.0" },
		"playwright artifact": func(value *LaunchConfig) {
			value.PlaywrightArtifactSHA256 = "4123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
		},
	} {
		t.Run(name+" mismatch", func(t *testing.T) {
			mismatched := config
			mutate(&mismatched)
			if _, err := OpenLaunched(t.Context(), mismatched); err == nil {
				t.Fatal("mismatched launch context accepted")
			}
		})
	}
	invalidDigest := config
	invalidDigest.BrowserArtifactSHA256 = "not-a-digest"
	if _, err := OpenLaunched(t.Context(), invalidDigest); err == nil {
		t.Fatal("invalid browser artifact digest accepted")
	}
	config.ReleaseGeneration = 18
	if _, err := OpenLaunched(t.Context(), config); !errors.Is(err, ErrReleaseUpdateRequired) {
		t.Fatalf("mismatched launch generation error = %v", err)
	}
}

func TestStoreReadsAndWritesCentralSettings(t *testing.T) {
	now := time.Now().UTC()
	revision := int64(4)
	settings := json.RawMessage(`{"network":{"mode":"direct"}}`)
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set(central.ReleaseGenerationHeader, "17")
		switch {
		case request.URL.Path == "/v1/auth/exchange":
			writeSession(t, writer, "access", "refresh", now)
		case request.URL.Path == "/v1/settings" && request.Method == http.MethodGet:
			_ = json.NewEncoder(writer).Encode(resourceEnvelope{Kind: "settings", ID: "settings", Revision: revision, Data: settings})
		case request.URL.Path == "/v1/settings" && request.Method == http.MethodPut:
			if request.Header.Get("If-Match") != "4" || request.Header.Get("If-None-Match") != "" ||
				request.Header.Get("Idempotency-Key") == "" {
				t.Errorf("settings mutation headers = %v", request.Header)
			}
			var input struct {
				Data json.RawMessage `json:"data"`
			}
			decodeRequest(t, request, &input)
			settings = input.Data
			revision++
			_ = json.NewEncoder(writer).Encode(resourceEnvelope{Kind: "settings", ID: "settings", Revision: revision, Data: settings})
		default:
			http.NotFound(writer, request)
		}
	}))
	t.Cleanup(server.Close)
	store, err := Open(t.Context(), Config{BaseURL: server.URL, UserID: "user", AccessToken: "credential", HTTPClient: server.Client()})
	if err != nil {
		t.Fatal(err)
	}
	var current map[string]any
	settingsRevision, err := store.GetSettings(t.Context(), &current)
	if err != nil || settingsRevision != 4 || current["network"] == nil {
		t.Fatalf("GetSettings() = %#v, revision %d, %v", current, settingsRevision, err)
	}
	if err := store.PutSettings(t.Context(), map[string]any{"hooks": []string{"discord"}}, settingsRevision); err != nil {
		t.Fatal(err)
	}
}

func TestStoreCreatesCentralSettingsWithExclusivePrecondition(t *testing.T) {
	now := time.Now().UTC()
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set(central.ReleaseGenerationHeader, "17")
		switch {
		case request.URL.Path == "/v1/auth/exchange":
			writeSession(t, writer, "access", "refresh", now)
		case request.URL.Path == "/v1/settings" && request.Method == http.MethodPut:
			if request.Header.Get("If-None-Match") != "*" || request.Header.Get("If-Match") != "" {
				t.Errorf("settings create precondition = %v", request.Header)
			}
			_ = json.NewEncoder(writer).Encode(resourceEnvelope{
				Kind: "settings", ID: "settings", Revision: 1, Data: json.RawMessage(`{"network":null}`),
			})
		default:
			http.NotFound(writer, request)
		}
	}))
	t.Cleanup(server.Close)
	store, err := Open(t.Context(), Config{
		BaseURL: server.URL, UserID: "user", AccessToken: "credential", HTTPClient: server.Client(),
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.PutSettings(t.Context(), map[string]any{"network": nil}, 0); err != nil {
		t.Fatal(err)
	}
}
