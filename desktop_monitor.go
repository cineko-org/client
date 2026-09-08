package main

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"time"

	"github.com/cineko-org/client/internal/adapters/cgv"
	"github.com/cineko-org/client/internal/application"
	"github.com/cineko-org/client/internal/logging"
	catalogpb "github.com/cineko-org/contracts/v3/gen/go/cineko/catalog"
	clientpb "github.com/cineko-org/contracts/v3/gen/go/cineko/client"
	commonpb "github.com/cineko-org/contracts/v3/gen/go/cineko/common"
)

const localMonitorTick = time.Second

const (
	localMonitorSignalCancellation = "cancellation_round"
	localMonitorSignalNewSchedule  = "new_schedule"
)

type localMonitorStore interface {
	UserID() string
	GetCatalog(context.Context) (*catalogpb.CatalogIndex, error)
	ListMonitorsByUser(context.Context, string) ([]*clientpb.Resource, error)
	GetMonitor(context.Context, string) (*clientpb.Resource, error)
	GetPreset(context.Context, string) (*clientpb.Resource, error)
}

type localExecutionServer interface {
	CanAcceptExecution() bool
	ExecuteAvailability(context.Context, string, *catalogpb.Showtime, bool) error
	RecordLocalSystemEvent(*clientpb.AppEvent)
}

// desktopMonitorWorker prioritizes newly opened schedules and rotates short
// cancellation-seat rounds across the existing matching showtimes.
type desktopMonitorWorker struct {
	summary         *monitoringSummary
	store           localMonitorStore
	server          localExecutionServer
	scheduleChanged <-chan struct{}

	initializedMonitors map[string]struct{}
	knownTargets        map[string]struct{}
	newTargets          map[string]time.Time
	lastAttempt         map[string]time.Time
}

type localMonitorTarget struct {
	monitorID          string
	showtime           *catalogpb.Showtime
	signal             string
	watchCancellations bool
}

type localMonitorResult struct {
	monitorID  string
	showtimeID string
	signal     string
	err        error
	execution  *localMonitorExecution
}

type localMonitorExecution struct {
	target *localMonitorTarget
	cancel context.CancelFunc
}

type localMonitorInventory struct {
	activeMonitorIDs []string
	targets          []*localMonitorTarget
}

func (worker *desktopMonitorWorker) Run(ctx context.Context) error {
	if worker == nil || worker.store == nil || worker.server == nil {
		return errors.New("local monitor dependencies are incomplete")
	}
	ticker := time.NewTicker(localMonitorTick)
	defer ticker.Stop()
	var executionChanged <-chan struct{}
	if notifier, ok := worker.server.(interface{ ExecutionAvailable() <-chan struct{} }); ok {
		executionChanged = notifier.ExecutionAvailable()
	}
	runtime := &localMonitorRuntime{
		worker: worker,
		active: make(map[string]*localMonitorExecution),
		done:   make(chan localMonitorResult, 256),
	}
	for {
		select {
		case <-ctx.Done():
			runtime.cancelAll(nil)
			return nil
		case result := <-runtime.done:
			runtime.handleResult(ctx, result)
			continue
		case <-ticker.C:
		case <-worker.scheduleChanged:
		case <-executionChanged:
		}
		runtime.refresh(ctx)
	}
}

type localMonitorRuntime struct {
	worker            *desktopMonitorWorker
	active            map[string]*localMonitorExecution
	done              chan localMonitorResult
	waitingForBrowser bool
	retries           map[string]localMonitorRetry
}

type localMonitorRetry struct {
	after    time.Time
	failures int
}

func (runtime *localMonitorRuntime) cancelAll(except *localMonitorExecution) {
	for key, execution := range runtime.active {
		if execution == except {
			continue
		}
		execution.cancel()
		logMonitorCancellation(execution.target, "monitor_stopped_or_other_tab_won")
		delete(runtime.active, key)
	}
}

