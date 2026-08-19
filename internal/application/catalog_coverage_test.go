package application

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/cineko-org/client/internal/domain"
	contracts "github.com/cineko-org/contracts/v3"
)

func TestCatalogServiceCoversSyncAndCacheFailures(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	service, repository, gateway, ref, theater, auditorium, _ := newCatalogCoverageHarness()

	result, err := service.Sync(ctx, ref, []string{"2026-08-10"})
	if err != nil || len(result.Analyses) != 1 || result.Cached {
		t.Fatalf("Sync() = %+v, %v", result, err)
	}
	delete(repository.theaters, theater.ID)
	if _, found, err := service.Cached(ctx, ref); err != nil || found {
		t.Fatalf("Cached(missing theater) = %t, %v", found, err)
	}
	repository.theaters[theater.ID] = theater

	for _, fail := range []string{"get-theater", "list-auditoriums", "get-seat-map"} {
		repository.fail = fail
		if _, _, err := service.Cached(ctx, ref); !errors.Is(err, errInjected) {
			t.Fatalf("Cached(%s) error = %v", fail, err)
		}
		repository.fail = ""
	}
	delete(repository.auditoriums, theater.ID)
	if _, found, err := service.Cached(ctx, ref); err != nil || found {
		t.Fatalf("Cached(empty) = %t, %v", found, err)
	}
	repository.auditoriums[theater.ID] = []domain.Auditorium{auditorium}
	delete(repository.seatMaps, auditorium.ID)
	if _, found, err := service.Cached(ctx, ref); err != nil || found {
		t.Fatalf("Cached(missing layout) = %t, %v", found, err)
	}

	gateway.fail = "resolve"
	if _, err := service.DiscoverAuditoriums(ctx, ref, nil); !errors.Is(err, errInjected) {
		t.Fatalf("DiscoverAuditoriums(resolve) error = %v", err)
	}
	if _, err := service.CaptureAuditoriumSeatMap(ctx, ref, auditorium.ID, nil); !errors.Is(err, errInjected) {
		t.Fatalf("CaptureAuditoriumSeatMap(resolve) error = %v", err)
	}
	gateway.fail = ""
	repository.fail = "get-auditorium"
	if _, err := service.DiscoverAuditoriums(ctx, ref, nil); !errors.Is(err, errInjected) {
		t.Fatalf("DiscoverAuditoriums(persist) error = %v", err)
	}
}

func TestCatalogServiceCoversObservationAndCaptureFailures(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	service, repository, gateway, ref, theater, auditorium, seatMap := newCatalogCoverageHarness()

	for _, fail := range []string{"put-theater", "discover"} {
		if fail == "put-theater" {
			repository.fail = fail
		} else {
			gateway.fail = fail
		}
		if _, _, err := service.observeAuditoriums(ctx, ref, nil); !errors.Is(err, errInjected) {
			t.Fatalf("observeAuditoriums(%s) error = %v", fail, err)
		}
		repository.fail, gateway.fail = "", ""
	}
	invalidGateway := *gateway
	invalidGateway.theater = domain.Theater{}
	invalidService := NewCatalogService(repository, repository, repository, &invalidGateway, fixedClock{gateway.now})
	if _, _, err := invalidService.observeAuditoriums(ctx, ref, nil); err == nil {
		t.Fatal("observeAuditoriums accepted invalid theater")
	}
	if _, err := service.persistObservedAuditorium(ctx, theater, domain.Auditorium{}); err == nil {
		t.Fatal("persistObservedAuditorium accepted invalid auditorium")
	}

	for _, fail := range []string{"put-auditorium", "capture", "put-seat-map"} {
		repository.fail = fail
		if fail == "capture" {
			repository.fail = ""
			gateway.fail = fail
		}
		if _, err := service.CaptureAuditoriumSeatMap(ctx, ref, auditorium.ID, nil); err == nil {
			t.Fatalf("CaptureAuditoriumSeatMap(%s) succeeded", fail)
		}
		repository.fail, gateway.fail = "", ""
	}

	invalidSeatMapGateway := *gateway
	invalidSeatMapGateway.seatMap = domain.SeatMap{}
	invalidService = NewCatalogService(repository, repository, repository, &invalidSeatMapGateway, fixedClock{gateway.now})
	if _, err := invalidService.CaptureAuditoriumSeatMap(ctx, ref, auditorium.ID, nil); err == nil {
		t.Fatal("CaptureAuditoriumSeatMap accepted invalid layout")
	}

	missingGateway := *gateway
	missingGateway.observations = []AuditoriumObservation{{Auditorium: auditorium}}
	missingService := NewCatalogService(repository, repository, repository, &missingGateway, fixedClock{gateway.now})
	if _, err := missingService.CaptureAuditoriumSeatMap(ctx, ref, auditorium.ID, nil); !errors.Is(err, ErrBookingNotOpen) {
		t.Fatalf("CaptureAuditoriumSeatMap(no showtime) error = %v", err)
	}
	if _, err := service.CaptureAuditoriumSeatMap(ctx, ref, "missing", nil); !errors.Is(err, ErrNotFound) {
		t.Fatalf("CaptureAuditoriumSeatMap(missing) error = %v", err)
	}

	repository.fail = "put-auditorium-after-capture"
	repository.putAuditoriumCalls = 0
	if _, err := service.captureObservedSeatMap(
		ctx, theater, auditorium, *gateway.observations[0].RepresentativeShowing,
	); !errors.Is(err, errInjected) {
		t.Fatalf("captureObservedSeatMap(auditorium) error = %v", err)
	}
	repository.fail = ""
	_ = seatMap
}

