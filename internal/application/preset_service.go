package application

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/cineko-org/client/internal/domain"
)

type CreatePresetRequest struct {
	ExpectedRevision int64
	UserID           string
	Name             string
	TheaterID        string
	AuditoriumID     string
	SeatCount        int
	SeatPreference   domain.SeatPreference
}

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

func (service *PresetService) Create(ctx context.Context, request CreatePresetRequest) (domain.Preset, error) {
	if err := service.ensureUniqueName(ctx, request.UserID, request.Name, ""); err != nil {
		return domain.Preset{}, err
	}
	now := service.clock.Now()
	preset := domain.Preset{
		ID: service.ids.NewID(), UserID: request.UserID, CreatedAt: now,
	}
	applyPresetRequest(&preset, request, now)
	return service.validateAndSave(ctx, preset)
}

func (service *PresetService) Update(
	ctx context.Context,
	userID, presetID string,
	request CreatePresetRequest,
) (domain.Preset, error) {
	preset, err := service.presets.GetPreset(ctx, presetID)
	if err != nil {
		return domain.Preset{}, err
	}
	if !preset.Owns(userID) {
		return domain.Preset{}, ErrNotFound
	}
	if request.ExpectedRevision < 1 || preset.Revision != request.ExpectedRevision {
		return domain.Preset{}, ErrConflict
	}
	if uniqueErr := service.ensureUniqueName(ctx, userID, request.Name, presetID); uniqueErr != nil {
		return domain.Preset{}, uniqueErr
	}
	applyPresetRequest(&preset, request, service.clock.Now())
	return service.validateAndSave(ctx, preset)
}

func (service *PresetService) List(ctx context.Context, userID string) ([]domain.Preset, error) {
	return service.presets.ListPresetsByUser(ctx, userID)
}

func (service *PresetService) Delete(ctx context.Context, userID, presetID string, expectedRevision ...int64) error {
	preset, err := service.presets.GetPreset(ctx, presetID)
	if err != nil {
		return err
	}
	if !preset.Owns(userID) {
		return ErrNotFound
	}
	if len(expectedRevision) > 0 && (expectedRevision[0] < 1 || preset.Revision != expectedRevision[0]) {
		return ErrConflict
	}
	return service.presets.DeletePreset(ctx, presetID)
}

func applyPresetRequest(preset *domain.Preset, request CreatePresetRequest, updatedAt time.Time) {
	preset.Name = strings.TrimSpace(request.Name)
	preset.TheaterID = request.TheaterID
	preset.AuditoriumID = request.AuditoriumID
	preset.SeatCount = request.SeatCount
	preset.SeatPreference = request.SeatPreference
	preset.UpdatedAt = updatedAt
}

func (service *PresetService) validateAndSave(ctx context.Context, preset domain.Preset) (domain.Preset, error) {
	if err := preset.Validate(nil); err != nil {
		return domain.Preset{}, err
	}
	if err := service.presets.PutPreset(ctx, preset); err != nil {
		return domain.Preset{}, err
	}
	return preset, nil
}

func (service *PresetService) ensureUniqueName(ctx context.Context, userID, name, exceptID string) error {
	presets, err := service.presets.ListPresetsByUser(ctx, userID)
	if err != nil {
		return err
	}
	normalizedName := strings.TrimSpace(name)
	for _, preset := range presets {
		if preset.ID != exceptID && strings.EqualFold(preset.Name, normalizedName) {
			return fmt.Errorf("%w: preset name already exists", ErrConflict)
		}
	}
	return nil
}
