package centralhttp

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	catalogpb "github.com/cineko-org/contracts/gen/go/cineko/catalog"
	seatmappb "github.com/cineko-org/contracts/gen/go/cineko/seatmap"
	servicepb "github.com/cineko-org/contracts/gen/go/cineko/service"
)

func TestGetCatalogUsesServiceContract(t *testing.T) {
	t.Parallel()
	now := time.Now().UTC()
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set(releaseGenerationHeader, "17")
		switch request.URL.Path {
		case "/v1/auth/exchange":
			writeSession(t, writer, "access", "refresh", now)
		case "/v1/catalog":
			input := &servicepb.GetCatalogRequest{}
			decodeRequest(t, request, input)
			writeProto(t, writer, servicepb.GetCatalogResponse_builder{Catalog: &catalogpb.CatalogIndex{}}.Build())
		default:
			http.NotFound(writer, request)
		}
	}))
	t.Cleanup(server.Close)
	store := openCatalogStore(t, server, now)
	if _, err := store.GetCatalog(t.Context()); err != nil {
		t.Fatal(err)
	}
}

func TestGetSeatMapUsesContractedResolutionEndpoint(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, time.August, 14, 8, 0, 0, 0, time.UTC)
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set(releaseGenerationHeader, "17")
		switch request.URL.Path {
		case "/v1/auth/exchange":
			writeSession(t, writer, "access", "refresh", time.Now().UTC())
		case "/v1/catalog/auditoriums/auditorium-1/seat-map:resolve":
			if request.Method != http.MethodPost {
				t.Errorf("method = %s", request.Method)
			}
			input := &servicepb.ResolveSeatMapRequest{}
			decodeRequest(t, request, input)
			if input.GetAuditoriumId() != "auditorium-1" {
				t.Errorf("auditorium ID = %q", input.GetAuditoriumId())
			}
			writeProto(t, writer, servicepb.ResolveSeatMapResponse_builder{Resolution: seatmappb.Resolution_builder{
				Ready: seatmappb.Ready_builder{Snapshot: seatMapSnapshot(now)}.Build(),
			}.Build()}.Build())
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
	if seatMap.GetAuditoriumId() != "auditorium-1" || seatMap.GetLayoutHash() != "layout-hash" {
		t.Fatalf("seat map = %s", seatMap)
	}
}

func TestResolveSeatMapUsesCentralResolution(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, time.August, 14, 8, 0, 0, 0, time.UTC)
	requestCount := 0
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set(releaseGenerationHeader, "17")
		switch request.URL.Path {
		case "/v1/auth/exchange":
			writeSession(t, writer, "access", "refresh", time.Now().UTC())
		case "/v1/catalog/auditoriums/auditorium-1/seat-map:resolve":
			requestCount++
			if request.Method != http.MethodPost {
				t.Errorf("method = %s", request.Method)
			}
			input := &servicepb.ResolveSeatMapRequest{}
			decodeRequest(t, request, input)
			if input.GetAuditoriumId() != "auditorium-1" {
				t.Errorf("auditorium ID = %q", input.GetAuditoriumId())
			}
			writeProto(t, writer, servicepb.ResolveSeatMapResponse_builder{Resolution: seatmappb.Resolution_builder{Ready: seatmappb.Ready_builder{Snapshot: seatMapSnapshot(now)}.Build()}.Build()}.Build())
		default:
			http.NotFound(writer, request)
		}
	}))
	t.Cleanup(server.Close)
	store := openCatalogStore(t, server, now)
	resolution, err := store.ResolveSeatMap(t.Context(), "auditorium-1")
	if err != nil || resolution.GetReady() == nil || resolution.GetReady().GetSnapshot().GetLayoutHash() != "layout-hash" || requestCount != 1 {
		t.Fatalf("ResolveSeatMap() = %s, requests %d, error %v", resolution, requestCount, err)
	}
}

func TestResolveSeatMapReturnsWaitingWithoutProviderDetails(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, time.August, 14, 8, 0, 0, 0, time.UTC)
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set(releaseGenerationHeader, "17")
		switch request.URL.Path {
		case "/v1/auth/exchange":
			writeSession(t, writer, "access", "refresh", time.Now().UTC())
		case "/v1/catalog/auditoriums/auditorium-1/seat-map:resolve":
			writeProto(t, writer, servicepb.ResolveSeatMapResponse_builder{Resolution: seatmappb.Resolution_builder{CaptureQueued: seatmappb.CaptureQueued_builder{}.Build()}.Build()}.Build())
		default:
			http.NotFound(writer, request)
		}
	}))
	t.Cleanup(server.Close)
	store := openCatalogStore(t, server, now)
	resolution, err := store.ResolveSeatMap(t.Context(), "auditorium-1")
	if err != nil || resolution.GetCaptureQueued() == nil {
		t.Fatalf("ResolveSeatMap() = %s, error %v", resolution, err)
	}
}

func TestResolveSeatMapRejectsInvalidCentralResolution(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, time.August, 14, 8, 0, 0, 0, time.UTC)
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set(releaseGenerationHeader, "17")
		switch request.URL.Path {
		case "/v1/auth/exchange":
			writeSession(t, writer, "access", "refresh", time.Now().UTC())
		case "/v1/catalog/auditoriums/auditorium-1/seat-map:resolve":
			writeProto(t, writer, servicepb.ResolveSeatMapResponse_builder{Resolution: &seatmappb.Resolution{}}.Build())
		default:
			http.NotFound(writer, request)
		}
	}))
	t.Cleanup(server.Close)
	store := openCatalogStore(t, server, now)
	if _, err := store.ResolveSeatMap(t.Context(), "auditorium-1"); err == nil {
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

func seatMapSnapshot(observedAt time.Time) *seatmappb.Snapshot {
	auditoriumID, snapshotID, layoutHash := "auditorium-1", "auditorium-1/layout-hash", "layout-hash"
	return seatmappb.Snapshot_builder{
		Id:           &snapshotID,
		AuditoriumId: &auditoriumID,
		LayoutHash:   &layoutHash,
		Layout:       seatmappb.Layout_builder{}.Build(),
		ObservedAt:   timestamp(observedAt),
	}.Build()
}
