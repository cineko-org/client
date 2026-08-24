package main

import (
	"context"
	"sync"
	"testing"
	"time"

	catalogpb "github.com/cineko-org/contracts/v3/gen/go/cineko/catalog"
	clientpb "github.com/cineko-org/contracts/v3/gen/go/cineko/client"
	"google.golang.org/protobuf/types/known/timestamppb"
)

func TestMonitorStartsOneWatcherPerMatchingShowtime(t *testing.T) {
	store := newMonitorExecutionStore(true, "showtime-1", "showtime-2")
	scheduleChanged := make(chan struct{}, 1)
	server := &monitorExecutionServer{started: make(chan monitorExecutionCall, 2)}
	worker := &desktopMonitorWorker{store: store, server: server, scheduleChanged: scheduleChanged}
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan error, 1)
	go func() { done <- worker.Run(ctx) }()
	scheduleChanged <- struct{}{}
	want := map[string]bool{"showtime-1": false, "showtime-2": false}
	for range 2 {
		select {
		case call := <-server.started:
			want[call.showtimeID] = call.watchCancellations
		case <-time.After(time.Second):
			t.Fatal("matching showtime tabs did not start concurrently")
		}
	}
	for showtimeID, persistent := range want {
		if !persistent {
			t.Fatalf("showtime %s persistent watch = false", showtimeID)
		}
	}
	cancel()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}

func TestMonitorCancellationToggleKeepsNewScheduleOneShot(t *testing.T) {
	store := newMonitorExecutionStore(false)
	scheduleChanged := make(chan struct{}, 1)
	server := &monitorExecutionServer{started: make(chan monitorExecutionCall, 1)}
	worker := &desktopMonitorWorker{store: store, server: server, scheduleChanged: scheduleChanged}
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- worker.Run(ctx) }()
	scheduleChanged <- struct{}{}
	select {
	case call := <-server.started:
		t.Fatalf("baseline cancellation tab started while disabled: %+v", call)
	case <-time.After(100 * time.Millisecond):
	}
	store.setShowtimes("showtime-new")
	scheduleChanged <- struct{}{}
	select {
	case call := <-server.started:
		if call.showtimeID != "showtime-new" || call.watchCancellations {
			t.Fatalf("new-schedule execution = %+v", call)
		}
	case <-time.After(time.Second):
		t.Fatal("new schedule did not start a one-shot booking attempt")
	}
	cancel()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}

type monitorExecutionStore struct {
	mu      sync.Mutex
	catalog *catalogpb.CatalogIndex
	monitor *clientpb.Resource
	preset  *clientpb.Resource
}

func newMonitorExecutionStore(watchCancellations bool, showtimeIDs ...string) *monitorExecutionStore {
	monitorID, userID, presetID, movieID := "monitor", "user", "preset", "movie"
	theaterID, auditoriumID := "theater", "auditorium"
	monitor := clientpb.Monitor_builder{
		Id: &monitorID, UserId: &userID, PresetId: &presetID, MovieId: &movieID,
		State:                  clientpb.MonitorState_builder{Pending: clientpb.MonitorPending_builder{}.Build()}.Build(),
		WatchCancellationSeats: &watchCancellations,
	}.Build()
	preset := clientpb.Preset_builder{
		Id: &presetID, UserId: &userID, TheaterId: &theaterID, AuditoriumId: &auditoriumID,
	}.Build()
	store := &monitorExecutionStore{
		monitor: clientpb.Resource_builder{Monitor: monitor}.Build(),
		preset:  clientpb.Resource_builder{Preset: preset}.Build(),
	}
	store.setShowtimes(showtimeIDs...)
	return store
}

