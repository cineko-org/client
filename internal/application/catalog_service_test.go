package application

import (
	"context"
	"testing"
	"time"

	"github.com/cineko-org/client/internal/domain"
	contracts "github.com/cineko-org/contracts/v3"
)

func TestCatalogEnsureReturnsCompleteCachedLayoutsWithoutOpeningGateway(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, time.August, 9, 9, 0, 0, 0, time.UTC)
	theater, auditorium, seatMap := catalogFixture(now)
	repository := &catalogRepository{
		theaters: map[string]domain.Theater{theater.ID: theater},
		auditoriums: map[string][]domain.Auditorium{
			theater.ID: {auditorium},
		},
		seatMaps: map[string]domain.SeatMap{auditorium.ID: seatMap},
	}
	gateway := &catalogGateway{}
	service := NewCatalogService(repository, repository, repository, gateway, fixedClock{now})

	result, err := service.Ensure(context.Background(), TheaterRef{Region: theater.Region, Name: theater.Name}, nil)
	if err != nil {
		t.Fatalf("Ensure() error = %v", err)
	}
	if !result.Cached || len(result.Auditoriums) != 1 || len(result.Analyses) != 1 {
		t.Fatalf("cached result = %+v", result)
	}
	if gateway.resolveCalls != 0 || gateway.discoverCalls != 0 || gateway.captureCalls != 0 {
		t.Fatalf("gateway calls = resolve %d, discover %d, capture %d", gateway.resolveCalls, gateway.discoverCalls, gateway.captureCalls)
	}
}

func TestCatalogEnsureCapturesAndStoresOnlyMissingLayout(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, time.August, 9, 9, 0, 0, 0, time.UTC)
	theater, auditorium, seatMap := catalogFixture(now)
	repository := &catalogRepository{
		theaters: map[string]domain.Theater{theater.ID: theater},
		auditoriums: map[string][]domain.Auditorium{
			theater.ID: {auditorium},
		},
		seatMaps: make(map[string]domain.SeatMap),
	}
	gateway := &catalogGateway{
		theater: theater,
		observations: []AuditoriumObservation{{
			Auditorium: auditorium,
			RepresentativeShowing: &domain.Showtime{
				ID: "showtime-1", Date: "2026-08-10", StartsAt: "20:30",
			},
		}},
		seatMap: seatMap,
	}
	service := NewCatalogService(repository, repository, repository, gateway, fixedClock{now})

	result, err := service.Ensure(
		context.Background(), TheaterRef{Region: theater.Region, Name: theater.Name}, []string{"2026-08-10"},
	)
	if err != nil {
		t.Fatalf("Ensure() error = %v", err)
	}
	if result.Cached || gateway.captureCalls != 1 {
		t.Fatalf("result/gateway = %+v / %+v", result, gateway)
	}
	stored, exists := repository.seatMaps[auditorium.ID]
	if !exists || stored.Version != seatMap.Version {
		t.Fatalf("captured layout was not stored: %+v", stored)
	}
}

func TestCatalogDiscoveryStoresSelectableAuditoriumsWithoutCapturingLayouts(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, time.August, 9, 9, 0, 0, 0, time.UTC)
	theater, auditorium, _ := catalogFixture(now)
	auditorium.SeatMapVersion = ""
	repository := &catalogRepository{
		theaters: make(map[string]domain.Theater), auditoriums: make(map[string][]domain.Auditorium),
		seatMaps: make(map[string]domain.SeatMap),
	}
	gateway := &catalogGateway{
		theater: theater,
		observations: []AuditoriumObservation{{
			Auditorium:            auditorium,
			RepresentativeShowing: &domain.Showtime{ID: "showtime-1", Date: "2026-08-10", StartsAt: "20:30"},
		}},
	}
	service := NewCatalogService(repository, repository, repository, gateway, fixedClock{now})

	result, err := service.DiscoverAuditoriums(
		context.Background(), TheaterRef{Region: theater.Region, Name: theater.Name}, []string{"2026-08-10"},
	)
	if err != nil {
		t.Fatalf("DiscoverAuditoriums() error = %v", err)
	}
	if len(result.Auditoriums) != 1 || result.Auditoriums[0].ID != auditorium.ID {
		t.Fatalf("auditoriums = %+v", result.Auditoriums)
	}
	if gateway.captureCalls != 0 {
		t.Fatalf("CaptureSeatMap called %d times during metadata discovery", gateway.captureCalls)
	}
}