func (runtime *localMonitorRuntime) handleResult(ctx context.Context, result localMonitorResult) {
	key := result.monitorID + "\x00" + result.showtimeID
	if runtime.active[key] != result.execution {
		return
	}
	result.execution.cancel()
	delete(runtime.active, key)
	if result.err == nil {
		delete(runtime.retries, key)
		logging.Info(ctx, "monitor.execution.completed",
			"event", "monitor.execution.completed", "scenario", "booking_monitoring",
			"operation", "watch_showtime_tab", "outcome", "succeeded",
			"monitor_id", result.monitorID, "showtime_id", result.showtimeID, "signal_kind", result.signal)
		runtime.cancelAll(nil)
		return
	}
	if errors.Is(result.err, context.Canceled) {
		logMonitorCancellation(result.execution.target, "execution_context_canceled")
	} else {
		logLocalMonitorFailure(ctx, result)
		if !result.execution.target.watchCancellations &&
			(errors.Is(result.err, application.ErrSeatUnavailable) || errors.Is(result.err, application.ErrBookingNotOpen)) {
			delete(runtime.retries, key)
			return
		}
		if runtime.retries == nil {
			runtime.retries = make(map[string]localMonitorRetry)
		}
		retry := runtime.retries[key]
		retry.failures++
		delay := min(30*time.Second*time.Duration(1<<min(retry.failures-1, 5)), 15*time.Minute)
		retry.after = time.Now().Add(delay)
		runtime.retries[key] = retry
		// Infrastructure failures must not consume a newly opened schedule.
		// A completed no-seat round remains one-shot when cancellation watching is off.
		if result.signal == localMonitorSignalNewSchedule &&
			!errors.Is(result.err, application.ErrSeatUnavailable) && !errors.Is(result.err, application.ErrBookingNotOpen) {
			runtime.worker.ensureTargetState()
			runtime.worker.newTargets[key] = time.Now()
		}
		logging.Info(ctx, "monitor.execution.retry.scheduled", "event", "monitor.execution.retry.scheduled",
			"scenario", "booking_monitoring", "monitor_id", result.monitorID, "showtime_id", result.showtimeID,
			"retry_at", retry.after, "retry_after_ms", delay.Milliseconds(), "failures", retry.failures)
		if errors.Is(result.err, cgv.ErrAuthenticationRequired) {
			if reporter, ok := runtime.worker.server.(interface{ NotifyBookingPreparationFailed(error, bool) }); ok {
				reporter.NotifyBookingPreparationFailed(result.err, true)
			}
		}
	}
}

func logLocalMonitorFailure(ctx context.Context, result localMonitorResult) {
	if errors.Is(result.err, application.ErrSeatUnavailable) || errors.Is(result.err, application.ErrBookingNotOpen) {
		logging.Info(ctx, "monitor.execution.completed",
			"event", "monitor.execution.completed", "scenario", "booking_monitoring",
			"operation", "watch_showtime_tab", "outcome", "unavailable",
			"monitor_id", result.monitorID, "showtime_id", result.showtimeID,
			"signal_kind", result.signal, "error", fmt.Sprintf("%+v", result.err))
		return
	}
	logging.ErrorUnexpected(ctx, "monitor.execution.failed", "booking_monitoring", "watch_showtime_tab",
		"seat selection reaches payment or reports an unavailable round", "execution failed unexpectedly", result.err,
		"monitor_id", result.monitorID, "showtime_id", result.showtimeID, "signal_kind", result.signal)
}

func (runtime *localMonitorRuntime) refresh(ctx context.Context) {
	inventory, err := runtime.worker.inventory(ctx)
	if err != nil {
		logging.ErrorUnexpected(ctx, "monitor.target.failed", "booking_monitoring", "select_monitor_target",
			"active monitors resolve against the local catalog", "target selection failed", err)
		return
	}
	runtime.worker.observeInventory(ctx, inventory, time.Now())
	runtime.removeStaleExecutions(inventory.targets)
	runtime.startExecutions(ctx, inventory.targets)
}

func (runtime *localMonitorRuntime) removeStaleExecutions(targets []*localMonitorTarget) {
	desired := make(map[string]*localMonitorTarget, len(targets))
	for _, target := range targets {
		if key := localMonitorTargetKey(target); key != "" {
			desired[key] = target
		}
	}
	for key, execution := range runtime.active {
		target, keep := desired[key]
		if keep && (target.watchCancellations || !execution.target.watchCancellations) {
			continue
		}
		execution.cancel()
		logMonitorCancellation(execution.target, "target_removed")
		delete(runtime.active, key)
	}
}

