package local

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/cineko-org/client/internal/application"
	catalogpb "github.com/cineko-org/contracts/v3/gen/go/cineko/catalog"
	clientpb "github.com/cineko-org/contracts/v3/gen/go/cineko/client"
	collectionpb "github.com/cineko-org/contracts/v3/gen/go/cineko/collection"
	commonpb "github.com/cineko-org/contracts/v3/gen/go/cineko/common"
	observationpb "github.com/cineko-org/contracts/v3/gen/go/cineko/observation"
	seatmappb "github.com/cineko-org/contracts/v3/gen/go/cineko/seatmap"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"
)

const localUserID = "local"

// Store is the single-user durable Client database. Every record remains a
// generated protobuf message on disk so the application does not gain a second
// DTO or network-owned persistence contract.
type Store struct {
	mu sync.RWMutex

	root             string
	settings         *clientpb.Resource
	resources        map[string]map[string]*clientpb.Resource
	catalog          *catalogpb.CatalogIndex
	seatMaps         map[string]*seatmappb.Snapshot
	posters          map[string]*catalogpb.MoviePoster
	changed          chan struct{}
	seatChanged      chan struct{}
	seatRequests     chan string
	scheduleRequests chan string
}

func Open(dataDir string) (*Store, error) {
	root := filepath.Join(filepath.Clean(dataDir), "state")
	store := &Store{
		root: root,
		resources: map[string]map[string]*clientpb.Resource{
			"presets": {}, "monitors": {}, "reservations": {}, "external-operations": {}, "app-events": {},
		},
		catalog:          catalogpb.CatalogIndex_builder{}.Build(),
		seatMaps:         make(map[string]*seatmappb.Snapshot),
		posters:          make(map[string]*catalogpb.MoviePoster),
		changed:          make(chan struct{}, 1),
		seatChanged:      make(chan struct{}, 1),
		seatRequests:     make(chan string, 16),
		scheduleRequests: make(chan string, 16),
	}
	for _, directory := range []string{
		root, filepath.Join(root, "resources"), filepath.Join(root, "seat-maps"), filepath.Join(root, "posters"),
	} {
		if err := os.MkdirAll(directory, 0o700); err != nil {
			return nil, fmt.Errorf("create local state directory: %w", err)
		}
	}
	if err := store.load(); err != nil {
		return nil, err
	}
	return store, nil
}

func (store *Store) Close() error                     { return nil }
func (store *Store) UserID() string                   { return localUserID }
func (store *Store) ResourceChanged() <-chan struct{} { return store.changed }
func (store *Store) SeatMapRequests() <-chan string   { return store.seatRequests }
func (store *Store) ScheduleRequests() <-chan string  { return store.scheduleRequests }

func (store *Store) load() error {
	if err := store.loadSettings(); err != nil {
		return err
	}
	if err := store.loadCatalog(); err != nil {
		return err
	}
	if err := store.loadResources(); err != nil {
		return err
	}
	if err := store.loadSeatMaps(); err != nil {
		return err
	}
	if err := store.loadPosters(); err != nil {
		return err
	}
	return store.reconcilePosterURLs()
}

func (store *Store) loadSettings() error {
	store.settings = &clientpb.Resource{}
	if err := readProto(filepath.Join(store.root, "settings.json"), &store.settings); err != nil {
		return fmt.Errorf("load local settings: %w", err)
	}
	if store.settings.GetSettings() == nil {
		store.settings = nil
	}
	return nil
}

func (store *Store) loadCatalog() error {
	if err := readProto(filepath.Join(store.root, "catalog.json"), &store.catalog); err != nil {
		return fmt.Errorf("load local catalog: %w", err)
	}
	if store.catalog == nil {
		store.catalog = catalogpb.CatalogIndex_builder{}.Build()
	}
	return nil
}

func (store *Store) loadResources() error {
	for kind := range store.resources {
		directory := filepath.Join(store.root, "resources", kind)
		if err := os.MkdirAll(directory, 0o700); err != nil {
			return err
		}
		if err := loadDirectory(directory, func(contents []byte) error {
			value := &clientpb.Resource{}
			if err := (protojson.UnmarshalOptions{DiscardUnknown: false}).Unmarshal(contents, value); err != nil {
				return err
			}
			if err := validateResource(kind, value); err != nil {
				return err
			}
			store.resources[kind][value.GetIdentity().GetId()] = value
			return nil
		}); err != nil {
			return fmt.Errorf("load local %s: %w", kind, err)
		}
	}
	return nil
}

