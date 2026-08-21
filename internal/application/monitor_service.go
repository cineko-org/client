package application

import (
	"context"
	"errors"
	"fmt"
	"strings"

	clientpb "github.com/cineko-org/contracts/gen/go/cineko/client"
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
		return nil, fmt.Errorf("%w: active monitor cannot be edited", ErrConflict)
	}
	presetResource, err := service.presets.GetPreset(ctx, request.GetPresetId())
	if err != nil {
		return nil, ErrNotFound
	}
	preset, _, err := presetMessage(presetResource)
	if err != nil || preset.GetUserId() != request.GetUserId() {
		return nil, ErrNotFound
	}
	updated := service.newMonitor(request)
	updated.SetId(job.GetId())
	updated.SetCreatedAt(job.GetCreatedAt())
	if err := validateMonitorMessage(updated); err != nil {
		return nil, err
	}
	if monitorIsExpired(updated, service.clock.Now()) {
		return nil, ErrMonitorExpired
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
	if monitorIsExpired(job, service.clock.Now()) {
		return nil, ErrMonitorExpired
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
	if monitorIsExpired(job, service.clock.Now()) {
		return nil, ErrMonitorExpired
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

func monitorIsActive(value *clientpb.Monitor) bool {
	switch monitorStateName(value) {
	case "running", "triggered", "payment_unknown":
		return true
	default:
		return false
	}
}