func (runtime *localMonitorRuntime) startExecutions(ctx context.Context, targets []*localMonitorTarget) {
	waiting := false
	for _, target := range targets {
		key := localMonitorTargetKey(target)
		if key == "" || runtime.active[key] != nil || !localMonitorTargetRunnable(target) {
			continue
		}
		if time.Now().Before(runtime.retries[key].after) {
			continue
		}
		if !runtime.worker.server.CanAcceptExecution() {
			waiting = true
			continue
		}
		runtime.startExecution(ctx, key, target)
	}
	if waiting && !runtime.waitingForBrowser {
		logging.WarnUnexpected(ctx, "monitor.execution.waiting", "booking_monitoring", "wait_for_booking_browser",
			"browser capacity available for discovered schedules", "seat selection has not started; browser is not ready",
			"target_count", len(targets))
	} else if !waiting && runtime.waitingForBrowser {
		logging.Info(ctx, "monitor.execution.waiting.ended", "event", "monitor.execution.waiting.ended", "scenario", "booking_monitoring", "outcome", "no_waiting_targets")
	}
	runtime.waitingForBrowser = waiting
}

func logMonitorCancellation(target *localMonitorTarget, reason string) {
	logging.Info(context.Background(), "monitor.execution.completed", "event", "monitor.execution.completed",
		"scenario", "booking_monitoring", "operation", "watch_showtime_tab", "outcome", "canceled",
		"monitor_id", target.monitorID, "showtime_id", target.showtime.GetId(), "signal_kind", target.signal, "reason", reason)
}

func localMonitorTargetRunnable(target *localMonitorTarget) bool {
	return target.watchCancellations || target.signal == localMonitorSignalNewSchedule
}

func (runtime *localMonitorRuntime) startExecution(ctx context.Context, key string, target *localMonitorTarget) {
	runtime.worker.markAttempt(target, time.Now())
	executionContext, cancel := context.WithCancel(ctx)
	execution := &localMonitorExecution{target: target, cancel: cancel}
	runtime.active[key] = execution
	logging.Info(ctx, "monitor.execution.started",
		"event", "monitor.execution.started", "scenario", "booking_monitoring",
		"operation", "watch_showtime_tab", "outcome", "started",
		"monitor_id", target.monitorID, "showtime_id", target.showtime.GetId(), "signal_kind", target.signal,
		"active_executions", len(runtime.active))
	go func() {
		err := runtime.worker.server.ExecuteAvailability(
			executionContext, target.monitorID, target.showtime, target.watchCancellations,
		)
		result := localMonitorResult{
			monitorID: target.monitorID, showtimeID: target.showtime.GetId(), signal: target.signal,
			err: err, execution: execution,
		}
		select {
		case runtime.done <- result:
		case <-ctx.Done():
		}
	}()
}