func (store *Store) loadSeatMaps() error {
	if err := loadDirectory(filepath.Join(store.root, "seat-maps"), func(contents []byte) error {
		value := &seatmappb.Snapshot{}
		if err := (protojson.UnmarshalOptions{DiscardUnknown: false}).Unmarshal(contents, value); err != nil {
			return err
		}
		if value.GetAuditoriumId() == "" {
			return errors.New("local seat map has no auditorium")
		}
		store.seatMaps[value.GetAuditoriumId()] = value
		return nil
	}); err != nil {
		return fmt.Errorf("load local seat maps: %w", err)
	}
	return nil
}

func (store *Store) loadPosters() error {
	if err := loadDirectory(filepath.Join(store.root, "posters"), func(contents []byte) error {
		value := &catalogpb.MoviePoster{}
		if err := (protojson.UnmarshalOptions{DiscardUnknown: false}).Unmarshal(contents, value); err != nil {
			return err
		}
		if value.GetMovieId() == "" || len(value.GetData()) == 0 {
			return errors.New("local poster is incomplete")
		}
		store.posters[value.GetMovieId()] = value
		return nil
	}); err != nil {
		return fmt.Errorf("load local posters: %w", err)
	}
	return nil
}

func (store *Store) reconcilePosterURLs() error {
	if attachPosterURLs(store.catalog, store.posters) {
		if err := writeProtoAtomic(filepath.Join(store.root, "catalog.json"), store.catalog); err != nil {
			return fmt.Errorf("reconcile local poster URLs: %w", err)
		}
	}
	return nil
}

func loadDirectory(directory string, consume func([]byte) error) error {
	entries, err := os.ReadDir(directory)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}
		contents, err := os.ReadFile(filepath.Join(directory, entry.Name())) // #nosec G304 -- entry is scoped to the local state directory.
		if err != nil {
			return err
		}
		if err := consume(contents); err != nil {
			return fmt.Errorf("decode %s: %w", entry.Name(), err)
		}
	}
	return nil
}

func readProto[T proto.Message](path string, destination *T) error {
	contents, err := os.ReadFile(path) // #nosec G304 -- path is scoped to Cineko's local state directory.
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	return (protojson.UnmarshalOptions{DiscardUnknown: false}).Unmarshal(contents, *destination)
}

func writeProtoAtomic(path string, value proto.Message) error {
	contents, err := protojson.MarshalOptions{UseProtoNames: false}.Marshal(value)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	temporary, err := os.CreateTemp(filepath.Dir(path), ".cineko-state-*")
	if err != nil {
		return err
	}
	temporaryPath := temporary.Name()
	defer func() { _ = os.Remove(temporaryPath) }()
	if err := temporary.Chmod(0o600); err != nil {
		_ = temporary.Close()
		return err
	}
	if _, err := temporary.Write(contents); err != nil {
		_ = temporary.Close()
		return err
	}
	if err := temporary.Sync(); err != nil {
		_ = temporary.Close()
		return err
	}
	if err := temporary.Close(); err != nil {
		return err
	}
	return os.Rename(temporaryPath, path)
}

func recordPath(root, kind, id string) string {
	digest := sha256.Sum256([]byte(kind + "\x00" + strings.TrimSpace(id)))
	return filepath.Join(root, kind, hex.EncodeToString(digest[:])+".json")
}

func (store *Store) signalChanged() {
	select {
	case store.changed <- struct{}{}:
	default:
	}
}

func (store *Store) GetSettings(_ context.Context, output *clientpb.Settings) (int64, error) {
	store.mu.RLock()
	defer store.mu.RUnlock()
	if store.settings == nil {
		return 0, application.ErrNotFound
	}
	if output != nil {
		proto.Reset(output)
		proto.Merge(output, store.settings.GetSettings())
	}
	return store.settings.GetIdentity().GetRevision(), nil
}