func TestCatalogServiceCoversSyncObservationOutcomes(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	service, repository, gateway, ref, theater, auditorium, seatMap := newCatalogCoverageHarness()
	observation := gateway.observations[0]

	repository.seatMaps[auditorium.ID] = seatMap
	got, analysis, incomplete, err := service.syncObservation(ctx, theater, observation, false)
	if err != nil || analysis == nil || incomplete || got.SeatMapVersion != seatMap.Version {
		t.Fatalf("syncObservation(cache) = %+v, %+v, %t, %v", got, analysis, incomplete, err)
	}

	repository.fail = "get-seat-map"
	if _, _, _, err := service.syncObservation(ctx, theater, observation, false); !errors.Is(err, errInjected) {
		t.Fatalf("syncObservation(cache error) = %v", err)
	}
	repository.fail = ""
	delete(repository.seatMaps, auditorium.ID)
	withoutShowtime := observation
	withoutShowtime.RepresentativeShowing = nil
	_, analysis, incomplete, err = service.syncObservation(ctx, theater, withoutShowtime, false)
	if err != nil || analysis != nil || !incomplete {
		t.Fatalf("syncObservation(incomplete) = %+v, %t, %v", analysis, incomplete, err)
	}

	gateway.fail = "capture"
	_, analysis, incomplete, err = service.syncObservation(ctx, theater, observation, true)
	if err != nil || analysis != nil || !incomplete {
		t.Fatalf("syncObservation(capture failure) = %+v, %t, %v", analysis, incomplete, err)
	}
	gateway.fail = ""

	repository.fail = "get-auditorium"
	if _, _, _, err := service.syncObservation(ctx, theater, observation, true); !errors.Is(err, errInjected) {
		t.Fatalf("syncObservation(persist) = %v", err)
	}
	repository.fail = ""
	repository.seatMaps[auditorium.ID] = seatMap
	repository.putAuditoriumCalls = 0
	repository.fail = "put-auditorium-cached"
	if _, _, _, err := service.syncObservation(ctx, theater, observation, false); !errors.Is(err, errInjected) {
		t.Fatalf("syncObservation(cache persist) = %v", err)
	}
	repository.fail = ""
	delete(repository.seatMaps, auditorium.ID)

	gateway.fail = "resolve"
	if _, err := service.Sync(ctx, ref, nil); !errors.Is(err, errInjected) {
		t.Fatalf("Sync(observe) = %v", err)
	}
	gateway.fail = ""
	repository.fail = "get-auditorium"
	if _, err := service.Sync(ctx, ref, nil); !errors.Is(err, errInjected) {
		t.Fatalf("Sync(observation) = %v", err)
	}
	repository.fail = ""
	withoutShowtimeResult := *gateway
	withoutShowtimeResult.observations = []AuditoriumObservation{{Auditorium: auditorium}}
	incompleteService := NewCatalogService(repository, repository, repository, &withoutShowtimeResult, fixedClock{gateway.now})
	result, err := incompleteService.Sync(ctx, ref, nil)
	if err != nil || len(result.Incomplete) != 1 {
		t.Fatalf("Sync(incomplete) = %+v, %v", result, err)
	}
	repository.fail = "list-auditoriums"
	if _, err := service.Sync(ctx, ref, nil); !errors.Is(err, errInjected) {
		t.Fatalf("Sync(list) = %v", err)
	}
}