func (worker *desktopMonitorWorker) inventory(ctx context.Context) (*localMonitorInventory, error) {
	catalog, err := worker.store.GetCatalog(ctx)
	if err != nil {
		return nil, err
	}
	monitors, err := worker.store.ListMonitorsByUser(ctx, worker.store.UserID())
	if err != nil {
		return nil, err
	}
	now := time.Now()
	location, locationErr := time.LoadLocation("Asia/Seoul")
	if locationErr == nil {
		now = now.In(location)
	}
	inventory := &localMonitorInventory{targets: make([]*localMonitorTarget, 0)}
	for _, resource := range monitors {
		monitor := resource.GetMonitor()
		if !localMonitorActive(monitor) {
			continue
		}
		inventory.activeMonitorIDs = append(inventory.activeMonitorIDs, monitor.GetId())
		presetResource, err := worker.store.GetPreset(ctx, monitor.GetPresetId())
		if err != nil {
			logging.ErrorUnexpected(ctx, "monitor.preset.read.failed", "booking_monitoring", "resolve_monitor_preset",
				"active monitor references an existing preset", "preset read failed", err,
				"monitor_id", monitor.GetId(), "preset_id", monitor.GetPresetId())
			continue
		}
		if presetResource.GetPreset() == nil {
			logging.WarnUnexpected(ctx, "monitor.preset.missing", "booking_monitoring", "resolve_monitor_preset",
				"active monitor resource contains a preset", "preset message is absent",
				"monitor_id", monitor.GetId(), "preset_id", monitor.GetPresetId())
			continue
		}
		preset := presetResource.GetPreset()
		for _, showtime := range catalog.GetShowtimes() {
			if localShowtimeMatches(monitor, preset, showtime, now) {
				inventory.targets = append(inventory.targets, &localMonitorTarget{
					monitorID: monitor.GetId(), showtime: showtime, signal: localMonitorSignalCancellation,
					watchCancellations: monitor.GetWatchCancellationSeats(),
				})
			}
		}
	}
	sort.Strings(inventory.activeMonitorIDs)
	sort.Slice(inventory.targets, func(i, j int) bool {
		return localMonitorTargetLess(inventory.targets[i], inventory.targets[j])
	})
	return inventory, nil
}

func (worker *desktopMonitorWorker) observeInventory(
	ctx context.Context,
	inventory *localMonitorInventory,
	discoveredAt time.Time,
) []*localMonitorTarget {
	worker.ensureTargetState()
	if inventory == nil {
		return nil
	}
	wasInitialized := make(map[string]bool, len(inventory.activeMonitorIDs))
	worker.summary.inventory(inventory.activeMonitorIDs, discoveredAt)
	for _, monitorID := range inventory.activeMonitorIDs {
		_, wasInitialized[monitorID] = worker.initializedMonitors[monitorID]
		worker.initializedMonitors[monitorID] = struct{}{}
	}
	newTargets := make([]*localMonitorTarget, 0)
	for _, target := range inventory.targets {
		key := localMonitorTargetKey(target)
		if key == "" {
			continue
		}
		if _, known := worker.knownTargets[key]; known {
			if _, newlyOpened := worker.newTargets[key]; newlyOpened {
				target.signal = localMonitorSignalNewSchedule
				newTargets = append(newTargets, target)
			}
			continue
		}
		worker.knownTargets[key] = struct{}{}
		if !wasInitialized[target.monitorID] {
			continue
		}
		worker.newTargets[key] = discoveredAt
		worker.summary.discovery(target.monitorID)
		target.signal = localMonitorSignalNewSchedule
		newTargets = append(newTargets, target)
		logging.Info(ctx, "monitor.schedule.discovered",
			"event", "monitor.schedule.discovered", "scenario", "booking_monitoring",
			"operation", "detect_new_schedule", "outcome", "discovered",
			"monitor_id", target.monitorID, "showtime_id", target.showtime.GetId(),
			"starts_at", target.showtime.GetStartsAt().AsTime(), "discovered_at", discoveredAt)
	}
	return newTargets
}

func (worker *desktopMonitorWorker) selectTarget(targets []*localMonitorTarget) *localMonitorTarget {
	worker.ensureTargetState()
	candidates := make([]*localMonitorTarget, 0, len(targets))
	for _, target := range targets {
		key := localMonitorTargetKey(target)
		if key == "" {
			continue
		}
		_, newlyOpened := worker.newTargets[key]
		copyTarget := *target
		if newlyOpened {
			copyTarget.signal = localMonitorSignalNewSchedule
		} else {
			copyTarget.signal = localMonitorSignalCancellation
		}
		candidates = append(candidates, &copyTarget)
	}
	if len(candidates) == 0 {
		return nil
	}
	sort.SliceStable(candidates, func(i, j int) bool {
		leftKey, rightKey := localMonitorTargetKey(candidates[i]), localMonitorTargetKey(candidates[j])
		leftNewAt, leftNew := worker.newTargets[leftKey]
		rightNewAt, rightNew := worker.newTargets[rightKey]
		if leftNew != rightNew {
			return leftNew
		}
		if leftNew && !leftNewAt.Equal(rightNewAt) {
			return leftNewAt.Before(rightNewAt)
		}
		leftAttempt, rightAttempt := worker.lastAttempt[leftKey], worker.lastAttempt[rightKey]
		if !leftAttempt.Equal(rightAttempt) {
			if leftAttempt.IsZero() != rightAttempt.IsZero() {
				return leftAttempt.IsZero()
			}
			return leftAttempt.Before(rightAttempt)
		}
		return localMonitorTargetLess(candidates[i], candidates[j])
	})
	return candidates[0]
}