func (store *Store) PutSettings(_ context.Context, input *clientpb.Settings, expectedRevision int64) error {
	if input == nil || expectedRevision < 0 {
		return errors.New("local settings mutation is invalid")
	}
	store.mu.Lock()
	defer store.mu.Unlock()
	currentRevision := int64(0)
	var createdAt *timestamppb.Timestamp
	if store.settings != nil {
		currentRevision = store.settings.GetIdentity().GetRevision()
		createdAt = store.settings.GetIdentity().GetCreatedAt()
	}
	if currentRevision != expectedRevision {
		return application.ErrConflict
	}
	now := timestamppb.Now()
	if createdAt == nil {
		createdAt = now
	}
	id, revision := "settings", currentRevision+1
	value := clientpb.Resource_builder{
		Identity: commonpb.ResourceIdentity_builder{Id: &id, Revision: &revision, CreatedAt: createdAt, UpdatedAt: now}.Build(),
		Settings: proto.CloneOf(input),
	}.Build()
	if err := writeProtoAtomic(filepath.Join(store.root, "settings.json"), value); err != nil {
		return err
	}
	store.settings = value
	store.signalChanged()
	return nil
}

func (store *Store) GetCatalog(context.Context) (*catalogpb.CatalogIndex, error) {
	store.mu.RLock()
	defer store.mu.RUnlock()
	return proto.CloneOf(store.catalog), nil
}

func (store *Store) PutCatalogSnapshot(_ context.Context, snapshot *catalogpb.CatalogSnapshot) error {
	if snapshot == nil || snapshot.GetProvider() == nil {
		return errors.New("catalog snapshot is required")
	}
	store.mu.Lock()
	defer store.mu.Unlock()
	index := proto.CloneOf(store.catalog)
	upsertProvider(index, snapshot.GetProvider())
	for _, theater := range snapshot.GetTheaters() {
		upsertTheater(index, theater)
	}
	for _, movie := range snapshot.GetMovies() {
		upsertMovie(index, movie)
	}
	for _, auditorium := range snapshot.GetAuditoriums() {
		upsertAuditorium(index, auditorium)
	}
	for _, showtime := range snapshot.GetShowtimes() {
		upsertShowtime(index, showtime)
	}
	for _, poster := range snapshot.GetPosters() {
		if poster == nil || poster.GetMovieId() == "" || len(poster.GetData()) == 0 {
			continue
		}
		if current := store.posters[poster.GetMovieId()]; current != nil && current.GetContentHash() == poster.GetContentHash() {
			continue
		}
		path := recordPath(store.root, "posters", poster.GetMovieId())
		if err := writeProtoAtomic(path, poster); err != nil {
			return err
		}
		store.posters[poster.GetMovieId()] = proto.CloneOf(poster)
	}
	attachPosterURLs(index, store.posters)
	index.SetGeneration(index.GetGeneration() + 1)
	if err := writeProtoAtomic(filepath.Join(store.root, "catalog.json"), index); err != nil {
		return err
	}
	store.catalog = index
	store.signalChanged()
	return nil
}

func (store *Store) PutScheduleCaptures(
	_ context.Context,
	theater *catalogpb.Theater,
	captures []*observationpb.Capture,
) error {
	if theater == nil || theater.GetId() == "" {
		return errors.New("schedule theater is required")
	}
	store.mu.Lock()
	defer store.mu.Unlock()
	index := proto.CloneOf(store.catalog)
	upsertTheater(index, theater)
	for _, capture := range captures {
		applyScheduleCapture(index, theater.GetId(), capture)
	}
	index.SetGeneration(index.GetGeneration() + 1)
	if err := writeProtoAtomic(filepath.Join(store.root, "catalog.json"), index); err != nil {
		return err
	}
	store.catalog = index
	store.signalChanged()
	return nil
}

func applyScheduleCapture(index *catalogpb.CatalogIndex, theaterID string, capture *observationpb.Capture) {
	if capture == nil || !capture.GetComplete() || capture.GetTargetDate() == nil {
		return
	}
	dateKey := localDateKey(capture.GetTargetDate())
	kept := make([]*catalogpb.Showtime, 0, len(index.GetShowtimes())+len(capture.GetShowtimes()))
	for _, existing := range index.GetShowtimes() {
		identity := existing.GetIdentity().GetCgv()
		if existing.GetTheaterId() == theaterID && identity != nil && localDateKey(identity.GetScheduleDate()) == dateKey {
			continue
		}
		kept = append(kept, existing)
	}
	index.SetShowtimes(kept)
	for _, showtime := range capture.GetShowtimes() {
		applyCapturedShowtime(index, showtime)
	}
}

