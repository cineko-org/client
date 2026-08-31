package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/cineko-org/client/internal/interfaces/webui"
	"github.com/cineko-org/client/internal/logging"
	catalogpb "github.com/cineko-org/contracts/v3/gen/go/cineko/catalog"
	clientpb "github.com/cineko-org/contracts/v3/gen/go/cineko/client"
	observationpb "github.com/cineko-org/contracts/v3/gen/go/cineko/observation"
	seatmappb "github.com/cineko-org/contracts/v3/gen/go/cineko/seatmap"
	"github.com/cineko-org/probe/v2/networkcapture"
	"github.com/cineko-org/probe/v2/probe"
)

const (
	localCatalogInterval = 6 * time.Hour
	// Opening detection is a low-frequency schedule scan. Cancellation-seat
	// watching has its own browser-tab loop and must never amplify this into a
	// complete multi-date theater scan every second.
	localScheduleInterval = 30 * time.Second
	localScannerRetry     = 30 * time.Second
	catalogRefreshMarker  = "catalog-refresh"
)

type localScannerStore interface {
	UserID() string
	GetCatalog(context.Context) (*catalogpb.CatalogIndex, error)
	PutCatalogSnapshot(context.Context, *catalogpb.CatalogSnapshot) error
	PutScheduleCaptures(context.Context, *catalogpb.Theater, []*observationpb.Capture) error
	CachedPosterMovieIDs() []string
	ListMonitorsByUser(context.Context, string) ([]*clientpb.Resource, error)
	GetPreset(context.Context, string) (*clientpb.Resource, error)
	GetTheater(context.Context, string) (*catalogpb.Theater, error)
	GetAuditorium(context.Context, string) (*catalogpb.Auditorium, error)
	PutSeatMap(context.Context, *seatmappb.Snapshot) error
	SeatMapRequests() <-chan string
	ScheduleRequests() <-chan string
}

// embeddedProbe is an in-process anonymous scanner. It has no remote identity,
// registration, assignment polling, lease, or result transport.
type embeddedProbe struct {
	scanner         *probe.LocalScanner
	store           localScannerStore
	scheduleChanged chan<- struct{}

	cancel  context.CancelFunc
	done    chan struct{}
	failure chan error

	activityMu         sync.Mutex
	closing            bool
	scanCancel         context.CancelFunc
	shutdownOnce       sync.Once
	shutdownErr        error
	scheduleCursor     map[string]int
	catalogRefreshPath string
	clock              func() time.Time
}

func startEmbeddedProbe(
	parent context.Context,
	store localScannerStore,
	dataDir string,
	scheduleChanged chan<- struct{},
	networkCapture *networkcapture.Store,
) (*embeddedProbe, error) {
	if parent == nil || store == nil {
		return nil, errors.New("embedded scanner dependencies are incomplete")
	}
	scanner, err := probe.NewLocalScanner(probe.LocalScannerConfig{
		DataDir:        filepath.Join(dataDir, "scanner"),
		Logger:         logging.Logger(),
		NetworkCapture: networkCapture,
	})
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithCancel(parent)
	embedded := &embeddedProbe{
		scanner: scanner, store: store, scheduleChanged: scheduleChanged, cancel: cancel,
		done: make(chan struct{}), failure: make(chan error, 1),
		scheduleCursor:     make(map[string]int),
		catalogRefreshPath: filepath.Join(dataDir, "scanner", catalogRefreshMarker),
		clock:              time.Now,
	}
	go embedded.run(ctx)
	return embedded, nil
}

func (embedded *embeddedProbe) run(ctx context.Context) {
	defer close(embedded.done)
	defer func() {
		if recovered := recover(); recovered != nil {
			err := fmt.Errorf("embedded scanner panic: %v", recovered)
			select {
			case embedded.failure <- err:
			default:
			}
		}
	}()
	catalogTimer := time.NewTimer(embedded.initialCatalogDelay(ctx))
	defer catalogTimer.Stop()
	scheduleTicker := time.NewTicker(localScheduleInterval)
	defer scheduleTicker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-catalogTimer.C:
			bootstrapSchedule := embedded.needsYongsanScheduleBootstrap(ctx)
			if err := embedded.captureCatalog(ctx); err != nil {
				embedded.logFailure(ctx, "catalog", err)
				resetTimer(catalogTimer, localScannerRetry)
			} else {
				if err := embedded.recordCatalogRefresh(); err != nil {
					logging.WarnUnexpected(ctx, "scanner.catalog.refresh_marker.failed", "catalog_collection", "record_catalog_refresh",
						"durable catalog refresh timestamp", "timestamp could not be written", "error", err.Error())
				}
				resetTimer(catalogTimer, localCatalogInterval)
				if bootstrapSchedule {
					embedded.bootstrapYongsan(ctx)
				}
			}
		case <-scheduleTicker.C:
			embedded.captureActiveSchedules(ctx)
		case theaterID := <-embedded.store.ScheduleRequests():
			embedded.captureTheaterSchedules(ctx, theaterID)
		case auditoriumID := <-embedded.store.SeatMapRequests():
			embedded.captureSeatMap(ctx, auditoriumID)
		}
	}
}

