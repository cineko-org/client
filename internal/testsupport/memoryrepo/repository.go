package memoryrepo

import (
	"context"
	"encoding/json"
	"errors"
	"sort"
	"sync"
	"time"

	"github.com/cineko-org/client/internal/application"
	"github.com/cineko-org/client/internal/domain"
	contracts "github.com/cineko-org/contracts/v3"
)

type monitorLease struct {
	owner     string
	expiresAt time.Time
}

type Repository struct {
	mu                 sync.RWMutex
	theaters           map[string]domain.Theater
	auditoriums        map[string]domain.Auditorium
	seatMaps           map[string]domain.SeatMap
	presets            map[string]domain.Preset
	monitors           map[string]domain.MonitorJob
	monitorLeases      map[string]monitorLease
	reservations       map[string]domain.Reservation
	externalOperations map[string]domain.ExternalOperation
	events             map[string]domain.AppEvent
	catalog            contracts.CatalogIndex
}

func New() *Repository {
	return &Repository{
		theaters: make(map[string]domain.Theater), auditoriums: make(map[string]domain.Auditorium),
		seatMaps: make(map[string]domain.SeatMap), presets: make(map[string]domain.Preset),
		monitors: make(map[string]domain.MonitorJob), monitorLeases: make(map[string]monitorLease),
		reservations: make(map[string]domain.Reservation), externalOperations: make(map[string]domain.ExternalOperation),
		events: make(map[string]domain.AppEvent),
	}
}

func clone[T any](value T) T {
	data, _ := json.Marshal(value)
	var result T
	_ = json.Unmarshal(data, &result)
	return result
}

func (repository *Repository) PutTheater(_ context.Context, value domain.Theater) error {
	repository.mu.Lock()
	defer repository.mu.Unlock()
	repository.theaters[value.ID] = clone(value)
	return nil
}

func (repository *Repository) GetTheater(_ context.Context, id string) (domain.Theater, error) {
	repository.mu.RLock()
	defer repository.mu.RUnlock()
	value, found := repository.theaters[id]
	if !found {
		return domain.Theater{}, application.ErrNotFound
	}
	return clone(value), nil
}

func (repository *Repository) ListTheaters(context.Context) ([]domain.Theater, error) {
	repository.mu.RLock()
	defer repository.mu.RUnlock()
	return sortedValues(repository.theaters, func(value domain.Theater) string { return value.Name }), nil
}

func (repository *Repository) PutAuditorium(_ context.Context, value domain.Auditorium) error {
	repository.mu.Lock()
	defer repository.mu.Unlock()
	repository.auditoriums[value.ID] = clone(value)
	return nil
}

func (repository *Repository) GetAuditorium(_ context.Context, id string) (domain.Auditorium, error) {
	repository.mu.RLock()
	defer repository.mu.RUnlock()
	value, found := repository.auditoriums[id]
	if !found {
		return domain.Auditorium{}, application.ErrNotFound
	}
	return clone(value), nil
}

func (repository *Repository) ListAuditoriumsByTheater(_ context.Context, theaterID string) ([]domain.Auditorium, error) {
	repository.mu.RLock()
	defer repository.mu.RUnlock()
	values := make([]domain.Auditorium, 0)
	for _, value := range repository.auditoriums {
		if value.TheaterID == theaterID {
			values = append(values, clone(value))
		}
	}
	sort.Slice(values, func(i, j int) bool { return values[i].Name < values[j].Name })
	return values, nil
}

func (repository *Repository) PutSeatMap(_ context.Context, value domain.SeatMap) error {
	repository.mu.Lock()
	defer repository.mu.Unlock()
	repository.seatMaps[value.AuditoriumID] = clone(value)
	return nil
}

func (repository *Repository) GetSeatMap(_ context.Context, auditoriumID string) (domain.SeatMap, error) {
	repository.mu.RLock()
	defer repository.mu.RUnlock()
	value, found := repository.seatMaps[auditoriumID]
	if !found {
		return domain.SeatMap{}, application.ErrNotFound
	}
	return clone(value), nil
}

