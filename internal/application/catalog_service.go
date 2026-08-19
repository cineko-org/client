package application

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"

	"github.com/cineko-org/client/internal/domain"
	contracts "github.com/cineko-org/contracts/v3"
)

type CatalogSyncResult struct {
	Theater     domain.Theater              `json:"theater"`
	Auditoriums []domain.Auditorium         `json:"auditoriums"`
	Analyses    []domain.AuditoriumAnalysis `json:"analyses"`
	Incomplete  []string                    `json:"incomplete"`
	Cached      bool                        `json:"cached"`
}

type CatalogService struct {
	theaters    TheaterRepository
	auditoriums AuditoriumRepository
	seatMaps    SeatMapRepository
	gateway     CatalogGateway
	clock       Clock
}

func NewCatalogService(
	theaters TheaterRepository,
	auditoriums AuditoriumRepository,
	seatMaps SeatMapRepository,
	gateway CatalogGateway,
	clock Clock,
) *CatalogService {
	return &CatalogService{
		theaters: theaters, auditoriums: auditoriums, seatMaps: seatMaps, gateway: gateway, clock: clock,
	}
}

func (service *CatalogService) Sync(
	ctx context.Context,
	ref TheaterRef,
	targetDates []string,
) (CatalogSyncResult, error) {
	return service.sync(ctx, ref, targetDates, true)
}

func (service *CatalogService) Ensure(
	ctx context.Context,
	ref TheaterRef,
	targetDates []string,
) (CatalogSyncResult, error) {
	if result, found, err := service.Cached(ctx, ref); err != nil || found {
		return result, err
	}
	return service.sync(ctx, ref, targetDates, false)
}

func (service *CatalogService) Cached(
	ctx context.Context,
	ref TheaterRef,
) (CatalogSyncResult, bool, error) {
	sourceKey := strings.TrimSpace(ref.Region) + "/" + strings.TrimSpace(ref.Name)
	theaterID := contracts.CatalogID(contracts.ProviderCGV, "theater", sourceKey)
	theater, err := service.theaters.GetTheater(ctx, theaterID)
	if errors.Is(err, ErrNotFound) {
		return CatalogSyncResult{}, false, nil
	}
	if err != nil {
		return CatalogSyncResult{}, false, err
	}
	auditoriums, err := service.auditoriums.ListAuditoriumsByTheater(ctx, theater.ID)
	if err != nil {
		return CatalogSyncResult{}, false, err
	}
	if len(auditoriums) == 0 {
		return CatalogSyncResult{}, false, nil
	}
	result := CatalogSyncResult{Theater: theater, Auditoriums: auditoriums, Cached: true}
	for _, auditorium := range auditoriums {
		seatMap, getErr := service.seatMaps.GetSeatMap(ctx, auditorium.ID)
		if errors.Is(getErr, ErrNotFound) {
			return CatalogSyncResult{}, false, nil
		}
		if getErr != nil {
			return CatalogSyncResult{}, false, getErr
		}
		result.Analyses = append(result.Analyses, domain.AnalyzeSeatMap(seatMap))
	}
	return result, true, nil
}

func (service *CatalogService) DiscoverAuditoriums(
	ctx context.Context,
	ref TheaterRef,
	targetDates []string,
) (CatalogSyncResult, error) {
	theater, observations, err := service.observeAuditoriums(ctx, ref, targetDates)
	if err != nil {
		return CatalogSyncResult{}, err
	}
	result := CatalogSyncResult{Theater: theater}
	for _, observation := range observations {
		auditorium, persistErr := service.persistObservedAuditorium(ctx, theater, observation.Auditorium)
		if persistErr != nil {
			return CatalogSyncResult{}, persistErr
		}
		result.Auditoriums = append(result.Auditoriums, auditorium)
		if auditorium.SeatMapVersion == "" {
			result.Incomplete = append(result.Incomplete, auditorium.Name)
		}
	}
	return result, nil
}

