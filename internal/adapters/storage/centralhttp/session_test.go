package centralhttp

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	central "github.com/cineko-org/contracts/v3"
)

func TestStoreRefreshesExpiredSessionOnceAcrossConcurrentRequests(t *testing.T) {
	now := time.Now().UTC()
	var refreshes atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set(central.ReleaseGenerationHeader, "17")
		switch request.URL.Path {
		case "/v1/auth/exchange":
			writeSession(t, writer, "access-1", "refresh-1", now)
		case "/v1/auth/refresh":
			var input central.AuthRefreshRequest
			decodeRequest(t, request, &input)
			if input.RefreshToken != "refresh-1" {
				t.Errorf("refresh token = %q", input.RefreshToken)
			}
			refreshes.Add(1)
			writeSession(t, writer, "access-2", "refresh-2", now)
		case "/v1/devices/install":
			if request.Header.Get("Authorization") != "Bearer access-2" {
				t.Errorf("authorization = %q", request.Header.Get("Authorization"))
			}
			var device central.ClientDevice
			decodeRequest(t, request, &device)
			_ = json.NewEncoder(writer).Encode(device)
		default:
			http.NotFound(writer, request)
		}
	}))
	t.Cleanup(server.Close)
	store, err := Open(context.Background(), Config{
		BaseURL: server.URL, UserID: "user", AccessToken: "credential", HTTPClient: server.Client(),
	})
	if err != nil {
		t.Fatal(err)
	}
	store.clock = func() time.Time { return now }
	store.authMu.Lock()
	store.expiresAt = now
	store.authMu.Unlock()

	const workers = 8
	start := make(chan struct{})
	errors := make(chan error, workers)
	var group sync.WaitGroup
	for range workers {
		group.Add(1)
		go func() {
			defer group.Done()
			<-start
			_, registerErr := store.RegisterDevice(context.Background(), central.ClientDevice{
				InstallationID: "install", UserID: "user", DeviceID: "device",
			})
			errors <- registerErr
		}()
	}
	close(start)
	group.Wait()
	close(errors)
	for err := range errors {
		if err != nil {
			t.Fatal(err)
		}
	}
	if refreshes.Load() != 1 {
		t.Fatalf("refresh calls = %d", refreshes.Load())
	}
}

func TestStoreRefreshesAndRetriesAfterUnauthorizedResponse(t *testing.T) {
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
		case "/v1/devices/install":
			if request.Header.Get("Authorization") == "Bearer access-1" {
				writer.WriteHeader(http.StatusUnauthorized)
				_ = json.NewEncoder(writer).Encode(map[string]any{
					"error": map[string]any{"code": "unauthorized", "message": "expired"},
				})
				return
			}
			var device central.ClientDevice
			decodeRequest(t, request, &device)
			_ = json.NewEncoder(writer).Encode(device)
		default:
			http.NotFound(writer, request)
		}
	}))
	t.Cleanup(server.Close)
	store, err := Open(context.Background(), Config{
		BaseURL: server.URL, UserID: "user", AccessToken: "credential", HTTPClient: server.Client(),
	})
	if err != nil {
		t.Fatal(err)
	}
	store.clock = func() time.Time { return now }
	if _, err := store.RegisterDevice(context.Background(), central.ClientDevice{InstallationID: "install"}); err != nil {
		t.Fatal(err)
	}
	if refreshes.Load() != 1 {
		t.Fatalf("refresh calls = %d", refreshes.Load())
	}
}

func writeSession(t *testing.T, writer http.ResponseWriter, access string, refresh string, now time.Time) {
	t.Helper()
	writer.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(writer).Encode(central.AuthExchangeResponse{ // #nosec G117 -- test-only session fixture.
		AccessToken: access, ExpiresAt: now.Add(time.Hour),
		RefreshToken: refresh, RefreshExpiresAt: now.Add(24 * time.Hour),
		User: central.ClientUser{ID: "user"},
	}); err != nil {
		t.Errorf("encode response: %v", err)
	}
}

func decodeRequest(t *testing.T, request *http.Request, value any) {
	t.Helper()
	if err := json.NewDecoder(request.Body).Decode(value); err != nil {
		t.Errorf("decode request: %v", err)
	}
}
