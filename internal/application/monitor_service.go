package application

import (
	"context"
	"errors"
	"fmt"
	"strings"

	clientpb "github.com/cineko-org/contracts/v3/gen/go/cineko/client"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"
)

func (service *MonitorService) Update(ctx context.Context, mutation *clientpb.WebUIResourceMutation) (*clientpb.Resource, error) {
	request, expectedRevision, err := monitorMutationMessage(mutation)
	if err != nil {
		return nil, err
	}
	resource, err := service.monitors.GetMonitor(ctx, request.GetId())
	if err != nil {
		return nil, ErrNotFound
	}
	job, revision, err := monitorMessage(resource)
	if err != nil || job.GetUserId() != request.GetUserId() {
		return nil, ErrNotFound
	}
	if expectedRevision < 1 || revision != expectedRevision {
		return nil, ErrConflict
	}
	if monitorIsActive(job) {
		return service.updateActiveMonitor(ctx, job, request, revision)
	}
	return service.updateInactiveMonitor(ctx, job, request, revision)
}

func (service *MonitorService) updateActiveMonitor(
	ctx context.Context,
	current, request *clientpb.Monitor,
	revision int64,
) (*clientpb.Resource, error) {
	currentWithoutToggle := cloneMonitor(current)
	requestWithoutToggle := cloneMonitor(request)
	currentWithoutToggle.SetWatchCancellationSeats(false)
	requestWithoutToggle.SetWatchCancellationSeats(false)
	if !proto.Equal(currentWithoutToggle, requestWithoutToggle) {
		return nil, fmt.Errorf("%w: only cancellation-seat watching can change on an active monitor", ErrConflict)
	}
	updated := cloneMonitor(current)
	updated.SetWatchCancellationSeats(request.GetWatchCancellationSeats())
	updated.SetUpdatedAt(timestamppb.New(service.clock.Now()))
	updatedResource := resourceForMonitor(updated, revision)
	if err := service.monitors.PutMonitor(ctx, updatedResource); err != nil {
		return nil, err
	}
	return updatedResource, nil
}

func (service *MonitorService) updateInactiveMonitor(
	ctx context.Context,
	current, request *clientpb.Monitor,
	revision int64,
) (*clientpb.Resource, error) {
	presetResource, err := service.presets.GetPreset(ctx, request.GetPresetId())
	if err != nil {
		return nil, ErrNotFound
	}
	preset, _, err := presetMessage(presetResource)
	if err != nil || preset.GetUserId() != request.GetUserId() {
		return nil, ErrNotFound
	}
	updated := service.newMonitor(request)
	updated.SetId(current.GetId())
	updated.SetCreatedAt(current.GetCreatedAt())
	if err := validateMonitorMessage(updated); err != nil {
		return nil, err
	}
	if err := validateMonitorPreset(updated, preset); err != nil {
		return nil, err
	}
	updatedResource := resourceForMonitor(updated, revision)
	if err := service.monitors.PutMonitor(ctx, updatedResource); err != nil {
		return nil, err
	}
	return updatedResource, nil
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
	mutation *clientpb.WebUIResourceMutation,
) (*clientpb.Resource, error) {
	request, _, err := monitorMutationMessage(mutation)
	if err != nil {
		return nil, err
	}
	presetResource, err := service.presets.GetPreset(ctx, request.GetPresetId())
	if err != nil {
		return nil, ErrNotFound
	}
	preset, _, err := presetMessage(presetResource)
	if err != nil || preset.GetUserId() != request.GetUserId() {
		return nil, ErrNotFound
	}
	job := service.newMonitor(request)
	if err := validateMonitorMessage(job); err != nil {
		return nil, err
	}
	if err := validateMonitorPreset(job, preset); err != nil {
		return nil, err
	}
	resource := resourceForMonitor(job, 0)
	if err := service.monitors.PutMonitor(ctx, resource); err != nil {
		return nil, err
	}
	return resource, nil
}

