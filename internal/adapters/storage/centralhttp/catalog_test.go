package centralhttp

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	central "github.com/cineko-org/contracts/v3"
)

func TestGetSeatMapUsesAuditoriumLatestVersionEndpoint(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, time.August, 14, 8, 0, 0, 0, time.UTC)
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set(central.ReleaseGenerationHeader, "17")
		switch request.URL.Path {
		case "/v1/auth/exchange":
			writeSession(t, writer, "access", "refresh", time.Now().UTC())
		case "/v1/catalog/auditoriums/auditorium-1/seat-map":
			if request.Method != http.MethodGet {
				t.Errorf("method = %s", request.Method)
			}
			_ = json.NewEncoder(writer).Encode(central.SeatMapVersion{
				ID:           central.SeatMapVersionID("auditorium-1", "layout-hash"),
				AuditoriumID: "auditorium-1", LayoutHash: "layout-hash", ObservedAt: now,
				Layout: json.RawMessage(`{"seats":[],"zones":[],"blocks":[]}`),
			})
		default:
			http.NotFound(writer, request)
		}
	}))
	t.Cleanup(server.Close)
	store := openCatalogStore(t, server, now)
	seatMap, err := store.GetSeatMap(t.Context(), "auditorium-1")
	if err != nil {
		t.Fatal(err)
	}
	if seatMap.AuditoriumID != "auditorium-1" || seatMap.Version != "layout-hash" {
		t.Fatalf("seat map = %+v", seatMap)
	}
}

func TestResolveSeatMapUsesCentralResolution(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, time.August, 14, 8, 0, 0, 0, time.UTC)
	requestCount := 0
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set(central.ReleaseGenerationHeader, "17")
		switch request.URL.Path {
		case "/v1/auth/exchange":
			writeSession(t, writer, "access", "refresh", time.Now().UTC())
		case "/v1/catalog/auditoriums/auditorium-1/seat-map:resolve":
			requestCount++
			if request.Method != http.MethodPost {
				t.Errorf("method = %s", request.Method)
			}
			_ = json.NewEncoder(writer).Encode(central.SeatMapResolution{
				Status: central.SeatMapResolutionReady,
				SeatMap: &central.SeatMapVersion{
					ID:           central.SeatMapVersionID("auditorium-1", "layout-hash"),
					AuditoriumID: "auditorium-1", LayoutHash: "layout-hash", ObservedAt: now,
					Layout: json.RawMessage(`{"seats":[],"zones":[],"blocks":[]}`),
				},
			})
		default:
			http.NotFound(writer, request)
		}
	}))
	t.Cleanup(server.Close)
	store := openCatalogStore(t, server, now)
	seatMap, ready, err := store.ResolveSeatMap(t.Context(), "auditorium-1")
	if err != nil || !ready || seatMap.Version != "layout-hash" || requestCount != 1 {
		t.Fatalf("ResolveSeatMap() = %+v, ready %v, requests %d, error %v", seatMap, ready, requestCount, err)
	}
}

func TestResolveSeatMapReturnsWaitingWithoutProviderDetails(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, time.August, 14, 8, 0, 0, 0, time.UTC)
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set(central.ReleaseGenerationHeader, "17")
		switch request.URL.Path {
		case "/v1/auth/exchange":
			writeSession(t, writer, "access", "refresh", time.Now().UTC())
		case "/v1/catalog/auditoriums/auditorium-1/seat-map:resolve":
			_ = json.NewEncoder(writer).Encode(central.SeatMapResolution{Status: central.SeatMapResolutionWaiting})
		default:
			http.NotFound(writer, request)
		}
	}))
	t.Cleanup(server.Close)
	store := openCatalogStore(t, server, now)
	seatMap, ready, err := store.ResolveSeatMap(t.Context(), "auditorium-1")
	if err != nil || ready || seatMap.AuditoriumID != "" {
		t.Fatalf("ResolveSeatMap() = %+v, ready %v, error %v", seatMap, ready, err)
	}
}

func TestResolveSeatMapRejectsInvalidCentralResolution(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, time.August, 14, 8, 0, 0, 0, time.UTC)
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set(central.ReleaseGenerationHeader, "17")
		switch request.URL.Path {
		case "/v1/auth/exchange":
			writeSession(t, writer, "access", "refresh", time.Now().UTC())
		case "/v1/catalog/auditoriums/auditorium-1/seat-map:resolve":
			_ = json.NewEncoder(writer).Encode(central.SeatMapResolution{Status: central.SeatMapResolutionReady})
		default:
			http.NotFound(writer, request)
		}
	}))
	t.Cleanup(server.Close)
	store := openCatalogStore(t, server, now)
	if _, _, err := store.ResolveSeatMap(t.Context(), "auditorium-1"); err == nil {
		t.Fatal("ResolveSeatMap() succeeded with an invalid Central response")
	}
}

func openCatalogStore(t *testing.T, server *httptest.Server, now time.Time) *Store {
	t.Helper()
	store, err := Open(context.Background(), Config{
		BaseURL: server.URL, UserID: "user", AccessToken: "credential",
		InstallationID: "installation-1", HTTPClient: server.Client(),
	})
	if err != nil {
		t.Fatal(err)
	}
	store.clock = func() time.Time { return now }
	return store
}
