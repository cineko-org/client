package application

import (
	"context"
	"fmt"
	"strings"
	"time"

	clientpb "github.com/cineko-org/contracts/v3/gen/go/cineko/client"
	"google.golang.org/protobuf/types/known/timestamppb"
)

type PresetService struct {
	presets PresetRepository
	ids     IDGenerator
	clock   Clock
}

func NewPresetService(
	presets PresetRepository,
	ids IDGenerator,
	clock Clock,
) *PresetService {
	return &PresetService{presets: presets, ids: ids, clock: clock}
}

func (service *PresetService) Create(ctx context.Context, mutation *clientpb.WebUIResourceMutation) (*clientpb.Resource, error) {
	request, _, err := presetMutationMessage(mutation)
	if err != nil {
		return nil, err
	}
	if err := service.ensureUniqueName(ctx, request.GetUserId(), request.GetName(), ""); err != nil {
		return nil, err
	}
	now := service.clock.Now()
	preset := clonePreset(request)
	preset.SetId(service.ids.NewID())
	preset.SetCreatedAt(timestamppb.New(now))
	preset.SetUpdatedAt(timestamppb.New(now))
	applyPreset(preset, request, now)
	return service.validateAndSave(ctx, resourceForPreset(preset, 0))
}

func (service *PresetService) Update(
	ctx context.Context,
	mutation *clientpb.WebUIResourceMutation,
) (*clientpb.Resource, error) {
	request, expectedRevision, err := presetMutationMessage(mutation)
	if err != nil {
		return nil, err
	}
	userID, presetID := request.GetUserId(), request.GetId()
	resource, err := service.presets.GetPreset(ctx, presetID)
	if err != nil {
		return nil, err
	}
	preset, revision, err := presetMessage(resource)
	if err != nil {
		return nil, err
	}
	if preset.GetUserId() != userID {
		return nil, ErrNotFound
	}
	if expectedRevision < 1 || revision != expectedRevision {
		return nil, ErrConflict
	}
	if uniqueErr := service.ensureUniqueName(ctx, userID, request.GetName(), presetID); uniqueErr != nil {
		return nil, uniqueErr
	}
	updated := clonePreset(preset)
	applyPreset(updated, request, service.clock.Now())
	return service.validateAndSave(ctx, resourceForPreset(updated, revision))
}

func (service *PresetService) List(ctx context.Context, userID string) ([]*clientpb.Resource, error) {
	return service.presets.ListPresetsByUser(ctx, userID)
}

func (service *PresetService) Delete(ctx context.Context, userID, presetID string, expectedRevision ...int64) error {
	resource, err := service.presets.GetPreset(ctx, presetID)
	if err != nil {
		return err
	}
	preset, revision, err := presetMessage(resource)
	if err != nil {
		return err
	}
	if preset.GetUserId() != userID {
		return ErrNotFound
	}
	if len(expectedRevision) > 0 && (expectedRevision[0] < 1 || revision != expectedRevision[0]) {
		return ErrConflict
	}
	return service.presets.DeletePreset(ctx, presetID)
}

func applyPreset(preset, request *clientpb.Preset, updatedAt time.Time) {
	preset.SetName(strings.TrimSpace(request.GetName()))
	preset.SetTheaterId(request.GetTheaterId())
	preset.SetAuditoriumId(request.GetAuditoriumId())
	preset.SetSeatCount(request.GetSeatCount())
	preset.SetSeatPreference(request.GetSeatPreference())
	preset.SetUpdatedAt(timestamppb.New(updatedAt))
}

func (service *PresetService) validateAndSave(ctx context.Context, resource *clientpb.Resource) (*clientpb.Resource, error) {
	preset, _, err := presetMessage(resource)
	if err != nil {
		return nil, err
	}
	if err := validatePresetMessage(preset); err != nil {
		return nil, err
	}
	if err := service.presets.PutPreset(ctx, resource); err != nil {
		return nil, err
	}
	return resource, nil
}

func (service *PresetService) ensureUniqueName(ctx context.Context, userID, name, exceptID string) error {
	resources, err := service.presets.ListPresetsByUser(ctx, userID)
	if err != nil {
		return err
	}
	normalizedName := strings.TrimSpace(name)
	for _, resource := range resources {
		preset, _, decodeErr := presetMessage(resource)
		if decodeErr != nil {
			return decodeErr
		}
		if preset.GetId() != exceptID && strings.EqualFold(preset.GetName(), normalizedName) {
			return fmt.Errorf("%w: preset name already exists", ErrConflict)
		}
	}
	return nil
}