func (worker *desktopMonitorWorker) markAttempt(target *localMonitorTarget, attemptedAt time.Time) {
	worker.ensureTargetState()
	key := localMonitorTargetKey(target)
	if key == "" {
		return
	}
	worker.lastAttempt[key] = attemptedAt
	delete(worker.newTargets, key)
}

func (worker *desktopMonitorWorker) ensureTargetState() {
	if worker.initializedMonitors == nil {
		worker.initializedMonitors = make(map[string]struct{})
	}
	if worker.knownTargets == nil {
		worker.knownTargets = make(map[string]struct{})
	}
	if worker.newTargets == nil {
		worker.newTargets = make(map[string]time.Time)
	}
	if worker.lastAttempt == nil {
		worker.lastAttempt = make(map[string]time.Time)
	}
}

func localMonitorTargetKey(target *localMonitorTarget) string {
	if target == nil || target.showtime == nil || target.monitorID == "" || target.showtime.GetId() == "" {
		return ""
	}
	return target.monitorID + "\x00" + target.showtime.GetId()
}

func localMonitorTargetLess(left, right *localMonitorTarget) bool {
	leftTime, rightTime := left.showtime.GetStartsAt().AsTime(), right.showtime.GetStartsAt().AsTime()
	if !leftTime.Equal(rightTime) {
		return leftTime.Before(rightTime)
	}
	return localMonitorTargetKey(left) < localMonitorTargetKey(right)
}

func localMonitorActive(monitor *clientpb.Monitor) bool {
	if monitor == nil || monitor.GetState() == nil {
		return false
	}
	return monitor.GetState().GetPending() != nil || monitor.GetState().GetRunning() != nil
}

func localShowtimeMatches(
	monitor *clientpb.Monitor,
	preset *clientpb.Preset,
	showtime *catalogpb.Showtime,
	now time.Time,
) bool {
	if !localShowtimeIdentityMatches(monitor, preset, showtime) || !showtime.GetStartsAt().AsTime().After(now) {
		return false
	}
	startsAt := showtime.GetStartsAt().AsTime().In(now.Location())
	if !localMonitorTimeMatches(monitor, startsAt) {
		return false
	}
	weekdays := monitor.GetTargetWeekdays()
	if len(weekdays) > 0 {
		matched := false
		for _, weekday := range weekdays {
			if int64(startsAt.Weekday()) == int64(weekday) {
				matched = true
				break
			}
		}
		if !matched {
			return false
		}
	}
	return true
}

func localShowtimeIdentityMatches(monitor *clientpb.Monitor, preset *clientpb.Preset, showtime *catalogpb.Showtime) bool {
	if monitor == nil || preset == nil || showtime == nil || showtime.GetMovie() == nil ||
		showtime.GetAuditorium() == nil || showtime.GetStartsAt() == nil {
		return false
	}
	return monitor.GetMovieId() == showtime.GetMovie().GetId() &&
		preset.GetTheaterId() == showtime.GetTheaterId() &&
		preset.GetAuditoriumId() == showtime.GetAuditorium().GetId()
}

func localMonitorTimeMatches(monitor *clientpb.Monitor, startsAt time.Time) bool {
	minute := startsAt.Hour()*60 + startsAt.Minute()
	if earliest := localTimeMinute(monitor.GetEarliestTime()); earliest >= 0 && minute < earliest {
		return false
	}
	if latest := localTimeMinute(monitor.GetLatestTime()); latest >= 0 && minute > latest {
		return false
	}
	return true
}

func localTimeMinute(value *commonpb.LocalTime) int {
	if value == nil {
		return -1
	}
	return int(value.GetHour())*60 + int(value.GetMinute())
}
