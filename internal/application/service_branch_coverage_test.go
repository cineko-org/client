package application

import (
	"context"
	"errors"
	"testing"
	"time"

	catalogpb "github.com/cineko-org/contracts/gen/go/cineko/catalog"
	clientpb "github.com/cineko-org/contracts/gen/go/cineko/client"
	seatmappb "github.com/cineko-org/contracts/gen/go/cineko/seatmap"
	"google.golang.org/protobuf/types/known/timestamppb"
)

func TestShowtimeDomainFromProtoHandlesNil(t *testing.T) {
	t.Parallel()
	if got := showtimeDomainFromProto(nil); got.ID != "" {
		t.Fatalf("showtimeDomainFromProto(nil) = %+v", got)
	}
}

func TestBookingWorkerCoversMalformedResourcesAndCompletionFailures(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	worker, monitors, _, _, _, _ := newWorkerCoverageHarness()
	failingMonitors := &runOnceMonitorRepository{monitorRepositoryFake: monitors, failAt: 2}
	worker.monitors = failingMonitors
	if _, err := worker.RunOnce(ctx, "monitor"); !errors.Is(err, errInjected) {
		t.Fatalf("RunOnce(complete persist) = %v", err)
	}

	worker, _, _, _, _, _ = newWorkerCoverageHarness()
	if _, err := worker.RunClaimedShowtime(ctx, &clientpb.Resource{}, &clientpb.Resource{}, nil, nil, nil); err == nil {
		t.Fatal("RunClaimedShowtime accepted a malformed monitor resource")
	}
	monitor := validWorkerJob()
	if _, err := worker.RunClaimedShowtime(ctx, resourceForMonitor(monitor, 0), &clientpb.Resource{}, nil, nil, nil); err == nil {
		t.Fatal("RunClaimedShowtime accepted a malformed preset resource")
	}

	malformedMonitors := &malformedMonitorRepository{
		monitorRepositoryFake: &monitorRepositoryFake{},
		resource:              &clientpb.Resource{},
	}
	worker.monitors = malformedMonitors
	if _, err := worker.startMonitor(ctx, "monitor"); err == nil {
		t.Fatal("startMonitor accepted a malformed monitor resource")
	}

	if _, _, _, err := worker.complete(ctx, monitor, nil, time.Now()); err == nil {
		t.Fatal("complete accepted a missing reservation")
	}
	if err := worker.putMonitor(ctx, nil, 0); err == nil {
		t.Fatal("putMonitor accepted a nil monitor")
	}
}

func TestBookingWorkerCoversMalformedPresetAndNilShowtimeOrdering(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	worker, _, presets, _, _, _ := newWorkerCoverageHarness()
	worker.presets = &malformedPresetRepository{
		presetRepositoryFake: presets,
		getResource:          &clientpb.Resource{},
	}
	if _, _, _, err := worker.loadBookingContext(ctx, validWorkerJob()); err == nil {
		t.Fatal("loadBookingContext accepted a malformed preset resource")
	}

	idWithTime, idWithoutTime := "with-time", "without-time"
	startsAt := timestamppb.New(time.Date(2026, time.August, 10, 20, 0, 0, 0, time.UTC))
	withTime := catalogpb.Showtime_builder{Id: &idWithTime, StartsAt: startsAt}.Build()
	withoutTime := catalogpb.Showtime_builder{Id: &idWithoutTime}.Build()
	best, ok := bestShowtime([]*catalogpb.Showtime{withoutTime, withTime})
	if !ok || best.GetId() != idWithTime {
		t.Fatalf("bestShowtime(nil timestamp ordering) = %+v, %t", best, ok)
	}
}

func TestCancellationServiceCoversMissingRequestsAndDraftIdentity(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	now := time.Date(2026, time.August, 9, 10, 0, 0, 0, time.UTC)
	repository := &reservationRepositoryFake{
		reservation: bookedReservationFixtureForTest("reservation", "user", "monitor"),
	}
	gateway := &emptyCancellationGateway{}
	service := NewCancellationService(repository, gateway, fixedClock{now})

	if _, err := service.Cancel(ctx, nil); !errors.Is(err, ErrNotFound) {
		t.Fatalf("Cancel(nil) = %v", err)
	}
	result, err := service.Cancel(ctx, cancellationRequest("user", false))
	if err != nil || result.GetReservationId() != "reservation" {
		t.Fatalf("Cancel(empty identity draft) = %+v, %v", result, err)
	}
	if cloneCancellationResult(nil) == nil {
		t.Fatal("cloneCancellationResult(nil) returned nil")
	}
}

