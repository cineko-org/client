package memoryrepo

import (
	"context"
	"errors"
	"sort"
	"sync"
	"time"

	"github.com/cineko-org/client/internal/application"
	catalogpb "github.com/cineko-org/contracts/v3/gen/go/cineko/catalog"
	clientpb "github.com/cineko-org/contracts/v3/gen/go/cineko/client"
	collectionpb "github.com/cineko-org/contracts/v3/gen/go/cineko/collection"
	seatmappb "github.com/cineko-org/contracts/v3/gen/go/cineko/seatmap"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// Repository mirrors production generated-Proto ports so tests cannot
// accidentally reintroduce a parallel DTO boundary.
type Repository struct {
	mu           sync.RWMutex
	theaters     map[string]*catalogpb.Theater
	auditoriums  map[string]*catalogpb.Auditorium
	seatMaps     map[string]*seatmappb.Snapshot
	presets      map[string]*clientpb.Resource
	monitors     map[string]*clientpb.Resource
	reservations map[string]*clientpb.Resource
	operations   map[string]*clientpb.Resource
	events       map[string]*clientpb.Resource
	catalog      *catalogpb.CatalogIndex
}

func New() *Repository {
	return &Repository{
		theaters: make(map[string]*catalogpb.Theater), auditoriums: make(map[string]*catalogpb.Auditorium),
		seatMaps: make(map[string]*seatmappb.Snapshot), presets: make(map[string]*clientpb.Resource),
		monitors: make(map[string]*clientpb.Resource), reservations: make(map[string]*clientpb.Resource),
		operations: make(map[string]*clientpb.Resource), events: make(map[string]*clientpb.Resource),
	}
}

func clone[T proto.Message](value T) T { return proto.CloneOf(value) }

func (repository *Repository) PutTheater(_ context.Context, value *catalogpb.Theater) error {
	repository.mu.Lock()
	defer repository.mu.Unlock()
	repository.theaters[value.GetId()] = clone(value)
	return nil
}

func (repository *Repository) GetTheater(_ context.Context, id string) (*catalogpb.Theater, error) {
	repository.mu.RLock()
	defer repository.mu.RUnlock()
	value := repository.theaters[id]
	if value == nil {
		return nil, application.ErrNotFound
	}
	return clone(value), nil
}

func (repository *Repository) ListTheaters(context.Context) ([]*catalogpb.Theater, error) {
	repository.mu.RLock()
	defer repository.mu.RUnlock()
	values := cloneMap(repository.theaters)
	sort.Slice(values, func(i, j int) bool { return values[i].GetName() < values[j].GetName() })
	return values, nil
}

func (repository *Repository) PutAuditorium(_ context.Context, value *catalogpb.Auditorium) error {
	repository.mu.Lock()
	defer repository.mu.Unlock()
	repository.auditoriums[value.GetId()] = clone(value)
	return nil
}

func (repository *Repository) GetAuditorium(_ context.Context, id string) (*catalogpb.Auditorium, error) {
	repository.mu.RLock()
	defer repository.mu.RUnlock()
	value := repository.auditoriums[id]
	if value == nil {
		return nil, application.ErrNotFound
	}
	return clone(value), nil
}

func (repository *Repository) ListAuditoriumsByTheater(_ context.Context, theaterID string) ([]*catalogpb.Auditorium, error) {
	repository.mu.RLock()
	defer repository.mu.RUnlock()
	values := make([]*catalogpb.Auditorium, 0)
	for _, value := range repository.auditoriums {
		if value.GetTheaterId() == theaterID {
			values = append(values, clone(value))
		}
	}
	sort.Slice(values, func(i, j int) bool { return values[i].GetName() < values[j].GetName() })
	return values, nil
}

func (repository *Repository) PutSeatMap(_ context.Context, value *seatmappb.Snapshot) error {
	repository.mu.Lock()
	defer repository.mu.Unlock()
	repository.seatMaps[value.GetAuditoriumId()] = clone(value)
	return nil
}

func (repository *Repository) GetSeatMap(_ context.Context, auditoriumID string) (*seatmappb.Snapshot, error) {
	repository.mu.RLock()
	defer repository.mu.RUnlock()
	value := repository.seatMaps[auditoriumID]
	if value == nil {
		return nil, application.ErrNotFound
	}
	return clone(value), nil
}

func (repository *Repository) SubmitLiveSeatObservation(
	_ context.Context,
	observation *seatmappb.LiveSeatObservation,
) (*seatmappb.Snapshot, error) {
	snapshot := observation.GetLayout()
	if snapshot == nil {
		return nil, application.ErrNotFound
	}
	repository.mu.Lock()
	repository.seatMaps[snapshot.GetAuditoriumId()] = clone(snapshot)
	repository.mu.Unlock()
	return clone(snapshot), nil
}

func (repository *Repository) ResolveSeatMap(ctx context.Context, auditoriumID string) (*seatmappb.Resolution, error) {
	value, err := repository.GetSeatMap(ctx, auditoriumID)
	if errors.Is(err, application.ErrNotFound) {
		return seatmappb.Resolution_builder{State: collectionpb.State_builder{
			Queued: collectionpb.Queued_builder{
				QueuedAt: timestamppb.Now(),
				Trigger: collectionpb.Trigger_builder{
					ClientRequest: collectionpb.ClientRequest_builder{}.Build(),
				}.Build(),
			}.Build(),
		}.Build()}.Build(), nil
	}
	if err != nil {
		return nil, err
	}
	return seatmappb.Resolution_builder{
		Snapshot: value,
		State: collectionpb.State_builder{
			Idle: collectionpb.Idle_builder{}.Build(),
		}.Build(),
	}.Build(), nil
}

func (repository *Repository) PutPreset(ctx context.Context, value *clientpb.Resource) error {
	return repository.put(ctx, repository.presets, value)
}
func (repository *Repository) GetPreset(ctx context.Context, id string) (*clientpb.Resource, error) {
	return repository.get(ctx, repository.presets, id)
}
func (repository *Repository) ListPresetsByUser(ctx context.Context, userID string) ([]*clientpb.Resource, error) {
	return repository.list(ctx, repository.presets, userID)
}
func (repository *Repository) DeletePreset(ctx context.Context, id string) error {
	return repository.delete(ctx, repository.presets, id)
}

func (repository *Repository) PutMonitor(ctx context.Context, value *clientpb.Resource) error {
	return repository.put(ctx, repository.monitors, value)
}
func (repository *Repository) GetMonitor(ctx context.Context, id string) (*clientpb.Resource, error) {
	return repository.get(ctx, repository.monitors, id)
}
func (repository *Repository) ListMonitorsByUser(ctx context.Context, userID string) ([]*clientpb.Resource, error) {
	return repository.list(ctx, repository.monitors, userID)
}
func (repository *Repository) DeleteMonitor(ctx context.Context, id string) error {
	return repository.delete(ctx, repository.monitors, id)
}

func (repository *Repository) PutReservation(ctx context.Context, value *clientpb.Resource) error {
	return repository.put(ctx, repository.reservations, value)
}
func (repository *Repository) GetReservation(ctx context.Context, id string) (*clientpb.Resource, error) {
	return repository.get(ctx, repository.reservations, id)
}
func (repository *Repository) ListReservationsByUser(ctx context.Context, userID string) ([]*clientpb.Resource, error) {
	return repository.list(ctx, repository.reservations, userID)
}
func (repository *Repository) PutExternalOperation(ctx context.Context, value *clientpb.Resource) error {
	return repository.put(ctx, repository.operations, value)
}

func (repository *Repository) GetCatalog(context.Context) (*catalogpb.CatalogIndex, error) {
	repository.mu.RLock()
	defer repository.mu.RUnlock()
	if repository.catalog == nil {
		return catalogpb.CatalogIndex_builder{}.Build(), nil
	}
	return clone(repository.catalog), nil
}

func (repository *Repository) PutAppEvent(ctx context.Context, value *clientpb.Resource) error {
	return repository.put(ctx, repository.events, value)
}
func (repository *Repository) ListAppEvents(ctx context.Context, userID string, limit int) ([]*clientpb.Resource, error) {
	values, err := repository.list(ctx, repository.events, userID)
	if limit > 0 && len(values) > limit {
		values = values[:limit]
	}
	return values, err
}

func (repository *Repository) MarkAppEventsRead(_ context.Context, userID string, at time.Time) error {
	repository.mu.Lock()
	defer repository.mu.Unlock()
	for _, resource := range repository.events {
		if event := resource.GetAppEvent(); event != nil && event.GetUserId() == userID && event.GetReadAt() == nil {
			event.SetReadAt(timestamppb.New(at))
		}
	}
	return nil
}

func (repository *Repository) DeleteAppEvents(_ context.Context, userID string) error {
	repository.mu.Lock()
	defer repository.mu.Unlock()
	for id, resource := range repository.events {
		if owner(resource) == userID {
			delete(repository.events, id)
		}
	}
	return nil
}

func (repository *Repository) DeleteAppEventsBefore(_ context.Context, cutoff time.Time) error {
	repository.mu.Lock()
	defer repository.mu.Unlock()
	for id, resource := range repository.events {
		if created := resource.GetIdentity().GetCreatedAt(); created != nil && created.AsTime().Before(cutoff) {
			delete(repository.events, id)
		}
	}
	return nil
}

func (*Repository) RecoverInterruptedWork(context.Context, time.Time) ([]*clientpb.Resource, error) {
	return nil, nil
}

func (repository *Repository) put(_ context.Context, target map[string]*clientpb.Resource, value *clientpb.Resource) error {
	if value == nil || value.GetIdentity() == nil || value.GetIdentity().GetId() == "" {
		return application.ErrNotFound
	}
	repository.mu.Lock()
	defer repository.mu.Unlock()
	target[value.GetIdentity().GetId()] = clone(value)
	return nil
}

func (repository *Repository) get(_ context.Context, source map[string]*clientpb.Resource, id string) (*clientpb.Resource, error) {
	repository.mu.RLock()
	defer repository.mu.RUnlock()
	value := source[id]
	if value == nil {
		return nil, application.ErrNotFound
	}
	return clone(value), nil
}

func (repository *Repository) list(_ context.Context, source map[string]*clientpb.Resource, userID string) ([]*clientpb.Resource, error) {
	repository.mu.RLock()
	defer repository.mu.RUnlock()
	values := make([]*clientpb.Resource, 0)
	for _, value := range source {
		if owner(value) == userID {
			values = append(values, clone(value))
		}
	}
	sort.Slice(values, func(i, j int) bool { return createdAt(values[i]).After(createdAt(values[j])) })
	return values, nil
}

func (repository *Repository) delete(_ context.Context, target map[string]*clientpb.Resource, id string) error {
	repository.mu.Lock()
	defer repository.mu.Unlock()
	delete(target, id)
	return nil
}

func owner(resource *clientpb.Resource) string {
	switch {
	case resource.GetPreset() != nil:
		return resource.GetPreset().GetUserId()
	case resource.GetMonitor() != nil:
		return resource.GetMonitor().GetUserId()
	case resource.GetReservation() != nil:
		return resource.GetReservation().GetUserId()
	case resource.GetExternalOperation() != nil:
		return resource.GetExternalOperation().GetUserId()
	case resource.GetAppEvent() != nil:
		return resource.GetAppEvent().GetUserId()
	default:
		return ""
	}
}

func createdAt(resource *clientpb.Resource) time.Time {
	if resource != nil && resource.GetIdentity() != nil && resource.GetIdentity().GetCreatedAt() != nil {
		return resource.GetIdentity().GetCreatedAt().AsTime()
	}
	return time.Time{}
}

func cloneMap[T proto.Message](values map[string]T) []T {
	result := make([]T, 0, len(values))
	for _, value := range values {
		result = append(result, clone(value))
	}
	return result
}