func (repository *Repository) ResolveSeatMap(
	ctx context.Context,
	auditoriumID string,
) (domain.SeatMap, bool, error) {
	value, err := repository.GetSeatMap(ctx, auditoriumID)
	if errors.Is(err, application.ErrNotFound) {
		return domain.SeatMap{}, false, nil
	}
	return value, err == nil, err
}

func (repository *Repository) PutPreset(_ context.Context, value domain.Preset) error {
	repository.mu.Lock()
	defer repository.mu.Unlock()
	repository.presets[value.ID] = clone(value)
	return nil
}

func (repository *Repository) GetPreset(_ context.Context, id string) (domain.Preset, error) {
	repository.mu.RLock()
	defer repository.mu.RUnlock()
	value, found := repository.presets[id]
	if !found {
		return domain.Preset{}, application.ErrNotFound
	}
	return clone(value), nil
}

func (repository *Repository) ListPresetsByUser(_ context.Context, userID string) ([]domain.Preset, error) {
	return lockedValuesByUser(&repository.mu, repository.presets, userID, func(value domain.Preset) string { return value.UserID }, func(value domain.Preset) time.Time { return value.CreatedAt }), nil
}

func (repository *Repository) DeletePreset(_ context.Context, id string) error {
	repository.mu.Lock()
	defer repository.mu.Unlock()
	delete(repository.presets, id)
	return nil
}

func (repository *Repository) PutMonitor(_ context.Context, value domain.MonitorJob) error {
	repository.mu.Lock()
	defer repository.mu.Unlock()
	repository.monitors[value.ID] = clone(value)
	return nil
}

func (repository *Repository) GetMonitor(_ context.Context, id string) (domain.MonitorJob, error) {
	repository.mu.RLock()
	defer repository.mu.RUnlock()
	value, found := repository.monitors[id]
	if !found {
		return domain.MonitorJob{}, application.ErrNotFound
	}
	return clone(value), nil
}

func (repository *Repository) ListMonitorsByUser(_ context.Context, userID string) ([]domain.MonitorJob, error) {
	return lockedValuesByUser(&repository.mu, repository.monitors, userID, func(value domain.MonitorJob) string { return value.UserID }, func(value domain.MonitorJob) time.Time { return value.CreatedAt }), nil
}

func (repository *Repository) DeleteMonitor(_ context.Context, id string) error {
	repository.mu.Lock()
	defer repository.mu.Unlock()
	delete(repository.monitors, id)
	delete(repository.monitorLeases, id)
	return nil
}

func (repository *Repository) AcquireMonitor(
	_ context.Context, id string, owner string, now time.Time, ttl time.Duration,
) (domain.MonitorJob, error) {
	repository.mu.Lock()
	defer repository.mu.Unlock()
	value, found := repository.monitors[id]
	if !found {
		return domain.MonitorJob{}, application.ErrNotFound
	}
	lease := repository.monitorLeases[id]
	if lease.owner != "" && lease.owner != owner && lease.expiresAt.After(now) {
		return domain.MonitorJob{}, application.ErrConflict
	}
	repository.monitorLeases[id] = monitorLease{owner: owner, expiresAt: now.Add(ttl)}
	return clone(value), nil
}

func (repository *Repository) RenewMonitor(
	_ context.Context, id string, owner string, now time.Time, ttl time.Duration,
) error {
	repository.mu.Lock()
	defer repository.mu.Unlock()
	lease := repository.monitorLeases[id]
	if lease.owner != owner || !lease.expiresAt.After(now) {
		return application.ErrConflict
	}
	repository.monitorLeases[id] = monitorLease{owner: owner, expiresAt: now.Add(ttl)}
	return nil
}

func (repository *Repository) ReleaseMonitor(_ context.Context, id string, owner string) error {
	repository.mu.Lock()
	defer repository.mu.Unlock()
	if repository.monitorLeases[id].owner == owner {
		delete(repository.monitorLeases, id)
	}
	return nil
}

func (repository *Repository) PutReservation(_ context.Context, value domain.Reservation) error {
	repository.mu.Lock()
	defer repository.mu.Unlock()
	repository.reservations[value.ID] = clone(value)
	return nil
}

