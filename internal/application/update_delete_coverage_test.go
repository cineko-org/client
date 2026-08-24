package application

import (
	"context"
	"errors"
	"testing"
	"time"

	clientpb "github.com/cineko-org/contracts/v3/gen/go/cineko/client"
	commonpb "github.com/cineko-org/contracts/v3/gen/go/cineko/common"
)

func TestMonitorServiceTogglesCancellationWatchWhileActive(t *testing.T) {
	now := time.Date(2026, time.August, 24, 10, 0, 0, 0, time.UTC)
	monitor := monitorFixtureForTest("Movie", []string{"2026-08-25"})
	setMonitorState(monitor, "running", "")
	monitor.SetWatchCancellationSeats(false)
	repository := &monitorRepositoryFake{job: monitor, revision: 3}
	service := NewMonitorService(repository, newPresetRepositoryFake(), &sequenceIDs{}, fixedClock{now})
	updated := cloneMonitor(monitor)
	updated.SetWatchCancellationSeats(true)
	revision, commandID := int64(3), "toggle-cancellation-watch"
	request := clientpb.WebUIResourceMutation_builder{
		Mutation: commonpb.MutationIdentity_builder{CommandId: &commandID, ExpectedRevision: &revision}.Build(),
		Monitor:  updated,
	}.Build()
	resource, err := service.Update(t.Context(), request)
	if err != nil {
		t.Fatal(err)
	}
	if !resource.GetMonitor().GetWatchCancellationSeats() || resource.GetMonitor().GetState().GetRunning() == nil {
		t.Fatalf("active toggle result = %+v", resource.GetMonitor())
	}
	request.GetMonitor().SetMovieTitle("different")
	if _, err := service.Update(t.Context(), request); !errors.Is(err, ErrConflict) {
		t.Fatalf("active non-toggle update error = %v", err)
	}
	request.SetMonitor(cloneMonitor(repository.job))
	request.GetMonitor().SetWatchCancellationSeats(false)
	repository.putErr = errInjected
	if _, err := service.Update(t.Context(), request); !errors.Is(err, errInjected) {
		t.Fatalf("active toggle persistence error = %v", err)
	}
}

func TestMonitorServiceRejectsPresetWithTooFewCandidateSeats(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, time.August, 24, 10, 0, 0, 0, time.UTC)
	presets := newPresetRepositoryFake()
	presets.values["preset"] = applicationPreset("user", "auditorium", now)
	request := monitorMutationForTest(1, "command", "monitor", "user", "preset", "movie_1", "Movie", nil, []int{1}, 0, "", "")
	request.GetMonitor().SetSeatCount(2)

	t.Run("create", func(t *testing.T) {
		service := NewMonitorService(&monitorRepositoryFake{}, presets, &sequenceIDs{}, fixedClock{now})
		if _, err := service.Create(t.Context(), request); err == nil {
			t.Fatal("Create accepted too few candidate seats")
		}
	})

	t.Run("idempotent create", func(t *testing.T) {
		monitors := &monitorRepositoryFake{getErr: ErrNotFound}
		service := NewMonitorService(monitors, presets, &sequenceIDs{}, fixedClock{now})
		if _, err := service.CreateIdempotent(t.Context(), request); err == nil {
			t.Fatal("CreateIdempotent accepted too few candidate seats")
		}
	})

	t.Run("update", func(t *testing.T) {
		current := monitorFixtureForTest("Movie", []string{"2026-08-25"})
		monitors := &monitorRepositoryFake{job: current, revision: 1}
		service := NewMonitorService(monitors, presets, &sequenceIDs{}, fixedClock{now})
		if _, err := service.Update(t.Context(), request); err == nil {
			t.Fatal("Update accepted too few candidate seats")
		}
	})
}

func TestMonitorServiceSetEnabledBoundaries(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, time.August, 24, 10, 0, 0, 0, time.UTC)
	newService := func(state string) (*monitorRepositoryFake, *MonitorService) {
		monitor := monitorFixtureForTest("Movie", []string{"2026-08-25"})
		setMonitorState(monitor, state, "")
		repository := &monitorRepositoryFake{job: monitor, revision: 2}
		return repository, NewMonitorService(repository, newPresetRepositoryFake(), &sequenceIDs{}, fixedClock{now})
	}

	t.Run("lookup", func(t *testing.T) {
		repository, service := newService("pending")
		repository.getErr = errInjected
		if _, err := service.SetEnabled(t.Context(), "user", "monitor", false); !errors.Is(err, errInjected) {
			t.Fatalf("lookup error = %v", err)
		}
	})

	t.Run("owner", func(t *testing.T) {
		_, service := newService("pending")
		if _, err := service.SetEnabled(t.Context(), "other", "monitor", false); !errors.Is(err, ErrNotFound) {
			t.Fatalf("owner error = %v", err)
		}
	})

	for _, state := range []string{"pending", "running"} {
		t.Run("already enabled "+state, func(t *testing.T) {
			_, service := newService(state)
			if _, err := service.SetEnabled(t.Context(), "user", "monitor", true); err != nil {
				t.Fatalf("already enabled error = %v", err)
			}
		})
	}

	t.Run("invalid enable", func(t *testing.T) {
		_, service := newService("booked")
		if _, err := service.SetEnabled(t.Context(), "user", "monitor", true); !errors.Is(err, ErrConflict) {
			t.Fatalf("invalid enable error = %v", err)
		}
	})

	t.Run("already disabled", func(t *testing.T) {
		_, service := newService("stopped")
		if _, err := service.SetEnabled(t.Context(), "user", "monitor", false); err != nil {
			t.Fatalf("already disabled error = %v", err)
		}
	})

	t.Run("invalid disable", func(t *testing.T) {
		_, service := newService("booked")
		if _, err := service.SetEnabled(t.Context(), "user", "monitor", false); !errors.Is(err, ErrConflict) {
			t.Fatalf("invalid disable error = %v", err)
		}
	})

	t.Run("persistence", func(t *testing.T) {
		repository, service := newService("pending")
		repository.putErr = errInjected
		if _, err := service.SetEnabled(t.Context(), "user", "monitor", false); !errors.Is(err, errInjected) {
			t.Fatalf("persistence error = %v", err)
		}
	})
}

func TestValidatePresetMessageAllowsNoSeatPreference(t *testing.T) {
	id, userID, name, theaterID, auditoriumID := "preset", "user", "center", "theater", "auditorium"
	preset := clientpb.Preset_builder{
		Id: &id, UserId: &userID, Name: &name, TheaterId: &theaterID, AuditoriumId: &auditoriumID,
	}.Build()
	if err := validatePresetMessage(preset); err != nil {
		t.Fatalf("preset without a seat preference rejected: %v", err)
	}
}

func TestMonitorServiceRejectsStaleUpdateAndDelete(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	now := time.Date(2026, time.August, 9, 10, 0, 0, 0, time.UTC)
	presets := newPresetRepositoryFake()
	presets.values["preset"] = applicationPreset("user", "auditorium", now)
	monitors := &monitorRepositoryFake{
		job:      monitorFixtureForTest("Movie", []string{"2026-08-10"}),
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
