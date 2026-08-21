package application

import (
	"context"
	"errors"
	"testing"
	"time"

	clientpb "github.com/cineko-org/contracts/gen/go/cineko/client"
	commonpb "github.com/cineko-org/contracts/gen/go/cineko/common"
	"google.golang.org/protobuf/types/known/durationpb"
)

func TestApplicationServicesRejectMissingProtoMutations(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	monitorService := NewMonitorService(nil, nil, nil, nil)
	if _, err := monitorService.Update(ctx, nil); err == nil {
		t.Fatal("MonitorService.Update(nil) succeeded")
	}
	if _, err := monitorService.Create(ctx, nil); err == nil {
		t.Fatal("MonitorService.Create(nil) succeeded")
	}
	if _, err := monitorService.CreateIdempotent(ctx, nil); err == nil {
		t.Fatal("MonitorService.CreateIdempotent(nil) succeeded")
	}
	presetService := NewPresetService(nil, nil, nil)
	if _, err := presetService.Update(ctx, nil); err == nil {
		t.Fatal("PresetService.Update(nil) succeeded")
	}
}

func TestPresetServiceValidatesGeneratedProto(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	now := time.Date(2026, time.August, 9, 10, 0, 0, 0, time.UTC)
	repository := newPresetRepositoryFake()
	repository.values["preset"] = presetResourceFixture(presetFixtureForTest("preset", "other", "theater", "auditorium", []string{"A1"}), 1)
	service := NewPresetService(repository, &sequenceIDs{}, fixedClock{now})
	mutation := presetMutationForTest(1, "preset", "user", "center", "theater", "auditorium", 1, clientpb.SeatPreference_builder{Together: boolPointer(true)}.Build())
	if _, err := service.Update(ctx, mutation); !errors.Is(err, ErrNotFound) {
		t.Fatalf("Update() ownership error = %v, want %v", err, ErrNotFound)
	}
	invalid := presetMutationForTest(0, "", "user", "", "theater", "auditorium", 1, clientpb.SeatPreference_builder{Together: boolPointer(true)}.Build())
	if _, err := service.Create(ctx, invalid); err == nil {
		t.Fatal("Create() accepted a Proto mutation with an empty preset name")
	}
}

func TestGeneratedProtoStateHelpers(t *testing.T) {
	t.Parallel()
	id, userID, presetID, movieID, title := "monitor", "user", "preset", "movie", "Movie"
	horizon := int32(14)
	year, month, day := int32(2026), int32(8), int32(10)
	hour, minute := int32(18), int32(30)
	message := clientpb.Monitor_builder{
		Id: &id, UserId: &userID, PresetId: &presetID,
		Mode:    clientpb.MonitorMode_builder{Opening: clientpb.OpeningMonitor_builder{}.Build()}.Build(),
		MovieId: &movieID, MovieTitle: &title,
		TargetDates:       []*commonpb.LocalDate{commonpb.LocalDate_builder{Year: &year, Month: &month, Day: &day}.Build()},
		SearchHorizonDays: &horizon, EarliestTime: commonpb.LocalTime_builder{Hour: &hour, Minute: &minute}.Build(),
		PollInterval: durationpb.New(5 * time.Second), MaximumPollInterval: durationpb.New(7 * time.Second),
	}.Build()
	revision := int64(2)
	mutation := clientpb.WebUIResourceMutation_builder{
		Mutation: commonpb.MutationIdentity_builder{ExpectedRevision: &revision}.Build(), Monitor: message,
	}.Build()
	got, actualRevision, err := monitorMutationMessage(mutation)
	if err != nil || got != message || actualRevision != 2 {
		t.Fatalf("monitorMutationMessage() = %v, %d, %v", got, actualRevision, err)
	}
	if got := monitorPollInterval(message); got != 5*time.Second {
		t.Fatalf("monitorPollInterval() = %v", got)
	}
	if got := monitorPollIntervalMax(message); got != 7*time.Second {
		t.Fatalf("monitorPollIntervalMax() = %v", got)
	}
	if got := localTimeValue(message.GetEarliestTime()); got != "18:30" {
		t.Fatalf("localTimeValue() = %q", got)
	}
}