func (store *monitorExecutionStore) setShowtimes(showtimeIDs ...string) {
	movieID, theaterID, auditoriumID := "movie", "theater", "auditorium"
	movie := catalogpb.Movie_builder{Id: &movieID}.Build()
	auditorium := catalogpb.Auditorium_builder{Id: &auditoriumID, TheaterId: &theaterID}.Build()
	showtimes := make([]*catalogpb.Showtime, 0, len(showtimeIDs))
	for index, showtimeID := range showtimeIDs {
		startsAt := time.Now().Add(time.Duration(index+2) * time.Hour)
		showtimes = append(showtimes, catalogpb.Showtime_builder{
			Id: &showtimeID, Movie: movie, TheaterId: &theaterID, Auditorium: auditorium,
			StartsAt: timestamppb.New(startsAt), EndsAt: timestamppb.New(startsAt.Add(2 * time.Hour)),
		}.Build())
	}
	store.mu.Lock()
	store.catalog = catalogpb.CatalogIndex_builder{Showtimes: showtimes}.Build()
	store.mu.Unlock()
}

func (*monitorExecutionStore) UserID() string { return "user" }
func (store *monitorExecutionStore) GetCatalog(context.Context) (*catalogpb.CatalogIndex, error) {
	store.mu.Lock()
	defer store.mu.Unlock()
	return store.catalog, nil
}
func (store *monitorExecutionStore) ListMonitorsByUser(context.Context, string) ([]*clientpb.Resource, error) {
	return []*clientpb.Resource{store.monitor}, nil
}
func (store *monitorExecutionStore) GetMonitor(context.Context, string) (*clientpb.Resource, error) {
	return store.monitor, nil
}
func (store *monitorExecutionStore) GetPreset(context.Context, string) (*clientpb.Resource, error) {
	return store.preset, nil
}

type monitorExecutionCall struct {
	showtimeID         string
	watchCancellations bool
}

type monitorExecutionServer struct {
	started chan monitorExecutionCall
}

func (*monitorExecutionServer) CanAcceptExecution() bool { return true }
func (server *monitorExecutionServer) ExecuteAvailability(
	ctx context.Context,
	_ string,
	showtime *catalogpb.Showtime,
	watchCancellations bool,
) error {
	server.started <- monitorExecutionCall{showtimeID: showtime.GetId(), watchCancellations: watchCancellations}
	<-ctx.Done()
	return ctx.Err()
}
func (*monitorExecutionServer) RecordLocalSystemEvent(*clientpb.AppEvent) {}

func TestMonitorScheduleChangeWakesBeforeFallbackTick(t *testing.T) {
	t.Parallel()
	scheduleChanged := make(chan struct{}, 1)
	store := &monitorWakeStore{catalogReads: make(chan time.Time, 1)}
	worker := &desktopMonitorWorker{
		store: store, server: monitorWakeServer{}, scheduleChanged: scheduleChanged,
	}
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan error, 1)
	go func() { done <- worker.Run(ctx) }()
	startedAt := time.Now()
	scheduleChanged <- struct{}{}
	select {
	case readAt := <-store.catalogReads:
		if elapsed := readAt.Sub(startedAt); elapsed >= localMonitorTick/2 {
			t.Fatalf("schedule event wake took %s", elapsed)
		}
	case <-time.After(localMonitorTick / 2):
		t.Fatal("schedule event did not wake the monitor before fallback tick")
	}
	cancel()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}

func TestMonitorInventoryDistinguishesExistingAndNewSchedules(t *testing.T) {
	t.Parallel()
	worker := &desktopMonitorWorker{}
	now := time.Date(2026, 8, 24, 7, 0, 0, 0, time.UTC)
	existing := testLocalMonitorTarget("showtime-existing", now.Add(time.Hour))

	newTargets := worker.observeInventory(context.Background(), &localMonitorInventory{
		activeMonitorIDs: []string{"monitor-1"}, targets: []*localMonitorTarget{existing},
	}, now)
	if len(newTargets) != 0 {
		t.Fatalf("baseline targets classified as new = %+v", newTargets)
	}
	if selected := worker.selectTarget([]*localMonitorTarget{existing}); selected == nil || selected.signal != localMonitorSignalCancellation {
		t.Fatalf("baseline selection = %+v", selected)
	}

	opened := testLocalMonitorTarget("showtime-new", now.Add(2*time.Hour))
	newTargets = worker.observeInventory(context.Background(), &localMonitorInventory{
		activeMonitorIDs: []string{"monitor-1"}, targets: []*localMonitorTarget{existing, opened},
	}, now.Add(time.Second))
	if len(newTargets) != 1 || newTargets[0].showtime.GetId() != "showtime-new" {
		t.Fatalf("new targets = %+v", newTargets)
	}
	selected := worker.selectTarget([]*localMonitorTarget{existing, opened})
	if selected == nil || selected.showtime.GetId() != "showtime-new" || selected.signal != localMonitorSignalNewSchedule {
		t.Fatalf("new schedule was not prioritized = %+v", selected)
	}
}