func (embedded *embeddedProbe) initialCatalogDelay(ctx context.Context) time.Duration {
	now := embedded.clock()
	catalog, err := embedded.store.GetCatalog(ctx)
	if err != nil || catalog.GetGeneration() == 0 || len(catalog.GetMovies()) == 0 || len(catalog.GetTheaters()) == 0 {
		return 0
	}
	contents, err := os.ReadFile(filepath.Clean(embedded.catalogRefreshPath)) // #nosec G304 -- application-owned scanner metadata path.
	if errors.Is(err, os.ErrNotExist) {
		if markerErr := embedded.recordCatalogRefresh(); markerErr != nil {
			logging.WarnUnexpected(ctx, "scanner.catalog.refresh_marker.bootstrap.failed", "catalog_collection", "bootstrap_catalog_refresh_marker",
				"durable catalog refresh timestamp", "timestamp could not be written", "error", markerErr.Error())
		}
		return localCatalogInterval
	}
	if err != nil {
		logging.WarnUnexpected(ctx, "scanner.catalog.refresh_marker.read.failed", "catalog_collection", "read_catalog_refresh_marker",
			"readable catalog refresh timestamp", "timestamp could not be read", "error", err.Error())
		return 0
	}
	lastRefresh, err := time.Parse(time.RFC3339Nano, strings.TrimSpace(string(contents)))
	if err != nil {
		logging.WarnUnexpected(ctx, "scanner.catalog.refresh_marker.invalid", "catalog_collection", "parse_catalog_refresh_marker",
			"RFC3339 catalog refresh timestamp", "timestamp was invalid", "error", err.Error())
		return 0
	}
	remaining := lastRefresh.Add(localCatalogInterval).Sub(now)
	if remaining < 0 {
		return 0
	}
	return remaining
}

func (embedded *embeddedProbe) recordCatalogRefresh() error {
	if err := os.MkdirAll(filepath.Dir(embedded.catalogRefreshPath), 0o700); err != nil {
		return fmt.Errorf("create catalog refresh directory: %w", err)
	}
	if err := os.WriteFile(filepath.Clean(embedded.catalogRefreshPath), []byte(embedded.clock().Format(time.RFC3339Nano)+"\n"), 0o600); err != nil {
		return fmt.Errorf("write catalog refresh marker: %w", err)
	}
	return nil
}

func (embedded *embeddedProbe) needsYongsanScheduleBootstrap(ctx context.Context) bool {
	catalog, err := embedded.store.GetCatalog(ctx)
	if err != nil {
		return true
	}
	yongsanID := ""
	for _, theater := range catalog.GetTheaters() {
		if theater.GetRegion() == "서울" && theater.GetName() == "용산아이파크몰" {
			yongsanID = theater.GetId()
			break
		}
	}
	if yongsanID == "" {
		return true
	}
	for _, showtime := range catalog.GetShowtimes() {
		if showtime.GetTheaterId() == yongsanID {
			return false
		}
	}
	return true
}

