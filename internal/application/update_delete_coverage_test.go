package application

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestMonitorServiceRejectsStaleUpdateAndDelete(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	now := time.Date(2026, time.August, 9, 10, 0, 0, 0, time.UTC)
	presets := newPresetRepositoryFake()
	presets.values["preset"] = applicationPreset("user", "auditorium", now)
	monitors := &monitorRepositoryFake{
		job:      monitorFixtureForTest("preset", "Movie", []string{"2026-08-10"}),
		revision: 2,
	}
	service := NewMonitorService(monitors, presets, &sequenceIDs{}, fixedClock{now})
	request := monitorMutationForTest(1, "", "monitor", "user", "preset", "movie_1", "Movie", []string{"2026-08-10"}, nil, 0, "", "")

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
	preset.GetIdentity().SetRevision(2)
	repository.values["preset"] = preset
	service := NewPresetService(repository, &sequenceIDs{}, fixedClock{now})
	request := validPresetRequest()
	request.GetPreset().SetId("preset")
	request.GetMutation().SetExpectedRevision(1)

	if _, err := service.Update(ctx, request); !errors.Is(err, ErrConflict) {
		t.Fatalf("Update(stale revision) error = %v", err)
	}
	if err := service.Delete(ctx, "user", "preset", 1); !errors.Is(err, ErrConflict) {
		t.Fatalf("Delete(stale revision) error = %v", err)
	}

	request.GetMutation().SetExpectedRevision(2)
	repository.values["duplicate"] = presetResourceFixture(presetFixtureForTest("duplicate", "user", "theater", "auditorium", []string{"A1"}), 0)
	if _, err := service.Update(ctx, request); !errors.Is(err, ErrConflict) {
		t.Fatalf("Update(duplicate name) error = %v", err)
	}
}