func TestMonitorWithEmptyBaselineRecognizesFirstOpenedSchedule(t *testing.T) {
	t.Parallel()
	worker := &desktopMonitorWorker{}
	now := time.Date(2026, 8, 24, 7, 0, 0, 0, time.UTC)
	worker.observeInventory(context.Background(), &localMonitorInventory{
		activeMonitorIDs: []string{"monitor-1"},
	}, now)
	opened := testLocalMonitorTarget("showtime-new", now.Add(time.Hour))
	newTargets := worker.observeInventory(context.Background(), &localMonitorInventory{
		activeMonitorIDs: []string{"monitor-1"}, targets: []*localMonitorTarget{opened},
	}, now.Add(time.Second))
	if len(newTargets) != 1 || newTargets[0].signal != localMonitorSignalNewSchedule {
		t.Fatalf("first opened schedule = %+v", newTargets)
	}
}

func TestMonitorCancellationTargetsRotateByLastAttempt(t *testing.T) {
	t.Parallel()
	worker := &desktopMonitorWorker{}
	now := time.Date(2026, 8, 24, 7, 0, 0, 0, time.UTC)
	first := testLocalMonitorTarget("showtime-1", now.Add(time.Hour))
	second := testLocalMonitorTarget("showtime-2", now.Add(2*time.Hour))
	targets := []*localMonitorTarget{first, second}

	selected := worker.selectTarget(targets)
	if selected == nil || selected.showtime.GetId() != "showtime-1" {
		t.Fatalf("first selection = %+v", selected)
	}
	worker.markAttempt(selected, now)
	selected = worker.selectTarget(targets)
	if selected == nil || selected.showtime.GetId() != "showtime-2" {
		t.Fatalf("second selection = %+v", selected)
	}
	worker.markAttempt(selected, now.Add(time.Second))
	selected = worker.selectTarget(targets)
	if selected == nil || selected.showtime.GetId() != "showtime-1" {
		t.Fatalf("rotated selection = %+v", selected)
	}
}

func testLocalMonitorTarget(showtimeID string, startsAt time.Time) *localMonitorTarget {
	showtime := &catalogpb.Showtime{}
	showtime.SetId(showtimeID)
	showtime.SetStartsAt(timestamppb.New(startsAt))
	return &localMonitorTarget{monitorID: "monitor-1", showtime: showtime}
}

type monitorWakeStore struct {
	catalogReads chan time.Time
}

func (store *monitorWakeStore) UserID() string { return "user" }

func (store *monitorWakeStore) GetCatalog(context.Context) (*catalogpb.CatalogIndex, error) {
	select {
	case store.catalogReads <- time.Now():
	default:
	}
	return &catalogpb.CatalogIndex{}, nil
}

func (*monitorWakeStore) ListMonitorsByUser(context.Context, string) ([]*clientpb.Resource, error) {
	return nil, nil
}

func (*monitorWakeStore) GetMonitor(context.Context, string) (*clientpb.Resource, error) {
	return &clientpb.Resource{}, nil
}

func (*monitorWakeStore) GetPreset(context.Context, string) (*clientpb.Resource, error) {
	return &clientpb.Resource{}, nil
}

type monitorWakeServer struct{}

func (monitorWakeServer) CanAcceptExecution() bool { return false }

func (monitorWakeServer) ExecuteAvailability(context.Context, string, *catalogpb.Showtime, bool) error {
	return nil
}

func (monitorWakeServer) RecordLocalSystemEvent(*clientpb.AppEvent) {}