func (embedded *embeddedProbe) captureCatalog(ctx context.Context) error {
	return embedded.withScan(ctx, func(scanContext context.Context) error {
		cachedPosterMovieIDs := embedded.store.CachedPosterMovieIDs()
		snapshot, err := embedded.scanner.CaptureCatalog(scanContext, cachedPosterMovieIDs)
		if err != nil {
			return err
		}
		if snapshot == nil || len(snapshot.GetMovies()) == 0 || len(snapshot.GetTheaters()) == 0 {
			return errors.New("catalog capture did not contain movies and theaters")
		}
		expectedPosters, observedPosters := catalogPosterCoverage(snapshot, cachedPosterMovieIDs)
		if observedPosters < expectedPosters {
			logging.WarnUnexpected(scanContext, "scanner.catalog.poster_coverage.unexpected", "poster_collection", "capture_catalog",
				fmt.Sprintf("%d current movies cached or captured", expectedPosters),
				fmt.Sprintf("%d current movie posters available", observedPosters),
				"movie_count", len(snapshot.GetMovies()),
				"captured_poster_count", len(snapshot.GetPosters()),
				"missing_poster_count", expectedPosters-observedPosters,
			)
		}
		if err := embedded.store.PutCatalogSnapshot(scanContext, snapshot); err != nil {
			return err
		}
		logging.Info(scanContext, "scanner.catalog.capture.completed",
			"event", "scanner.catalog.capture.completed",
			"scenario", "catalog_collection",
			"operation", "capture_catalog",
			"outcome", "succeeded",
			"movie_count", len(snapshot.GetMovies()),
			"theater_count", len(snapshot.GetTheaters()),
			"captured_poster_count", len(snapshot.GetPosters()),
		)
		return nil
	})
}

func catalogPosterCoverage(snapshot *catalogpb.CatalogSnapshot, cachedPosterMovieIDs []string) (int, int) {
	if snapshot == nil {
		return 0, 0
	}
	current := make(map[string]struct{}, len(snapshot.GetMovies()))
	for _, movie := range snapshot.GetMovies() {
		if movie != nil && strings.TrimSpace(movie.GetId()) != "" {
			current[movie.GetId()] = struct{}{}
		}
	}
	covered := make(map[string]struct{}, len(current))
	for _, movieID := range cachedPosterMovieIDs {
		if _, exists := current[movieID]; exists {
			covered[movieID] = struct{}{}
		}
	}
	for _, poster := range snapshot.GetPosters() {
		if poster != nil {
			if _, exists := current[poster.GetMovieId()]; exists {
				covered[poster.GetMovieId()] = struct{}{}
			}
		}
	}
	return len(current), len(covered)
}

func (embedded *embeddedProbe) bootstrapYongsan(ctx context.Context) {
	catalog, err := embedded.store.GetCatalog(ctx)
	if err != nil {
		embedded.logFailure(ctx, "bootstrap-yongsan-catalog", err)
		return
	}
	for _, theater := range catalog.GetTheaters() {
		if theater.GetRegion() == "서울" && theater.GetName() == "용산아이파크몰" {
			embedded.captureTheaterSchedules(ctx, theater.GetId())
			return
		}
	}
	logging.WarnUnexpected(ctx, "scanner.bootstrap.yongsan_missing", "catalog_collection", "bootstrap_yongsan_schedule",
		"서울 용산아이파크몰 theater in catalog", "theater not found")
}

func (embedded *embeddedProbe) captureActiveSchedules(ctx context.Context) {
	monitors, err := embedded.store.ListMonitorsByUser(ctx, embedded.store.UserID())
	if err != nil {
		embedded.logFailure(ctx, "list-monitors", err)
		return
	}
	targets := make(map[string]map[int32]struct{})
	for _, resource := range monitors {
		monitor := resource.GetMonitor()
		if monitor == nil || monitor.GetState() == nil ||
			(monitor.GetState().GetPending() == nil && monitor.GetState().GetRunning() == nil) {
			continue
		}
		preset, err := embedded.store.GetPreset(ctx, monitor.GetPresetId())
		if err != nil || preset.GetPreset() == nil {
			if err == nil {
				err = errors.New("monitor preset resource is empty")
			}
			logging.ErrorUnexpected(ctx, "scanner.monitor.preset.failed", "booking_monitoring", "resolve_monitor_preset",
				"active monitor references an existing preset", "preset unavailable", err,
				"monitor_id", monitor.GetId(), "preset_id", monitor.GetPresetId())
			continue
		}
		theaterID := preset.GetPreset().GetTheaterId()
		weekdays := targets[theaterID]
		if weekdays == nil {
			weekdays = make(map[int32]struct{}, 7)
			targets[theaterID] = weekdays
		}
		addMonitorProviderWeekdays(weekdays, monitor.GetTargetWeekdays())
	}
	theaterIDs := make([]string, 0, len(targets))
	for theaterID := range targets {
		theaterIDs = append(theaterIDs, theaterID)
	}
	sort.Strings(theaterIDs)
	for _, theaterID := range theaterIDs {
		weekdays := make([]int32, 0, len(targets[theaterID]))
		for weekday := range targets[theaterID] {
			weekdays = append(weekdays, weekday)
		}
		sort.Slice(weekdays, func(i, j int) bool { return weekdays[i] < weekdays[j] })
		shard := embedded.scheduleCursor[theaterID]
		embedded.captureTheaterScheduleWeekdayShard(ctx, theaterID, weekdays, shard)
		embedded.scheduleCursor[theaterID] = shard + 1
	}
}