func TestCatalogCaptureTargetsOnlySelectedAuditorium(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, time.August, 9, 9, 0, 0, 0, time.UTC)
	theater, auditorium, seatMap := catalogFixture(now)
	auditorium.SeatMapVersion = ""
	repository := &catalogRepository{
		theaters: make(map[string]domain.Theater), auditoriums: make(map[string][]domain.Auditorium),
		seatMaps: make(map[string]domain.SeatMap),
	}
	gateway := &catalogGateway{
		theater: theater,
		observations: []AuditoriumObservation{{
			Auditorium:            auditorium,
			RepresentativeShowing: &domain.Showtime{ID: "showtime-1", Date: "2026-08-10", StartsAt: "20:30"},
		}},
		seatMap: seatMap,
	}
	service := NewCatalogService(repository, repository, repository, gateway, fixedClock{now})

	got, err := service.CaptureAuditoriumSeatMap(
		context.Background(), TheaterRef{Region: theater.Region, Name: theater.Name},
		auditorium.ID, []string{"2026-08-10"},
	)
	if err != nil {
		t.Fatalf("CaptureAuditoriumSeatMap() error = %v", err)
	}
	if got.AuditoriumID != auditorium.ID || gateway.captureCalls != 1 {
		t.Fatalf("seat map/capture calls = %+v / %d", got, gateway.captureCalls)
	}
}

func TestCatalogRecaptureReplacesStoredLayoutVersion(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, time.August, 9, 9, 0, 0, 0, time.UTC)
	theater, auditorium, previous := catalogFixture(now)
	updated := previous
	updated.Version = "layout-v2"
	updated.Seats = append(updated.Seats, domain.Seat{
		Label: "A2", Row: "A", Number: 2, X: .6, Y: .5, Type: domain.SeatTypeStandard,
	})
	updated.Evidence.DOMSeatCount = len(updated.Seats)
	updated.Evidence.SnapshotSeatCount = len(updated.Seats)
	repository := &catalogRepository{
		theaters: map[string]domain.Theater{theater.ID: theater},
		auditoriums: map[string][]domain.Auditorium{
			theater.ID: {auditorium},
		},
		seatMaps: map[string]domain.SeatMap{auditorium.ID: previous},
	}
	gateway := &catalogGateway{
		theater: theater,
		observations: []AuditoriumObservation{{
			Auditorium:            auditorium,
			RepresentativeShowing: &domain.Showtime{ID: "showtime-2", Date: "2026-08-10", StartsAt: "22:00"},
		}},
		seatMap: updated,
	}
	service := NewCatalogService(repository, repository, repository, gateway, fixedClock{now})

	got, err := service.CaptureAuditoriumSeatMap(
		context.Background(), TheaterRef{Region: theater.Region, Name: theater.Name},
		auditorium.ID, []string{"2026-08-10"},
	)
	if err != nil {
		t.Fatalf("CaptureAuditoriumSeatMap() error = %v", err)
	}
	stored := repository.seatMaps[auditorium.ID]
	storedAuditorium, getErr := repository.GetAuditorium(context.Background(), auditorium.ID)
	if getErr != nil {
		t.Fatalf("GetAuditorium() error = %v", getErr)
	}
	if got.Version != updated.Version || stored.Version != updated.Version {
		t.Fatalf("layout versions = returned %q, stored %q", got.Version, stored.Version)
	}
	if storedAuditorium.SeatMapVersion != updated.Version || storedAuditorium.Capacity != len(updated.Seats) {
		t.Fatalf("auditorium layout metadata = %+v", storedAuditorium)
	}
}

type catalogRepository struct {
	theaters    map[string]domain.Theater
	auditoriums map[string][]domain.Auditorium
	seatMaps    map[string]domain.SeatMap
}

func (repository *catalogRepository) PutTheater(_ context.Context, value domain.Theater) error {
	if repository.theaters == nil {
		repository.theaters = make(map[string]domain.Theater)
	}
	repository.theaters[value.ID] = value
	return nil
}

func (repository *catalogRepository) GetTheater(_ context.Context, id string) (domain.Theater, error) {
	value, exists := repository.theaters[id]
	if !exists {
		return domain.Theater{}, ErrNotFound
	}
	return value, nil
}