func applyCapturedShowtime(index *catalogpb.CatalogIndex, showtime *catalogpb.Showtime) {
	if showtime == nil {
		return
	}
	upsertShowtime(index, showtime)
	if showtime.GetMovie() != nil {
		upsertMovie(index, showtime.GetMovie())
	}
	if showtime.GetAuditorium() != nil {
		upsertAuditorium(index, showtime.GetAuditorium())
	}
}

func (store *Store) CachedPosterMovieIDs() []string {
	store.mu.RLock()
	defer store.mu.RUnlock()
	ids := make([]string, 0, len(store.posters))
	for id := range store.posters {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids
}

func (store *Store) GetMoviePoster(_ context.Context, movieID string) (*application.MoviePoster, error) {
	store.mu.RLock()
	defer store.mu.RUnlock()
	poster := store.posters[strings.TrimSpace(movieID)]
	if poster == nil {
		return nil, application.ErrNotFound
	}
	return &application.MoviePoster{
		MovieID: poster.GetMovieId(), MediaType: poster.GetMediaType(), ContentHash: poster.GetContentHash(),
		Data: append([]byte(nil), poster.GetData()...),
	}, nil
}

func (store *Store) GetTheater(_ context.Context, id string) (*catalogpb.Theater, error) {
	store.mu.RLock()
	defer store.mu.RUnlock()
	for _, value := range store.catalog.GetTheaters() {
		if value.GetId() == id {
			return proto.CloneOf(value), nil
		}
	}
	return nil, application.ErrNotFound
}

func (store *Store) ListTheaters(context.Context) ([]*catalogpb.Theater, error) {
	store.mu.RLock()
	defer store.mu.RUnlock()
	values := proto.CloneOf(store.catalog).GetTheaters()
	sort.Slice(values, func(i, j int) bool {
		if values[i].GetRegion() == values[j].GetRegion() {
			return values[i].GetName() < values[j].GetName()
		}
		return values[i].GetRegion() < values[j].GetRegion()
	})
	return values, nil
}

func (store *Store) GetAuditorium(_ context.Context, id string) (*catalogpb.Auditorium, error) {
	store.mu.RLock()
	defer store.mu.RUnlock()
	for _, value := range store.catalog.GetAuditoriums() {
		if value.GetId() == id {
			return proto.CloneOf(value), nil
		}
	}
	return nil, application.ErrNotFound
}

func (store *Store) ListAuditoriumsByTheater(_ context.Context, theaterID string) ([]*catalogpb.Auditorium, error) {
	store.mu.RLock()
	defer store.mu.RUnlock()
	values := make([]*catalogpb.Auditorium, 0)
	for _, value := range store.catalog.GetAuditoriums() {
		if value.GetTheaterId() == theaterID {
			values = append(values, proto.CloneOf(value))
		}
	}
	sort.Slice(values, func(i, j int) bool { return values[i].GetName() < values[j].GetName() })
	if len(values) == 0 {
		select {
		case store.scheduleRequests <- theaterID:
		default:
		}
	}
	return values, nil
}

func (store *Store) PutSeatMap(_ context.Context, snapshot *seatmappb.Snapshot) error {
	if snapshot == nil || snapshot.GetAuditoriumId() == "" {
		return errors.New("seat map is required")
	}
	store.mu.Lock()
	defer store.mu.Unlock()
	path := recordPath(store.root, "seat-maps", snapshot.GetAuditoriumId())
	if err := writeProtoAtomic(path, snapshot); err != nil {
		return err
	}
	store.seatMaps[snapshot.GetAuditoriumId()] = proto.CloneOf(snapshot)
	select {
	case store.seatChanged <- struct{}{}:
	default:
	}
	store.signalChanged()
	return nil
}

func (store *Store) GetSeatMap(_ context.Context, auditoriumID string) (*seatmappb.Snapshot, error) {
	store.mu.RLock()
	defer store.mu.RUnlock()
	value := store.seatMaps[auditoriumID]
	if value == nil {
		return nil, application.ErrNotFound
	}
	return proto.CloneOf(value), nil
}

func (store *Store) ResolveSeatMap(ctx context.Context, auditoriumID string) (*seatmappb.Resolution, error) {
	value, err := store.GetSeatMap(ctx, auditoriumID)
	if err == nil {
		return seatmappb.Resolution_builder{
			Snapshot: value,
			State:    collectionpb.State_builder{Idle: collectionpb.Idle_builder{}.Build()}.Build(),
		}.Build(), nil
	}
	if !errors.Is(err, application.ErrNotFound) {
		return nil, err
	}
	select {
	case store.seatRequests <- auditoriumID:
	default:
	}
	return seatmappb.Resolution_builder{State: collectionpb.State_builder{
		Queued: collectionpb.Queued_builder{
			QueuedAt: timestamppb.Now(),
			Trigger:  collectionpb.Trigger_builder{ClientRequest: collectionpb.ClientRequest_builder{}.Build()}.Build(),
		}.Build(),
	}.Build()}.Build(), nil
}

func (store *Store) WatchSeatMap(ctx context.Context, auditoriumID string, consume func(*seatmappb.Resolution) error) error {
	for {
		resolution, err := store.ResolveSeatMap(ctx, auditoriumID)
		if err != nil {
			return err
		}
		if err := consume(resolution); err != nil {
			return err
		}
		if resolution.GetSnapshot() != nil {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-store.seatChanged:
		}
	}
}

func (store *Store) SubmitLiveSeatObservation(
	ctx context.Context,
	observation *seatmappb.LiveSeatObservation,
) (*seatmappb.Snapshot, error) {
	snapshot := observation.GetLayout()
	if snapshot == nil {
		return nil, errors.New("live seat layout is required")
	}
	if err := store.PutSeatMap(ctx, snapshot); err != nil {
		return nil, err
	}
	return proto.CloneOf(snapshot), nil
}

func (store *Store) PutPreset(ctx context.Context, value *clientpb.Resource) error {
	return store.put(ctx, "presets", value)
}
func (store *Store) GetPreset(ctx context.Context, id string) (*clientpb.Resource, error) {
	return store.get(ctx, "presets", id)
}
func (store *Store) ListPresetsByUser(ctx context.Context, userID string) ([]*clientpb.Resource, error) {
	return store.list(ctx, "presets", userID)
}
func (store *Store) DeletePreset(ctx context.Context, id string) error {
	return store.delete(ctx, "presets", id)
}
func (store *Store) PutMonitor(ctx context.Context, value *clientpb.Resource) error {
	return store.put(ctx, "monitors", value)
}
func (store *Store) GetMonitor(ctx context.Context, id string) (*clientpb.Resource, error) {
	return store.get(ctx, "monitors", id)
}
func (store *Store) ListMonitorsByUser(ctx context.Context, userID string) ([]*clientpb.Resource, error) {
	return store.list(ctx, "monitors", userID)
}
func (store *Store) DeleteMonitor(ctx context.Context, id string) error {
	return store.delete(ctx, "monitors", id)
}
func (store *Store) PutReservation(ctx context.Context, value *clientpb.Resource) error {
	return store.put(ctx, "reservations", value)
}
func (store *Store) GetReservation(ctx context.Context, id string) (*clientpb.Resource, error) {
	return store.get(ctx, "reservations", id)
}
func (store *Store) ListReservationsByUser(ctx context.Context, userID string) ([]*clientpb.Resource, error) {
	return store.list(ctx, "reservations", userID)
}
func (store *Store) PutExternalOperation(ctx context.Context, value *clientpb.Resource) error {
	return store.put(ctx, "external-operations", value)
}
func (store *Store) PutAppEvent(ctx context.Context, value *clientpb.Resource) error {
	return store.put(ctx, "app-events", value)
}
func (store *Store) ListAppEvents(ctx context.Context, userID string, limit int) ([]*clientpb.Resource, error) {
	values, err := store.list(ctx, "app-events", userID)
	if limit > 0 && len(values) > limit {
		values = values[:limit]
	}
	return values, err
}

func (store *Store) MarkAppEventsRead(ctx context.Context, userID string, at time.Time) error {
	values, err := store.ListAppEvents(ctx, userID, 0)
	if err != nil {
		return err
	}
	for _, value := range values {
		if event := value.GetAppEvent(); event != nil && event.GetReadAt() == nil {
			event.SetReadAt(timestamppb.New(at))
			if err := store.PutAppEvent(ctx, value); err != nil {
				return err
			}
		}
	}
	return nil
}

func (store *Store) DeleteAppEvents(ctx context.Context, userID string) error {
	values, err := store.ListAppEvents(ctx, userID, 0)
	if err != nil {
		return err
	}
	for _, value := range values {
		if err := store.delete(ctx, "app-events", value.GetIdentity().GetId()); err != nil {
			return err
		}
	}
	return nil
}

func (store *Store) DeleteAppEventsBefore(ctx context.Context, cutoff time.Time) error {
	values, err := store.ListAppEvents(ctx, localUserID, 0)
	if err != nil {
		return err
	}
	for _, value := range values {
		createdAt := value.GetIdentity().GetCreatedAt()
		if createdAt != nil && createdAt.AsTime().Before(cutoff) {
			if err := store.delete(ctx, "app-events", value.GetIdentity().GetId()); err != nil {
				return err
			}
		}
	}
	return nil
}

func (*Store) RecoverInterruptedWork(context.Context, time.Time) ([]*clientpb.Resource, error) {
	return nil, nil
}

func (store *Store) put(_ context.Context, kind string, destination *clientpb.Resource) error {
	if err := validateResource(kind, destination); err != nil {
		return err
	}
	if owner := resourceOwner(destination); owner != "" && owner != localUserID {
		return application.ErrNotFound
	}
	id := destination.GetIdentity().GetId()
	store.mu.Lock()
	defer store.mu.Unlock()
	current := store.resources[kind][id]
	expected := destination.GetIdentity().GetRevision()
	if current == nil && expected != 0 || current != nil && current.GetIdentity().GetRevision() != expected {
		return application.ErrConflict
	}
	now := timestamppb.Now()
	createdAt := now
	if current != nil && current.GetIdentity().GetCreatedAt() != nil {
		createdAt = current.GetIdentity().GetCreatedAt()
	}
	revision := expected + 1
	value := proto.CloneOf(destination)
	value.GetIdentity().SetRevision(revision)
	value.GetIdentity().SetCreatedAt(createdAt)
	value.GetIdentity().SetUpdatedAt(now)
	path := recordPath(filepath.Join(store.root, "resources"), kind, id)
	if err := writeProtoAtomic(path, value); err != nil {
		return err
	}
	store.resources[kind][id] = value
	proto.Reset(destination)
	proto.Merge(destination, value)
	store.signalChanged()
	return nil
}

func (store *Store) get(_ context.Context, kind, id string) (*clientpb.Resource, error) {
	store.mu.RLock()
	defer store.mu.RUnlock()
	value := store.resources[kind][id]
	if value == nil {
		return nil, application.ErrNotFound
	}
	return proto.CloneOf(value), nil
}

func (store *Store) list(_ context.Context, kind, userID string) ([]*clientpb.Resource, error) {
	if userID != localUserID {
		return nil, application.ErrNotFound
	}
	store.mu.RLock()
	defer store.mu.RUnlock()
	values := make([]*clientpb.Resource, 0, len(store.resources[kind]))
	for _, value := range store.resources[kind] {
		if owner := resourceOwner(value); owner == "" || owner == userID {
			values = append(values, proto.CloneOf(value))
		}
	}
	sort.Slice(values, func(i, j int) bool {
		return resourceCreatedAt(values[i]).After(resourceCreatedAt(values[j]))
	})
	return values, nil
}

func (store *Store) delete(_ context.Context, kind, id string) error {
	store.mu.Lock()
	defer store.mu.Unlock()
	if store.resources[kind][id] == nil {
		return nil
	}
	path := recordPath(filepath.Join(store.root, "resources"), kind, id)
	if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	delete(store.resources[kind], id)
	store.signalChanged()
	return nil
}

func validateResource(kind string, value *clientpb.Resource) error {
	if value == nil || value.GetIdentity() == nil || value.GetIdentity().GetId() == "" {
		return errors.New("local resource identity is required")
	}
	valid := kind == "presets" && value.GetPreset() != nil ||
		kind == "monitors" && value.GetMonitor() != nil ||
		kind == "reservations" && value.GetReservation() != nil ||
		kind == "external-operations" && value.GetExternalOperation() != nil ||
		kind == "app-events" && value.GetAppEvent() != nil
	if !valid {
		return fmt.Errorf("local resource kind %q does not match its body", kind)
	}
	return nil
}

func resourceOwner(value *clientpb.Resource) string {
	switch {
	case value.GetPreset() != nil:
		return value.GetPreset().GetUserId()
	case value.GetMonitor() != nil:
		return value.GetMonitor().GetUserId()
	case value.GetReservation() != nil:
		return value.GetReservation().GetUserId()
	case value.GetExternalOperation() != nil:
		return value.GetExternalOperation().GetUserId()
	case value.GetAppEvent() != nil:
		return value.GetAppEvent().GetUserId()
	default:
		return ""
	}
}

func resourceCreatedAt(value *clientpb.Resource) time.Time {
	if value != nil && value.GetIdentity() != nil && value.GetIdentity().GetCreatedAt() != nil {
		return value.GetIdentity().GetCreatedAt().AsTime()
	}
	return time.Time{}
}

func localDateKey(value *commonpb.LocalDate) string {
	if value == nil {
		return ""
	}
	return fmt.Sprintf("%04d-%02d-%02d", value.GetYear(), value.GetMonth(), value.GetDay())
}

func upsertProvider(index *catalogpb.CatalogIndex, value *catalogpb.Provider) {
	providers := index.GetProviders()
	for position, existing := range providers {
		if existing.GetId() == value.GetId() {
			providers[position] = proto.CloneOf(value)
			index.SetProviders(providers)
			return
		}
	}
	index.SetProviders(append(providers, proto.CloneOf(value)))
}

func upsertTheater(index *catalogpb.CatalogIndex, value *catalogpb.Theater) {
	theaters := index.GetTheaters()
	for position, existing := range theaters {
		if existing.GetId() == value.GetId() {
			theaters[position] = proto.CloneOf(value)
			index.SetTheaters(theaters)
			return
		}
	}
	index.SetTheaters(append(theaters, proto.CloneOf(value)))
}

func upsertMovie(index *catalogpb.CatalogIndex, value *catalogpb.Movie) {
	if value == nil {
		return
	}
	movies := index.GetMovies()
	for position, existing := range movies {
		if existing.GetId() == value.GetId() {
			replacement := proto.CloneOf(value)
			if replacement.GetPosterUrl() == "" && existing.GetPosterUrl() != "" {
				replacement.SetPosterUrl(existing.GetPosterUrl())
			}
			movies[position] = replacement
			index.SetMovies(movies)
			return
		}
	}
	index.SetMovies(append(movies, proto.CloneOf(value)))
}

func attachPosterURLs(index *catalogpb.CatalogIndex, posters map[string]*catalogpb.MoviePoster) bool {
	if index == nil {
		return false
	}
	changed := false
	for _, movie := range index.GetMovies() {
		if movie == nil {
			continue
		}
		expected := localPosterURL(posters[movie.GetId()])
		if expected != "" && movie.GetPosterUrl() != expected {
			movie.SetPosterUrl(expected)
			changed = true
		}
	}
	return changed
}

func localPosterURL(poster *catalogpb.MoviePoster) string {
	if poster == nil {
		return ""
	}
	movieID := strings.TrimSpace(poster.GetMovieId())
	hash := strings.ToLower(strings.TrimSpace(poster.GetContentHash()))
	decoded, err := hex.DecodeString(hash)
	if movieID == "" || err != nil || len(decoded) != sha256.Size {
		return ""
	}
	return "/v1/catalog/posters/" + url.PathEscape(movieID) + "?v=" + hash
}

func upsertAuditorium(index *catalogpb.CatalogIndex, value *catalogpb.Auditorium) {
	auditoriums := index.GetAuditoriums()
	for position, existing := range auditoriums {
		if existing.GetId() == value.GetId() {
			auditoriums[position] = proto.CloneOf(value)
			index.SetAuditoriums(auditoriums)
			return
		}
	}
	index.SetAuditoriums(append(auditoriums, proto.CloneOf(value)))
}

func upsertShowtime(index *catalogpb.CatalogIndex, value *catalogpb.Showtime) {
	showtimes := index.GetShowtimes()
	for position, existing := range showtimes {
		if existing.GetId() == value.GetId() {
			showtimes[position] = proto.CloneOf(value)
			index.SetShowtimes(showtimes)
			return
		}
	}
	index.SetShowtimes(append(showtimes, proto.CloneOf(value)))
}