func (service *CatalogService) CaptureAuditoriumSeatMap(
	ctx context.Context,
	ref TheaterRef,
	auditoriumID string,
	targetDates []string,
) (domain.SeatMap, error) {
	theater, observations, err := service.observeAuditoriums(ctx, ref, targetDates)
	if err != nil {
		return domain.SeatMap{}, err
	}
	for _, observation := range observations {
		auditorium, persistErr := service.persistObservedAuditorium(ctx, theater, observation.Auditorium)
		if persistErr != nil {
			return domain.SeatMap{}, persistErr
		}
		if auditorium.ID != auditoriumID {
			continue
		}
		if observation.RepresentativeShowing == nil {
			return domain.SeatMap{}, fmt.Errorf(
				"%w: selected auditorium has no bookable representative showtime",
				ErrBookingNotOpen,
			)
		}
		return service.captureObservedSeatMap(ctx, theater, auditorium, *observation.RepresentativeShowing)
	}
	return domain.SeatMap{}, ErrNotFound
}

func (service *CatalogService) observeAuditoriums(
	ctx context.Context,
	ref TheaterRef,
	targetDates []string,
) (domain.Theater, []AuditoriumObservation, error) {
	theater, err := service.gateway.ResolveTheater(ctx, ref)
	if err != nil {
		return domain.Theater{}, nil, err
	}
	theater.ProviderID = contracts.ProviderCGV
	if strings.TrimSpace(theater.SourceKey) == "" {
		theater.SourceKey = strings.TrimSpace(theater.Region) + "/" + strings.TrimSpace(theater.Name)
	}
	theater.ID = contracts.CatalogID(theater.ProviderID, "theater", theater.SourceKey)
	theater.ObservedAt = service.clock.Now()
	if validateErr := theater.Validate(); validateErr != nil {
		return domain.Theater{}, nil, validateErr
	}
	if putErr := service.theaters.PutTheater(ctx, theater); putErr != nil {
		return domain.Theater{}, nil, putErr
	}
	observations, discoverErr := service.gateway.DiscoverAuditoriums(ctx, theater, targetDates)
	return theater, observations, discoverErr
}

func (service *CatalogService) persistObservedAuditorium(
	ctx context.Context,
	theater domain.Theater,
	observed domain.Auditorium,
) (domain.Auditorium, error) {
	if strings.TrimSpace(observed.SourceKey) == "" {
		observed.SourceKey = theater.SourceKey + "/" + strings.TrimSpace(observed.Name)
	}
	observed.ID = contracts.CatalogID(theater.ProviderID, "auditorium", observed.SourceKey)
	observed.TheaterID = theater.ID
	observed.ObservedAt = service.clock.Now()
	existing, err := service.auditoriums.GetAuditorium(ctx, observed.ID)
	switch {
	case err == nil:
		observed.SeatMapVersion = existing.SeatMapVersion
		observed.Capacity = max(observed.Capacity, existing.Capacity)
	case errors.Is(err, ErrNotFound):
	default:
		return domain.Auditorium{}, err
	}
	if validateErr := observed.Validate(); validateErr != nil {
		return domain.Auditorium{}, validateErr
	}
	if putErr := service.auditoriums.PutAuditorium(ctx, observed); putErr != nil {
		return domain.Auditorium{}, putErr
	}
	return observed, nil
}

