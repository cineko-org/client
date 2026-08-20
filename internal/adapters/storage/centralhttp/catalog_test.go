package centralhttp

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/cineko-org/client/internal/domain"
	central "github.com/cineko-org/contracts/v3"
)

func TestCatalogSnapshotUsesGlobalEndpointAndStableIdempotencyKey(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, time.August, 14, 8, 0, 0, 0, time.UTC)
	var requests []central.CatalogSnapshot
	var keys []string
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set(central.ReleaseGenerationHeader, "17")
		switch request.URL.Path {
		case "/v1/auth/exchange":
			writeSession(t, writer, "access", "refresh", time.Now().UTC())
		case "/v1/catalog/snapshots":
			if request.Method != http.MethodPost {
				t.Errorf("method = %s", request.Method)
			}
			if request.Header.Get("Authorization") != "Bearer access" {
				t.Errorf("authorization = %q", request.Header.Get("Authorization"))
			}
			if request.Header.Get(installationIDHeader) != "installation-1" {
				t.Errorf("installation header = %q", request.Header.Get(installationIDHeader))
			}
			var snapshot central.CatalogSnapshot
			decodeRequest(t, request, &snapshot)
			requests = append(requests, snapshot)
			keys = append(keys, request.Header.Get("Idempotency-Key"))
			writer.WriteHeader(http.StatusNoContent)
		default:
			http.NotFound(writer, request)
		}
	}))
	t.Cleanup(server.Close)
	store := openCatalogStore(t, server, now)
	snapshot := central.CatalogSnapshot{
		Provider: central.Provider{ID: central.ProviderCGV, Name: "CGV"},
		Theaters: []central.Theater{{
			ID:         central.CatalogID(central.ProviderCGV, "theater", "서울/용산아이파크몰"),
			ProviderID: central.ProviderCGV, SourceKey: "서울/용산아이파크몰",
			Region: "서울", Name: "용산아이파크몰",
		}},
		ObservedAt: now,
	}
	for range 2 {
		if err := store.PublishCatalogSnapshot(t.Context(), snapshot); err != nil {
			t.Fatal(err)
		}
	}
	if len(requests) != 2 || requests[0].Theaters[0].ID != snapshot.Theaters[0].ID {
		t.Fatalf("requests = %+v", requests)
	}
	if keys[0] == "" || keys[0] != keys[1] || !strings.HasPrefix(keys[0], "catalog-snapshot-") {
		t.Fatalf("idempotency keys = %v", keys)
	}
}

func TestPutAuditoriumPublishesItsCanonicalTheater(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, time.August, 14, 8, 0, 0, 0, time.UTC)
	theater := central.Theater{
		ID:         central.CatalogID(central.ProviderCGV, "theater", "서울/용산아이파크몰"),
		ProviderID: central.ProviderCGV, SourceKey: "서울/용산아이파크몰",
		Region: "서울", Name: "용산아이파크몰",
	}
	var published central.CatalogSnapshot
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set(central.ReleaseGenerationHeader, "17")
		switch request.URL.Path {
		case "/v1/auth/exchange":
			writeSession(t, writer, "access", "refresh", time.Now().UTC())
		case "/v1/catalog":
			_ = json.NewEncoder(writer).Encode(central.CatalogIndex{Theaters: []central.Theater{theater}})
		case "/v1/catalog/snapshots":
			decodeRequest(t, request, &published)
			writer.WriteHeader(http.StatusNoContent)
		default:
			http.NotFound(writer, request)
		}
	}))
	t.Cleanup(server.Close)
	store := openCatalogStore(t, server, now)
	auditorium := domain.Auditorium{
		ID:        central.CatalogID(central.ProviderCGV, "auditorium", theater.SourceKey+"/IMAX관"),
		TheaterID: theater.ID, SourceKey: theater.SourceKey + "/IMAX관", Name: "IMAX관",
		Capacity: 624, ObservedAt: now,
	}
	if err := store.PutAuditorium(t.Context(), auditorium); err != nil {
		t.Fatal(err)
	}
	if len(published.Theaters) != 1 || published.Theaters[0] != theater ||
		len(published.Auditoriums) != 1 || published.Auditoriums[0].TheaterID != theater.ID {
		t.Fatalf("published snapshot = %+v", published)
	}
}

func TestSeatMapVersionUploadsOnlyStaticLayoutWithContentIdentity(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, time.August, 14, 8, 0, 0, 0, time.UTC)
	var uploaded []central.SeatMapVersion
	var paths, keys []string
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set(central.ReleaseGenerationHeader, "17")
		switch request.URL.Path {
		case "/v1/auth/exchange":
			writeSession(t, writer, "access", "refresh", time.Now().UTC())
		default:
			if !strings.HasPrefix(request.URL.Path, "/v1/catalog/seat-map-versions/") {
				http.NotFound(writer, request)
				return
			}
			if request.Method != http.MethodPut {
				t.Errorf("method = %s", request.Method)
			}
			if request.Header.Get(installationIDHeader) != "installation-1" {
				t.Errorf("installation header = %q", request.Header.Get(installationIDHeader))
			}
			var version central.SeatMapVersion
			decodeRequest(t, request, &version)
			uploaded = append(uploaded, version)
			paths = append(paths, request.URL.Path)
			keys = append(keys, request.Header.Get("Idempotency-Key"))
			writer.WriteHeader(http.StatusNoContent)
		}
	}))
	t.Cleanup(server.Close)
	store := openCatalogStore(t, server, now)
	seatMap := domain.SeatMap{
		AuditoriumID: "auditorium-1", Version: "static-layout-hash", ObservedAt: now,
		Seats: []domain.Seat{{
			ID: "seat-1", AuditoriumID: "auditorium-1", Label: "H12", Row: "H", Number: 12,
			X: 0.5, Y: 0.5, Type: domain.SeatTypeStandard,
		}},
		Evidence: domain.LayoutEvidence{ScreenshotSHA256: strings.Repeat("a", 64)},
	}
	for range 2 {
		if err := store.PutSeatMap(t.Context(), seatMap); err != nil {
			t.Fatal(err)
		}
	}
	if len(uploaded) != 2 || paths[0] != paths[1] || keys[0] != keys[1] || keys[0] == "" {
		t.Fatalf("paths = %v, keys = %v", paths, keys)
	}
	version := uploaded[0]
	digest := sha256.Sum256(version.Layout)
	wantHash := hex.EncodeToString(digest[:])
	wantID := central.SeatMapVersionID("auditorium-1", wantHash)
	if version.ID != wantID || version.LayoutHash != wantHash {
		t.Fatalf("version = %+v", version)
	}
	var layout map[string]json.RawMessage
	if err := json.Unmarshal(version.Layout, &layout); err != nil {
		t.Fatal(err)
	}
	if len(layout) != 3 || layout["seats"] == nil || layout["zones"] == nil || layout["blocks"] == nil {
		t.Fatalf("layout fields = %v", layout)
	}
	for _, forbidden := range []string{"availability", "available", "evidence", "observedAt"} {
		if _, exists := layout[forbidden]; exists {
			t.Fatalf("live or evidence field %q leaked into layout", forbidden)
		}
	}
}

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