func addMonitorProviderWeekdays(result map[int32]struct{}, targetWeekdays []int32) {
	if len(targetWeekdays) == 0 {
		for weekday := int32(time.Sunday); weekday <= int32(time.Saturday); weekday++ {
			result[weekday] = struct{}{}
		}
		return
	}
	for _, weekday := range targetWeekdays {
		result[weekday] = struct{}{}
		// CGV represents after-midnight screenings as 24:xx or later on
		// the preceding provider date, so scan both possible source days.
		result[(weekday+6)%7] = struct{}{}
	}
}

func (embedded *embeddedProbe) captureTheaterSchedules(ctx context.Context, theaterID string) {
	embedded.captureTheaterSchedulesFor(ctx, theaterID, nil, nil)
}

func (embedded *embeddedProbe) captureTheaterScheduleWeekdayShard(ctx context.Context, theaterID string, weekdays []int32, shard int) {
	embedded.captureTheaterSchedulesFor(ctx, theaterID, weekdays, &shard)
}

func (embedded *embeddedProbe) captureTheaterSchedulesFor(ctx context.Context, theaterID string, weekdays []int32, shard *int) {
	theater, err := embedded.store.GetTheater(ctx, theaterID)
	if err != nil {
		embedded.logFailure(ctx, "schedule-theater", err)
		return
	}
	startedAt := time.Now()
	err = embedded.withScan(ctx, func(scanContext context.Context) error {
		var captures []*observationpb.Capture
		var captureErr error
		switch {
		case len(weekdays) > 0 && shard != nil:
			captures, captureErr = embedded.scanner.CaptureScheduleWeekdayShard(scanContext, theater, weekdays, *shard)
		case len(weekdays) > 0:
			captures, captureErr = embedded.scanner.CaptureScheduleWeekdays(scanContext, theater, weekdays)
		default:
			captures, captureErr = embedded.scanner.CaptureSchedules(scanContext, theater)
		}
		if captureErr != nil {
			return captureErr
		}
		complete, showtimes, auditoriums := scheduleCaptureCounts(captures)
		if complete != len(captures) {
			logging.WarnUnexpected(scanContext, "scanner.schedule.partial", "schedule_collection", "capture_theater_schedule",
				fmt.Sprintf("%d complete dates", len(captures)), fmt.Sprintf("%d complete dates", complete),
				"theater_id", theater.GetId(), "capture_count", len(captures), "complete_count", complete)
		}
		if showtimes == 0 || auditoriums == 0 {
			logging.WarnUnexpected(scanContext, "scanner.schedule.empty", "auditorium_collection", "capture_theater_schedule",
				"at least one showtime and auditorium", fmt.Sprintf("%d showtimes and %d auditoriums", showtimes, auditoriums),
				"theater_id", theater.GetId())
		}
		if err := embedded.store.PutScheduleCaptures(scanContext, theater, captures); err != nil {
			return err
		}
		embedded.notifyScheduleChanged()
		logging.Debug(scanContext, "scanner.schedule.capture.completed",
			"event", "scanner.schedule.capture.completed", "scenario", "schedule_collection",
			"operation", "capture_theater_schedule", "outcome", "succeeded",
			"theater_id", theater.GetId(), "capture_count", len(captures),
			"complete_count", complete, "showtime_count", showtimes, "auditorium_count", auditoriums,
			"target_weekdays", weekdays, "duration_ms", time.Since(startedAt).Milliseconds())
		return nil
	})
	if err != nil && !errors.Is(err, context.Canceled) && !errors.Is(err, probe.ErrProviderThrottled) {
		embedded.logFailure(ctx, "schedule", err)
	}
}

func (embedded *embeddedProbe) notifyScheduleChanged() {
	if embedded == nil {
		return
	}
	select {
	case embedded.scheduleChanged <- struct{}{}:
	default:
	}
}

func scheduleCaptureCounts(captures []*observationpb.Capture) (int, int, int) {
	complete, showtimes := 0, 0
	auditoriums := make(map[string]struct{})
	for _, capture := range captures {
		if capture == nil {
			continue
		}
		if capture.GetComplete() {
			complete++
		}
		for _, showtime := range capture.GetShowtimes() {
			if showtime == nil {
				continue
			}
			showtimes++
			if auditorium := showtime.GetAuditorium(); auditorium != nil && strings.TrimSpace(auditorium.GetId()) != "" {
				auditoriums[auditorium.GetId()] = struct{}{}
			}
		}
	}
	return complete, showtimes, len(auditoriums)
}