func TestMonitorServiceRejectsMalformedStoredResources(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	now := time.Date(2026, time.August, 9, 10, 0, 0, 0, time.UTC)
	request := monitorMutationForTest(1, "", "monitor", "user", "preset", openingMonitorModeForTest(), "movie", "Movie", []string{"2026-08-10"}, nil, 0, "", "", 5*time.Second, 8*time.Second)
	presets := newPresetRepositoryFake()
	presets.values["preset"] = applicationPreset("user", "auditorium", now)

	malformedMonitors := &malformedMonitorRepository{
		monitorRepositoryFake: &monitorRepositoryFake{},
		resource:              &clientpb.Resource{},
	}
	service := NewMonitorService(malformedMonitors, presets, &sequenceIDs{}, fixedClock{now})
	if _, err := service.Update(ctx, request); !errors.Is(err, ErrNotFound) {
		t.Fatalf("Update(malformed monitor) = %v", err)
	}
	if err := service.Delete(ctx, "user", "monitor"); err == nil {
		t.Fatal("Delete accepted a malformed monitor")
	}

	monitors := &monitorRepositoryFake{job: validWorkerJob(), revision: 1}
	malformedPresets := &malformedPresetRepository{
		presetRepositoryFake: presets,
		getResource:          &clientpb.Resource{},
	}
	service = NewMonitorService(monitors, malformedPresets, &sequenceIDs{}, fixedClock{now})
	if _, err := service.Update(ctx, request); !errors.Is(err, ErrNotFound) {
		t.Fatalf("Update(malformed preset) = %v", err)
	}
	create := monitorMutationForTest(0, "", "", "user", "preset", openingMonitorModeForTest(), "movie", "Movie", []string{"2026-08-10"}, nil, 0, "", "", 5*time.Second, 8*time.Second)
	if _, err := service.Create(ctx, create); !errors.Is(err, ErrNotFound) {
		t.Fatalf("Create(malformed preset) = %v", err)
	}
	create.GetMutation().SetCommandId("command")
	monitors.getErr = ErrNotFound
	if _, err := service.CreateIdempotent(ctx, create); !errors.Is(err, ErrNotFound) {
		t.Fatalf("CreateIdempotent(malformed preset) = %v", err)
	}

	malformedMonitors.resource = &clientpb.Resource{}
	malformedMonitors.getErr = nil
	service = NewMonitorService(malformedMonitors, presets, &sequenceIDs{}, fixedClock{now})
	create.GetMutation().SetCommandId("command")
	if _, err := service.CreateIdempotent(ctx, create); err == nil {
		t.Fatal("CreateIdempotent accepted a malformed existing monitor")
	}
}

func TestPresetServiceRejectsMalformedStoredResources(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	now := time.Date(2026, time.August, 9, 10, 0, 0, 0, time.UTC)
	request := validPresetRequest()
	request.GetMutation().SetExpectedRevision(1)
	request.GetPreset().SetId("preset")
	repository := &malformedPresetRepository{
		presetRepositoryFake: newPresetRepositoryFake(),
		getResource:          &clientpb.Resource{},
	}
	service := NewPresetService(repository, &sequenceIDs{}, fixedClock{now})

	if _, err := service.Update(ctx, request); err == nil {
		t.Fatal("Update accepted a malformed preset resource")
	}
	if err := service.Delete(ctx, "user", "preset"); err == nil {
		t.Fatal("Delete accepted a malformed preset resource")
	}
	if _, err := service.validateAndSave(ctx, &clientpb.Resource{}); err == nil {
		t.Fatal("validateAndSave accepted a malformed preset resource")
	}

	repository.getResource = nil
	repository.listResources = []*clientpb.Resource{{}}
	if err := service.ensureUniqueName(ctx, "user", "name", ""); err == nil {
		t.Fatal("ensureUniqueName accepted a malformed listed preset")
	}
}

type malformedMonitorRepository struct {
	*monitorRepositoryFake
	resource *clientpb.Resource
}

func (repository *malformedMonitorRepository) GetMonitor(context.Context, string) (*clientpb.Resource, error) {
	if repository.getErr != nil {
		return nil, repository.getErr
	}
	return repository.resource, nil
}

func (repository *malformedMonitorRepository) AcquireMonitor(
	context.Context,
	string,
	string,
	time.Time,
	time.Duration,
) (*clientpb.Resource, error) {
	if repository.getErr != nil {
		return nil, repository.getErr
	}
	return repository.resource, nil
}

type malformedPresetRepository struct {
	*presetRepositoryFake
	getResource   *clientpb.Resource
	listResources []*clientpb.Resource
}

func (repository *malformedPresetRepository) GetPreset(context.Context, string) (*clientpb.Resource, error) {
	if repository.getErr != nil {
		return nil, repository.getErr
	}
	if repository.getResource != nil {
		return repository.getResource, nil
	}
	return repository.presetRepositoryFake.GetPreset(context.Background(), "preset")
}

func (repository *malformedPresetRepository) ListPresetsByUser(context.Context, string) ([]*clientpb.Resource, error) {
	if repository.listErr != nil {
		return nil, repository.listErr
	}
	if repository.listResources != nil {
		return repository.listResources, nil
	}
	return nil, nil
}

type emptyCancellationGateway struct{}

func (*emptyCancellationGateway) OpenSeatSelection(
	context.Context,
	*catalogpb.Showtime,
	int,
) (*seatmappb.Snapshot, []*seatmappb.Seat, error) {
	return nil, nil, nil
}

func (*emptyCancellationGateway) PreparePayment(
	context.Context,
	*catalogpb.Showtime,
	[]string,
) (*clientpb.Reservation, error) {
	return nil, nil
}

func (*emptyCancellationGateway) PrepareCancellation(
	context.Context,
	*clientpb.Reservation,
) (*clientpb.WebUICancellationResult, error) {
	return clientpb.WebUICancellationResult_builder{}.Build(), nil
}

func (*emptyCancellationGateway) CommitCancellation(context.Context) error { return nil }