func (service *CatalogService) captureObservedSeatMap(
	ctx context.Context,
	theater domain.Theater,
	auditorium domain.Auditorium,
	showtime domain.Showtime,
) (domain.SeatMap, error) {
	showtime.TheaterID = theater.ID
	showtime.AuditoriumID = auditorium.ID
	seatMap, err := service.gateway.CaptureSeatMap(ctx, auditorium, showtime)
	if err != nil {
		return domain.SeatMap{}, err
	}
	seatMap.AuditoriumID = auditorium.ID
	for index := range seatMap.Seats {
		seatMap.Seats[index].AuditoriumID = auditorium.ID
		seatMap.Seats[index].ID = StableID("seat", auditorium.ID, seatMap.Seats[index].Label)
	}
	if validateErr := seatMap.Validate(); validateErr != nil {
		return domain.SeatMap{}, validateErr
	}
	if putErr := service.seatMaps.PutSeatMap(ctx, seatMap); putErr != nil {
		return domain.SeatMap{}, putErr
	}
	auditorium.Capacity = len(seatMap.Seats)
	auditorium.SeatMapVersion = seatMap.Version
	if putErr := service.auditoriums.PutAuditorium(ctx, auditorium); putErr != nil {
		return domain.SeatMap{}, putErr
	}
	return seatMap, nil
}

func (service *CatalogService) sync(
	ctx context.Context,
	ref TheaterRef,
	targetDates []string,
	force bool,
) (CatalogSyncResult, error) {
	theater, observations, err := service.observeAuditoriums(ctx, ref, targetDates)
	if err != nil {
		return CatalogSyncResult{}, err
	}
	result := CatalogSyncResult{Theater: theater}
	for _, observation := range observations {
		auditorium, analysis, incomplete, syncErr := service.syncObservation(ctx, theater, observation, force)
		if syncErr != nil {
			return CatalogSyncResult{}, syncErr
		}
		result.Auditoriums = append(result.Auditoriums, auditorium)
		if analysis != nil {
			result.Analyses = append(result.Analyses, *analysis)
		}
		if incomplete {
			result.Incomplete = append(result.Incomplete, auditorium.Name)
		}
	}
	allAuditoriums, err := service.auditoriums.ListAuditoriumsByTheater(ctx, theater.ID)
	if err != nil {
		return CatalogSyncResult{}, err
	}
	result.Auditoriums = allAuditoriums
	return result, nil
}

func (service *CatalogService) syncObservation(
	ctx context.Context,
	theater domain.Theater,
	observation AuditoriumObservation,
	force bool,
) (domain.Auditorium, *domain.AuditoriumAnalysis, bool, error) {
	auditorium, err := service.persistObservedAuditorium(ctx, theater, observation.Auditorium)
	if err != nil {
		return domain.Auditorium{}, nil, false, err
	}
	if !force {
		cachedSeatMap, getErr := service.seatMaps.GetSeatMap(ctx, auditorium.ID)
		switch {
		case getErr == nil:
			auditorium.Capacity = len(cachedSeatMap.Seats)
			auditorium.SeatMapVersion = cachedSeatMap.Version
			if putErr := service.auditoriums.PutAuditorium(ctx, auditorium); putErr != nil {
				return domain.Auditorium{}, nil, false, putErr
			}
			analysis := domain.AnalyzeSeatMap(cachedSeatMap)
			return auditorium, &analysis, false, nil
		case !errors.Is(getErr, ErrNotFound):
			return domain.Auditorium{}, nil, false, getErr
		}
	}
	if observation.RepresentativeShowing == nil {
		return auditorium, nil, true, nil
	}
	seatMap, captureErr := service.captureObservedSeatMap(
		ctx, theater, auditorium, *observation.RepresentativeShowing,
	)
	if captureErr != nil {
		return uncapturedAuditorium(auditorium)
	}
	auditorium.Capacity = len(seatMap.Seats)
	auditorium.SeatMapVersion = seatMap.Version
	analysis := domain.AnalyzeSeatMap(seatMap)
	return auditorium, &analysis, false, nil
}

func uncapturedAuditorium(auditorium domain.Auditorium) (domain.Auditorium, *domain.AuditoriumAnalysis, bool, error) {
	return auditorium, nil, true, nil
}

func StableID(parts ...string) string {
	hash := sha256.Sum256([]byte(strings.Join(parts, "\x00")))
	return hex.EncodeToString(hash[:12])
}
