package centralhttp

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	catalogpb "github.com/cineko-org/contracts/v3/gen/go/cineko/catalog"
	collectionpb "github.com/cineko-org/contracts/v3/gen/go/cineko/collection"
	commonpb "github.com/cineko-org/contracts/v3/gen/go/cineko/common"
	seatmappb "github.com/cineko-org/contracts/v3/gen/go/cineko/seatmap"
	servicepb "github.com/cineko-org/contracts/v3/gen/go/cineko/service"
)

const testLayoutHash = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"

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
			writeProto(t, writer, servicepb.ResolveSeatMapResponse_builder{Resolution: resolvedSeatMap(now)}.Build())
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
	if seatMap.GetAuditoriumId() != "auditorium-1" || seatMap.GetLayoutHash() != testLayoutHash {
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
			writeProto(t, writer, servicepb.ResolveSeatMapResponse_builder{Resolution: resolvedSeatMap(now)}.Build())
		default:
			http.NotFound(writer, request)
		}
	}))
	t.Cleanup(server.Close)
	store := openCatalogStore(t, server, now)
	resolution, err := store.ResolveSeatMap(t.Context(), "auditorium-1")
	if err != nil || resolution.GetSnapshot().GetLayoutHash() != testLayoutHash || resolution.GetState().GetIdle() == nil || requestCount != 1 {
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
			writeProto(t, writer, servicepb.ResolveSeatMapResponse_builder{Resolution: queuedSeatMap(now)}.Build())
		default:
			http.NotFound(writer, request)
		}
	}))
	t.Cleanup(server.Close)
	store := openCatalogStore(t, server, now)
	resolution, err := store.ResolveSeatMap(t.Context(), "auditorium-1")
	if err != nil || resolution.GetState().GetQueued() == nil {
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

func TestSubmitLiveSeatObservationUsesGeneratedAtomicContract(t *testing.T) {
	t.Parallel()
	now := time.Now().UTC()
	commandID := "client-live-seat-command"
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set(releaseGenerationHeader, "17")
		switch request.URL.Path {
		case "/v1/auth/exchange":
			writeSession(t, writer, "access", "refresh", time.Now().UTC())
		case "/v1/catalog/live-seat-observations":
			if request.Method != http.MethodPost || request.Header.Get("Authorization") != "Bearer access" {
				t.Errorf("live observation request = %s, authorization %q", request.Method, request.Header.Get("Authorization"))
			}
			if request.Header.Get("Idempotency-Key") != commandID {
				t.Errorf("idempotency key = %q", request.Header.Get("Idempotency-Key"))
			}
			input := &servicepb.SubmitLiveSeatObservationRequest{}
			decodeRequest(t, request, input)
			if input.GetMutation().GetExpectedRevision() != 0 || len(input.GetObservation().GetAvailability().GetAvailableSeats()) != 0 {
				t.Errorf("live observation = %s", input)
			}
			writeProto(t, writer, servicepb.SubmitLiveSeatObservationResponse_builder{
				Snapshot: input.GetObservation().GetLayout(),
			}.Build())
		default:
			http.NotFound(writer, request)
		}
	}))
	t.Cleanup(server.Close)
	store := openCatalogStore(t, server, now)
	observation := liveSeatObservation(now)
	expectedRevision := int64(0)
	request := servicepb.SubmitLiveSeatObservationRequest_builder{
		Mutation: commonpb.MutationIdentity_builder{
			CommandId: &commandID, ExpectedRevision: &expectedRevision,
		}.Build(),
		Observation: observation,
	}.Build()
	response, err := store.SubmitLiveSeatObservation(t.Context(), request)
	if err != nil || response.GetSnapshot().GetLayoutHash() != testLayoutHash {
		t.Fatalf("SubmitLiveSeatObservation() = %s, %v", response, err)
	}
}

func TestWatchSeatMapConsumesTypedCentralStream(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, time.August, 14, 8, 0, 0, 0, time.UTC)
	stop := errors.New("stop after first seat-map event")
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set(releaseGenerationHeader, "17")
		switch request.URL.Path {
		case "/v1/auth/exchange":
			writeSession(t, writer, "access", "refresh", time.Now().UTC())
		case "/v1/catalog/auditoriums/auditorium-1/seat-map:watch":
			if request.Method != http.MethodGet || request.Header.Get("Accept") != "text/event-stream" || request.Header.Get("Authorization") != "Bearer access" {
				t.Errorf("seat-map watch request = %s, accept %q, authorization %q", request.Method, request.Header.Get("Accept"), request.Header.Get("Authorization"))
			}
			writer.Header().Set("Content-Type", "text/event-stream")
			_, _ = fmt.Fprintf(writer, "event: %s\ndata: %s\n\n", seatMapEventType, protoJSON(t,
				servicepb.WatchSeatMapResponse_builder{Resolution: resolvedSeatMap(now)}.Build(),
			))
		default:
			http.NotFound(writer, request)
		}
	}))
	t.Cleanup(server.Close)
	store := openCatalogStore(t, server, now)
	var received *seatmappb.Resolution
	err := store.WatchSeatMap(t.Context(), "auditorium-1", func(resolution *seatmappb.Resolution) error {
		received = resolution
		return stop
	})
	if !errors.Is(err, stop) || received.GetSnapshot().GetLayoutHash() != testLayoutHash {
		t.Fatalf("WatchSeatMap() = %s, error %v", received, err)
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
	auditoriumID, snapshotID, layoutHash := "auditorium-1", "auditorium-1/"+testLayoutHash, testLayoutHash
	seatID, seatLabel, capacity := "seat-1", "A1", int32(1)
	return seatmappb.Snapshot_builder{
		Id:           &snapshotID,
		AuditoriumId: &auditoriumID,
		LayoutHash:   &layoutHash,
		Capacity:     &capacity,
		Layout: seatmappb.Layout_builder{Seats: []*seatmappb.Seat{
			seatmappb.Seat_builder{Id: &seatID, AuditoriumId: &auditoriumID, Label: &seatLabel}.Build(),
		}}.Build(),
		ObservedAt: timestamp(observedAt),
	}.Build()
}

func liveSeatObservation(observedAt time.Time) *seatmappb.LiveSeatObservation {
	snapshot := seatMapSnapshot(observedAt)
	showtimeID := "showtime-1"
	auditoriumID, layoutHash := snapshot.GetAuditoriumId(), snapshot.GetLayoutHash()
	return seatmappb.LiveSeatObservation_builder{
		Layout: snapshot,
		Availability: seatmappb.AvailabilitySnapshot_builder{
			ShowtimeId: &showtimeID, AuditoriumId: &auditoriumID,
			LayoutHash: &layoutHash, ObservedAt: timestamp(observedAt),
		}.Build(),
	}.Build()
}

func resolvedSeatMap(observedAt time.Time) *seatmappb.Resolution {
	return seatmappb.Resolution_builder{
		Snapshot: seatMapSnapshot(observedAt),
		State: collectionpb.State_builder{
			Idle: collectionpb.Idle_builder{}.Build(),
		}.Build(),
	}.Build()
}

func queuedSeatMap(queuedAt time.Time) *seatmappb.Resolution {
	return seatmappb.Resolution_builder{State: collectionpb.State_builder{
		Queued: collectionpb.Queued_builder{
			QueuedAt: timestamp(queuedAt),
			Trigger: collectionpb.Trigger_builder{
				ClientRequest: collectionpb.ClientRequest_builder{}.Build(),
			}.Build(),
		}.Build(),
	}.Build()}.Build()
}