func (repository *catalogRepository) ListTheaters(context.Context) ([]domain.Theater, error) {
	values := make([]domain.Theater, 0, len(repository.theaters))
	for _, value := range repository.theaters {
		values = append(values, value)
	}
	return values, nil
}

func (repository *catalogRepository) PutAuditorium(_ context.Context, value domain.Auditorium) error {
	values := repository.auditoriums[value.TheaterID]
	for index := range values {
		if values[index].ID == value.ID {
			values[index] = value
			repository.auditoriums[value.TheaterID] = values
			return nil
		}
	}
	repository.auditoriums[value.TheaterID] = append(values, value)
	return nil
}

func (repository *catalogRepository) GetAuditorium(_ context.Context, id string) (domain.Auditorium, error) {
	for _, values := range repository.auditoriums {
		for _, value := range values {
			if value.ID == id {
				return value, nil
			}
		}
	}
	return domain.Auditorium{}, ErrNotFound
}

func (repository *catalogRepository) ListAuditoriumsByTheater(_ context.Context, theaterID string) ([]domain.Auditorium, error) {
	return append([]domain.Auditorium(nil), repository.auditoriums[theaterID]...), nil
}

func (repository *catalogRepository) PutSeatMap(_ context.Context, value domain.SeatMap) error {
	repository.seatMaps[value.AuditoriumID] = value
	return nil
}

func (repository *catalogRepository) GetSeatMap(_ context.Context, auditoriumID string) (domain.SeatMap, error) {
	value, exists := repository.seatMaps[auditoriumID]
	if !exists {
		return domain.SeatMap{}, ErrNotFound
	}
	return value, nil
}

type catalogGateway struct {
	theater       domain.Theater
	observations  []AuditoriumObservation
	seatMap       domain.SeatMap
	resolveCalls  int
	discoverCalls int
	captureCalls  int
}

func (gateway *catalogGateway) ResolveTheater(_ context.Context, ref TheaterRef) (domain.Theater, error) {
	gateway.resolveCalls++
	if gateway.theater.ID == "" {
		return domain.Theater{}, ErrNotFound
	}
	return gateway.theater, nil
}

func (gateway *catalogGateway) DiscoverAuditoriums(
	context.Context, domain.Theater, []string,
) ([]AuditoriumObservation, error) {
	gateway.discoverCalls++
	return gateway.observations, nil
}

func (gateway *catalogGateway) CaptureSeatMap(
	context.Context, domain.Auditorium, domain.Showtime,
) (domain.SeatMap, error) {
	gateway.captureCalls++
	return gateway.seatMap, nil
}

func catalogFixture(now time.Time) (domain.Theater, domain.Auditorium, domain.SeatMap) {
	theater := domain.Theater{
		ProviderID: contracts.ProviderCGV, Region: "서울", Name: "용산아이파크몰",
		SourceKey: "서울/용산아이파크몰", ObservedAt: now,
	}
	theater.ID = contracts.CatalogID(contracts.ProviderCGV, "theater", theater.SourceKey)
	auditorium := domain.Auditorium{
		TheaterID: theater.ID, SourceKey: theater.SourceKey + "/IMAX관",
		Name: "IMAX관", Capacity: 1, SeatMapVersion: "layout-v1", ObservedAt: now,
	}
	auditorium.ID = contracts.CatalogID(contracts.ProviderCGV, "auditorium", auditorium.SourceKey)
	seatMap := domain.SeatMap{
		AuditoriumID: auditorium.ID, Version: "layout-v1", ObservedAt: now,
		Seats: []domain.Seat{{
			ID: StableID("seat", auditorium.ID, "A1"), AuditoriumID: auditorium.ID,
			Label: "A1", Row: "A", Number: 1, X: .5, Y: .5, Type: domain.SeatTypeStandard,
		}},
		Evidence: domain.LayoutEvidence{
			ScreenshotPath: "layout.png", ScreenshotSHA256: "image-hash", SnapshotSHA256: "snapshot-hash",
			SourceShowtimeID: "showtime-1", DOMSeatCount: 1, SnapshotSeatCount: 1,
			CaptureTrigger: "refresh", CapturedAt: now,
		},
	}
	return theater, auditorium, seatMap
}