// CreateIdempotent uses the caller's stable command key as the monitor ID.
// Retrying a request after an uncertain HTTP response returns the same monitor
// instead of creating another one.
func (service *MonitorService) CreateIdempotent(
	ctx context.Context,
	mutation *clientpb.WebUIResourceMutation,
) (*clientpb.Resource, error) {
	request, _, err := monitorMutationMessage(mutation)
	if err != nil {
		return nil, err
	}
	commandID := monitorCommandID(mutation)
	if commandID == "" {
		return nil, errors.New("idempotency key is required")
	}
	existingResource, err := service.monitors.GetMonitor(ctx, commandID)
	if err == nil {
		existing, _, decodeErr := monitorMessage(existingResource)
		if decodeErr != nil {
			return nil, decodeErr
		}
		if existing.GetUserId() != request.GetUserId() {
			return nil, ErrNotFound
		}
		return existingResource, nil
	}
	if !errors.Is(err, ErrNotFound) {
		return nil, err
	}
	presetResource, err := service.presets.GetPreset(ctx, request.GetPresetId())
	if err != nil {
		return nil, ErrNotFound
	}
	preset, _, err := presetMessage(presetResource)
	if err != nil || preset.GetUserId() != request.GetUserId() {
		return nil, ErrNotFound
	}
	job := service.newMonitor(request)
	job.SetId(commandID)
	if err := validateMonitorMessage(job); err != nil {
		return nil, err
	}
	if err := validateMonitorPreset(job, preset); err != nil {
		return nil, err
	}
	resource := resourceForMonitor(job, 0)
	if err := service.monitors.PutMonitor(ctx, resource); err != nil {
		return nil, err
	}
	return resource, nil
}

func (service *MonitorService) List(ctx context.Context, userID string) ([]*clientpb.Resource, error) {
	return service.monitors.ListMonitorsByUser(ctx, userID)
}

// SetEnabled is the explicit user control for local monitoring.
// Disabling preserves the monitor as a manageable stopped resource; enabling
// resumes discovery without directly executing an old showtime command.
func (service *MonitorService) SetEnabled(
	ctx context.Context,
	userID string,
	monitorID string,
	enabled bool,
) (*clientpb.Resource, error) {
	resource, err := service.monitors.GetMonitor(ctx, monitorID)
	if err != nil {
		return nil, err
	}
	monitor, revision, err := monitorMessage(resource)
	if err != nil || monitor.GetUserId() != userID {
		return nil, ErrNotFound
	}
	state := monitorStateName(monitor)
	if enabled {
		if state == "pending" || state == "running" {
			return resource, nil
		}
		if state != "stopped" {
			return nil, fmt.Errorf("%w: monitor cannot be enabled in %s state", ErrConflict, state)
		}
	} else {
		if state == "stopped" {
			return resource, nil
		}
		if state != "pending" && state != "running" {
			return nil, fmt.Errorf("%w: monitor cannot be disabled in %s state", ErrConflict, state)
		}
	}
	updated := cloneMonitor(monitor)
	if enabled {
		setMonitorState(updated, "pending", "")
	} else {
		setMonitorState(updated, "stopped", "user_disabled")
	}
	updated.SetUpdatedAt(timestamppb.New(service.clock.Now()))
	updatedResource := resourceForMonitor(updated, revision)
	if err := service.monitors.PutMonitor(ctx, updatedResource); err != nil {
		return nil, err
	}
	return updatedResource, nil
}

func (service *MonitorService) Delete(ctx context.Context, userID, monitorID string, expectedRevision ...int64) error {
	resource, err := service.monitors.GetMonitor(ctx, monitorID)
	if err != nil {
		return err
	}
	job, revision, err := monitorMessage(resource)
	if err != nil {
		return err
	}
	if job.GetUserId() != userID {
		return ErrNotFound
	}
	if len(expectedRevision) > 0 && (expectedRevision[0] < 1 || revision != expectedRevision[0]) {
		return ErrConflict
	}
	if monitorStateName(job) == "running" {
		return fmt.Errorf("%w: running monitor cannot be deleted", ErrConflict)
	}
	return service.monitors.DeleteMonitor(ctx, monitorID)
}

func (service *MonitorService) newMonitor(request *clientpb.Monitor) *clientpb.Monitor {
	now := service.clock.Now()
	monitor := proto.CloneOf(request)
	monitor.SetId(service.ids.NewID())
	monitor.SetMovieId(strings.TrimSpace(request.GetMovieId()))
	monitor.SetMovieTitle(strings.TrimSpace(request.GetMovieTitle()))
	monitor.SetCreatedAt(timestamppb.New(now))
	monitor.SetUpdatedAt(timestamppb.New(now))
	applyMonitorDefaults(monitor)
	return monitor
}

func validateMonitorPreset(monitor *clientpb.Monitor, preset *clientpb.Preset) error {
	candidates := preset.GetSeatPreference().GetExplicitSeats()
	if len(candidates) > 0 && len(candidates) < int(monitor.GetSeatCount()) {
		return errors.New("preset candidate seats must cover the requested seat count")
	}
	return nil
}

func monitorIsActive(value *clientpb.Monitor) bool {
	switch monitorStateName(value) {
	case "running", "triggered", "payment_unknown":
		return true
	default:
		return false
	}
}