func newCatalogCoverageHarness() (
	*CatalogService,
	*catalogRepositoryFake,
	*catalogGatewayCoverageFake,
	TheaterRef,
	domain.Theater,
	domain.Auditorium,
	domain.SeatMap,
) {
	now := time.Date(2026, time.August, 9, 10, 0, 0, 0, time.UTC)
	ref := TheaterRef{Region: "서울", Name: "용산"}
	theater := domain.Theater{ProviderID: contracts.ProviderCGV, Region: ref.Region, Name: ref.Name, SourceKey: ref.Region + "/" + ref.Name, ObservedAt: now}
	theater.ID = contracts.CatalogID(contracts.ProviderCGV, "theater", theater.SourceKey)
	auditorium := domain.Auditorium{TheaterID: theater.ID, SourceKey: theater.SourceKey + "/IMAX", Name: "IMAX", ObservedAt: now}
	auditorium.ID = contracts.CatalogID(contracts.ProviderCGV, "auditorium", auditorium.SourceKey)
	seatMap := validApplicationSeatMap(auditorium.ID, now)
	showtime := domain.Showtime{ID: "showtime", Date: "2026-08-10", StartsAt: "20:00"}
	repository := &catalogRepositoryFake{
		theaters:    map[string]domain.Theater{theater.ID: theater},
		auditoriums: map[string][]domain.Auditorium{theater.ID: {auditorium}},
		seatMaps:    make(map[string]domain.SeatMap),
	}
	gateway := &catalogGatewayCoverageFake{
		now: now, theater: theater, seatMap: seatMap,
		observations: []AuditoriumObservation{{Auditorium: auditorium, RepresentativeShowing: &showtime}},
	}
	service := NewCatalogService(repository, repository, repository, gateway, fixedClock{now})
	return service, repository, gateway, ref, theater, auditorium, seatMap
}

type catalogRepositoryFake struct {
	theaters           map[string]domain.Theater
	auditoriums        map[string][]domain.Auditorium
	seatMaps           map[string]domain.SeatMap
	fail               string
	putAuditoriumCalls int
}

func (repository *catalogRepositoryFake) PutTheater(_ context.Context, value domain.Theater) error {
	if repository.fail == "put-theater" {
		return errInjected
	}
	repository.theaters[value.ID] = value
	return nil
}

func (repository *catalogRepositoryFake) GetTheater(_ context.Context, id string) (domain.Theater, error) {
	if repository.fail == "get-theater" {
		return domain.Theater{}, errInjected
	}
	value, found := repository.theaters[id]
	if !found {
		return domain.Theater{}, ErrNotFound
	}
	return value, nil
}

func (repository *catalogRepositoryFake) ListTheaters(context.Context) ([]domain.Theater, error) {
	return nil, nil
}

func (repository *catalogRepositoryFake) PutAuditorium(_ context.Context, value domain.Auditorium) error {
	repository.putAuditoriumCalls++
	if repository.fail == "put-auditorium" ||
		repository.fail == "put-auditorium-after-capture" && repository.putAuditoriumCalls == 1 ||
		repository.fail == "put-auditorium-cached" && repository.putAuditoriumCalls == 2 {
		return errInjected
	}
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

func (repository *catalogRepositoryFake) GetAuditorium(_ context.Context, id string) (domain.Auditorium, error) {
	if repository.fail == "get-auditorium" {
		return domain.Auditorium{}, errInjected
	}
	for _, values := range repository.auditoriums {
		for _, value := range values {
			if value.ID == id {
				return value, nil
			}
		}
	}
	return domain.Auditorium{}, ErrNotFound
}

func (repository *catalogRepositoryFake) ListAuditoriumsByTheater(
	_ context.Context,
	theaterID string,
) ([]domain.Auditorium, error) {
	if repository.fail == "list-auditoriums" {
		return nil, errInjected
	}
	return append([]domain.Auditorium(nil), repository.auditoriums[theaterID]...), nil
}

func (repository *catalogRepositoryFake) PutSeatMap(_ context.Context, value domain.SeatMap) error {
	if repository.fail == "put-seat-map" {
		return errInjected
	}
	repository.seatMaps[value.AuditoriumID] = value
	return nil
}

func (repository *catalogRepositoryFake) GetSeatMap(_ context.Context, id string) (domain.SeatMap, error) {
	if repository.fail == "get-seat-map" {
		return domain.SeatMap{}, errInjected
	}
	value, found := repository.seatMaps[id]
	if !found {
		return domain.SeatMap{}, ErrNotFound
	}
	return value, nil
}

type catalogGatewayCoverageFake struct {
	now          time.Time
	theater      domain.Theater
	observations []AuditoriumObservation
	seatMap      domain.SeatMap
	fail         string
}

func (gateway *catalogGatewayCoverageFake) ResolveTheater(
	context.Context,
	TheaterRef,
) (domain.Theater, error) {
	if gateway.fail == "resolve" {
		return domain.Theater{}, errInjected
	}
	return gateway.theater, nil
}

func (gateway *catalogGatewayCoverageFake) DiscoverAuditoriums(
	context.Context,
	domain.Theater,
	[]string,
) ([]AuditoriumObservation, error) {
	if gateway.fail == "discover" {
		return nil, errInjected
	}
	return gateway.observations, nil
}

func (gateway *catalogGatewayCoverageFake) CaptureSeatMap(
	context.Context,
	domain.Auditorium,
	domain.Showtime,
) (domain.SeatMap, error) {
	if gateway.fail == "capture" {
		return domain.SeatMap{}, errInjected
	}
	return gateway.seatMap, nil
}
