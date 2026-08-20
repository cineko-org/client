package application

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/cineko-org/client/internal/domain"
)

type CreateMonitorRequest struct {
	ExpectedRevision  int64
	UserID            string
	PresetID          string
	Mode              domain.MonitorMode
	MovieID           string
	Movie             string
	TargetDates       []string
	TargetWeekdays    []int
	SearchHorizonDays int
	EarliestTime      string
	LatestTime        string
	PollInterval      time.Duration
	PollIntervalMax   time.Duration
}

type UpdateMonitorRequest struct {
	ID string
	CreateMonitorRequest
}

func (service *MonitorService) Update(ctx context.Context, request UpdateMonitorRequest) (domain.MonitorJob, error) {
	job, err := service.monitors.GetMonitor(ctx, request.ID)
	if err != nil || job.UserID != request.UserID {
		return domain.MonitorJob{}, ErrNotFound
	}
	if request.ExpectedRevision < 1 || job.Revision != request.ExpectedRevision {
		return domain.MonitorJob{}, ErrConflict
	}
	if job.Status == domain.MonitorRunning || job.Status == domain.MonitorTriggered || job.Status == domain.MonitorPaymentUnknown {
		return domain.MonitorJob{}, fmt.Errorf("%w: active monitor cannot be edited", ErrConflict)
	}
	preset, err := service.presets.GetPreset(ctx, request.PresetID)
	if err != nil || !preset.Owns(request.UserID) {
		return domain.MonitorJob{}, ErrNotFound
	}
	updated := service.newMonitor(request.CreateMonitorRequest)
	updated.ID = job.ID
	updated.Revision = job.Revision
	updated.CreatedAt = job.CreatedAt
	if err := updated.Validate(); err != nil {
		return domain.MonitorJob{}, err
	}
	if updated.Expired(service.clock.Now()) {
		return domain.MonitorJob{}, ErrMonitorExpired
	}
	if err := service.monitors.PutMonitor(ctx, updated); err != nil {
		return domain.MonitorJob{}, err
	}
	return updated, nil
}

type MonitorService struct {
	monitors MonitorRepository
	presets  PresetRepository
	ids      IDGenerator
	clock    Clock
}

func NewMonitorService(
	monitors MonitorRepository,
	presets PresetRepository,
	ids IDGenerator,
	clock Clock,
) *MonitorService {
	return &MonitorService{monitors: monitors, presets: presets, ids: ids, clock: clock}
}

func (service *MonitorService) Create(
	ctx context.Context,
	request CreateMonitorRequest,
) (domain.MonitorJob, error) {
	preset, err := service.presets.GetPreset(ctx, request.PresetID)
	if err != nil || !preset.Owns(request.UserID) {
		return domain.MonitorJob{}, ErrNotFound
	}
	job := service.newMonitor(request)
	if err := job.Validate(); err != nil {
		return domain.MonitorJob{}, err
	}
	if job.Expired(service.clock.Now()) {
		return domain.MonitorJob{}, ErrMonitorExpired
	}
	if err := service.monitors.PutMonitor(ctx, job); err != nil {
		return domain.MonitorJob{}, err
	}
	return job, nil
}

// CreateIdempotent uses the caller's stable command key as the monitor ID.
// Retrying a request after an uncertain HTTP response returns the same monitor
// instead of creating another one.
func (service *MonitorService) CreateIdempotent(
	ctx context.Context,
	commandID string,
	request CreateMonitorRequest,
) (domain.MonitorJob, error) {
	commandID = strings.TrimSpace(commandID)
	if commandID == "" {
		return domain.MonitorJob{}, errors.New("idempotency key is required")
	}
	existing, err := service.monitors.GetMonitor(ctx, commandID)
	if err == nil {
		if existing.UserID != request.UserID {
			return domain.MonitorJob{}, ErrNotFound
		}
		return existing, nil
	}
	if !errors.Is(err, ErrNotFound) {
		return domain.MonitorJob{}, err
	}
	preset, err := service.presets.GetPreset(ctx, request.PresetID)
	if err != nil || !preset.Owns(request.UserID) {
		return domain.MonitorJob{}, ErrNotFound
	}
	job := service.newMonitor(request)
	job.ID = commandID
	if err := job.Validate(); err != nil {
		return domain.MonitorJob{}, err
	}
	if job.Expired(service.clock.Now()) {
		return domain.MonitorJob{}, ErrMonitorExpired
	}
	if err := service.monitors.PutMonitor(ctx, job); err != nil {
		return domain.MonitorJob{}, err
	}
	return job, nil
}

func (service *MonitorService) List(ctx context.Context, userID string) ([]domain.MonitorJob, error) {
	return service.monitors.ListMonitorsByUser(ctx, userID)
}

func (service *MonitorService) Delete(ctx context.Context, userID, monitorID string, expectedRevision ...int64) error {
	job, err := service.monitors.GetMonitor(ctx, monitorID)
	if err != nil {
		return err
	}
	if job.UserID != userID {
		return ErrNotFound
	}
	if len(expectedRevision) > 0 && (expectedRevision[0] < 1 || job.Revision != expectedRevision[0]) {
		return ErrConflict
	}
	if job.Status == domain.MonitorRunning {
		return fmt.Errorf("%w: running monitor cannot be deleted", ErrConflict)
	}
	return service.monitors.DeleteMonitor(ctx, monitorID)
}

func (service *MonitorService) newMonitor(request CreateMonitorRequest) domain.MonitorJob {
	now := service.clock.Now()
	return domain.MonitorJob{
		ID:                service.ids.NewID(),
		UserID:            request.UserID,
		PresetID:          request.PresetID,
		Mode:              monitorModeOrDefault(request.Mode),
		MovieID:           strings.TrimSpace(request.MovieID),
		Movie:             strings.TrimSpace(request.Movie),
		TargetDates:       append([]string(nil), request.TargetDates...),
		TargetWeekdays:    append([]int(nil), request.TargetWeekdays...),
		SearchHorizonDays: monitorHorizon(request.TargetWeekdays, request.SearchHorizonDays),
		EarliestTime:      request.EarliestTime,
		LatestTime:        request.LatestTime,
		PollInterval:      pollIntervalOrDefault(request.PollInterval),
		PollIntervalMax:   pollIntervalMaxOrDefault(request.PollInterval, request.PollIntervalMax),
		Status:            domain.MonitorPending,
		CreatedAt:         now,
		UpdatedAt:         now,
	}
}

func monitorModeOrDefault(mode domain.MonitorMode) domain.MonitorMode {
	if mode == "" {
		return domain.MonitorModeOpening
	}
	return mode
}

func monitorHorizon(weekdays []int, horizon int) int {
	if len(weekdays) > 0 && horizon == 0 {
		return domain.DefaultSearchHorizonDays
	}
	return horizon
}

func pollIntervalOrDefault(interval time.Duration) time.Duration {
	if interval == 0 {
		return 3 * time.Minute
	}
	return interval
}

func pollIntervalMaxOrDefault(interval, maximum time.Duration) time.Duration {
	if maximum > 0 {
		return maximum
	}
	if interval > 0 {
		return interval + interval/5
	}
	return 8 * time.Minute
}