func (repository *Repository) GetReservation(_ context.Context, id string) (domain.Reservation, error) {
	repository.mu.RLock()
	defer repository.mu.RUnlock()
	value, found := repository.reservations[id]
	if !found {
		return domain.Reservation{}, application.ErrNotFound
	}
	return clone(value), nil
}

func (repository *Repository) ListReservationsByUser(_ context.Context, userID string) ([]domain.Reservation, error) {
	repository.mu.RLock()
	defer repository.mu.RUnlock()
	values := make([]domain.Reservation, 0)
	for _, value := range repository.reservations {
		if value.UserID == userID {
			values = append(values, clone(value))
		}
	}
	return values, nil
}

func (repository *Repository) PutExternalOperation(_ context.Context, value domain.ExternalOperation) error {
	repository.mu.Lock()
	defer repository.mu.Unlock()
	repository.externalOperations[value.ID] = clone(value)
	return nil
}

func (repository *Repository) GetCatalog(context.Context) (contracts.CatalogIndex, error) {
	repository.mu.RLock()
	defer repository.mu.RUnlock()
	return clone(repository.catalog), nil
}

func (repository *Repository) PutAppEvent(_ context.Context, value domain.AppEvent) error {
	repository.mu.Lock()
	defer repository.mu.Unlock()
	repository.events[value.ID] = clone(value)
	return nil
}

func (repository *Repository) ListAppEvents(_ context.Context, userID string, limit int) ([]domain.AppEvent, error) {
	repository.mu.RLock()
	defer repository.mu.RUnlock()
	values := make([]domain.AppEvent, 0)
	for _, value := range repository.events {
		if value.UserID == userID {
			values = append(values, clone(value))
		}
	}
	sort.Slice(values, func(i, j int) bool { return values[i].CreatedAt.After(values[j].CreatedAt) })
	if limit > 0 && len(values) > limit {
		values = values[:limit]
	}
	return values, nil
}

func (repository *Repository) MarkAppEventsRead(_ context.Context, userID string, at time.Time) error {
	repository.mu.Lock()
	defer repository.mu.Unlock()
	for id, value := range repository.events {
		if value.UserID == userID && value.ReadAt == nil {
			readAt := at
			value.ReadAt = &readAt
			repository.events[id] = value
		}
	}
	return nil
}

func (repository *Repository) DeleteAppEvents(_ context.Context, userID string) error {
	repository.mu.Lock()
	defer repository.mu.Unlock()
	for id, value := range repository.events {
		if value.UserID == userID {
			delete(repository.events, id)
		}
	}
	return nil
}

func (repository *Repository) DeleteAppEventsBefore(_ context.Context, cutoff time.Time) error {
	repository.mu.Lock()
	defer repository.mu.Unlock()
	for id, value := range repository.events {
		if value.CreatedAt.Before(cutoff) {
			delete(repository.events, id)
		}
	}
	return nil
}

func (*Repository) RecoverInterruptedWork(context.Context, time.Time) ([]domain.AppEvent, error) {
	return nil, nil
}

func sortedValues[T any](values map[string]T, key func(T) string) []T {
	result := make([]T, 0, len(values))
	for _, value := range values {
		result = append(result, clone(value))
	}
	sort.Slice(result, func(i, j int) bool { return key(result[i]) < key(result[j]) })
	return result
}

func filteredValues[T any](
	values map[string]T,
	include func(T) bool,
	timestamp func(T) time.Time,
) []T {
	result := make([]T, 0)
	for _, value := range values {
		if include(value) {
			result = append(result, clone(value))
		}
	}
	sort.Slice(result, func(i, j int) bool { return timestamp(result[i]).Before(timestamp(result[j])) })
	return result
}

func lockedValuesByUser[T any](
	mutex *sync.RWMutex,
	values map[string]T,
	userID string,
	owner func(T) string,
	timestamp func(T) time.Time,
) []T {
	mutex.RLock()
	defer mutex.RUnlock()
	return filteredValues(values, func(value T) bool { return owner(value) == userID }, timestamp)
}
