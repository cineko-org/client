package application

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/cineko-org/client/internal/domain"
)

func TestMonitorServiceRejectsStaleUpdateAndDelete(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	now := time.Date(2026, time.August, 9, 10, 0, 0, 0, time.UTC)
	presets := newPresetRepositoryFake()
	presets.values["preset"] = applicationPreset("user", "auditorium", now)
	monitors := &monitorRepositoryFake{job: domain.MonitorJob{
		ID: "monitor", UserID: "user", PresetID: "preset", Revision: 2,
		Status: domain.MonitorPending, CreatedAt: now, UpdatedAt: now,
	}}
	service := NewMonitorService(monitors, presets, &sequenceIDs{}, fixedClock{now})
	request := UpdateMonitorRequest{
		ID: "monitor",
		CreateMonitorRequest: CreateMonitorRequest{
			ExpectedRevision: 1,
			UserID:           "user",
			PresetID:         "preset",
			MovieID:          "movie_1",
			Movie:            "Movie",
			TargetDates:      []string{"2026-08-10"},
			PollInterval:     5 * time.Second,
			PollIntervalMax:  8 * time.Second,
		},
	}

	if _, err := service.Update(ctx, request); !errors.Is(err, ErrConflict) {
		t.Fatalf("Update(stale revision) error = %v", err)
	}
	if err := service.Delete(ctx, "user", "monitor", 1); !errors.Is(err, ErrConflict) {
		t.Fatalf("Delete(stale revision) error = %v", err)
	}
}

func TestPresetServiceRejectsStaleAndConflictingUpdatesAndDeletes(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	now := time.Date(2026, time.August, 9, 10, 0, 0, 0, time.UTC)
	repository := newPresetRepositoryFake()
	repository.values["preset"] = applicationPreset("user", "auditorium", now)
	preset := repository.values["preset"]
	preset.Revision = 2
	repository.values["preset"] = preset
	service := NewPresetService(repository, &sequenceIDs{}, fixedClock{now})
	request := validPresetRequest()
	request.ExpectedRevision = 1

	if _, err := service.Update(ctx, "user", "preset", request); !errors.Is(err, ErrConflict) {
		t.Fatalf("Update(stale revision) error = %v", err)
	}
	if err := service.Delete(ctx, "user", "preset", 1); !errors.Is(err, ErrConflict) {
		t.Fatalf("Delete(stale revision) error = %v", err)
	}

	request.ExpectedRevision = 2
	repository.values["duplicate"] = domain.Preset{
		ID: "duplicate", UserID: "user", Name: "center",
	}
	if _, err := service.Update(ctx, "user", "preset", request); !errors.Is(err, ErrConflict) {
		t.Fatalf("Update(duplicate name) error = %v", err)
	}
}