func (embedded *embeddedProbe) captureSeatMap(ctx context.Context, auditoriumID string) {
	auditorium, err := embedded.store.GetAuditorium(ctx, auditoriumID)
	if err != nil {
		embedded.logFailure(ctx, "seat-map-auditorium", err)
		return
	}
	theater, err := embedded.store.GetTheater(ctx, auditorium.GetTheaterId())
	if err != nil {
		embedded.logFailure(ctx, "seat-map-theater", err)
		return
	}
	err = embedded.withScan(ctx, func(scanContext context.Context) error {
		snapshot, captureErr := embedded.scanner.CaptureSeatMap(scanContext, theater, auditorium)
		if captureErr != nil {
			return captureErr
		}
		if snapshot == nil || snapshot.GetLayout() == nil || len(snapshot.GetLayout().GetSeats()) == 0 {
			return errors.New("seat-map capture contained no seats")
		}
		if err := embedded.store.PutSeatMap(scanContext, snapshot); err != nil {
			return err
		}
		logging.Info(scanContext, "scanner.seat_map.capture.completed",
			"event", "scanner.seat_map.capture.completed", "scenario", "seat_map_collection",
			"operation", "capture_seat_map", "outcome", "succeeded",
			"auditorium_id", auditorium.GetId(), "seat_count", len(snapshot.GetLayout().GetSeats()))
		return nil
	})
	if err != nil && !errors.Is(err, context.Canceled) {
		embedded.logFailure(ctx, "seat-map", err)
	}
}

func (embedded *embeddedProbe) withScan(ctx context.Context, scan func(context.Context) error) error {
	embedded.activityMu.Lock()
	if embedded.closing {
		embedded.activityMu.Unlock()
		return context.Canceled
	}
	scanContext, cancel := context.WithCancel(ctx)
	embedded.scanCancel = cancel
	embedded.activityMu.Unlock()
	defer func() {
		cancel()
		embedded.activityMu.Lock()
		embedded.scanCancel = nil
		embedded.activityMu.Unlock()
	}()
	return scan(scanContext)
}

func resetTimer(timer *time.Timer, delay time.Duration) {
	if !timer.Stop() {
		select {
		case <-timer.C:
		default:
		}
	}
	timer.Reset(delay)
}

func (embedded *embeddedProbe) logFailure(ctx context.Context, operation string, err error) {
	scenario := "catalog_collection"
	switch {
	case strings.Contains(operation, "seat-map"):
		scenario = "seat_map_collection"
	case strings.Contains(operation, "schedule"):
		scenario = "schedule_collection"
	case strings.Contains(operation, "monitor"):
		scenario = "booking_monitoring"
	}
	logging.ErrorUnexpected(ctx, "scanner.operation.failed", scenario, operation,
		"scanner operation completes", "scanner operation failed", err)
}

func (embedded *embeddedProbe) OpenBooking(
	open func() (webui.Automation, error),
) (webui.Automation, error) {
	if open == nil {
		return nil, errors.New("booking browser opener is required")
	}
	embedded.activityMu.Lock()
	closing := embedded.closing
	embedded.activityMu.Unlock()
	if closing {
		return nil, errors.New("embedded scanner is shutting down")
	}
	return open()
}

func (embedded *embeddedProbe) Failure() <-chan error { return embedded.failure }

func (embedded *embeddedProbe) Close() error {
	embedded.shutdownOnce.Do(func() {
		embedded.activityMu.Lock()
		embedded.closing = true
		if embedded.scanCancel != nil {
			embedded.scanCancel()
		}
		embedded.activityMu.Unlock()
		embedded.cancel()
		select {
		case <-embedded.done:
		case <-time.After(10 * time.Second):
			embedded.shutdownErr = errors.New("embedded scanner shutdown timed out")
		}
		embedded.shutdownErr = errors.Join(embedded.shutdownErr, embedded.scanner.Close())
	})
	return embedded.shutdownErr
}

func superviseEmbeddedProbe(ctx context.Context, embedded *embeddedProbe, onFailure func(error)) {
	select {
	case err := <-embedded.Failure():
		if err != nil {
			onFailure(err)
		}
	case <-ctx.Done():
	}
}
