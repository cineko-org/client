package application

import (
	"bytes"
	"context"
	"errors"
	"testing"
	"time"

	"github.com/cineko-org/client/internal/domain"
	catalogpb "github.com/cineko-org/contracts/v3/gen/go/cineko/catalog"
	clientpb "github.com/cineko-org/contracts/v3/gen/go/cineko/client"
	commonpb "github.com/cineko-org/contracts/v3/gen/go/cineko/common"
	seatmappb "github.com/cineko-org/contracts/v3/gen/go/cineko/seatmap"
)

func TestShowtimeDomainFromProtoHandlesNil(t *testing.T) {
	t.Parallel()
	if got := showtimeDomainFromProto(nil); got.ID != "" {
		t.Fatalf("showtimeDomainFromProto(nil) = %+v", got)
	}
	if got := localDateValue(nil); got != "" {
		t.Fatalf("localDateValue(nil) = %q", got)
	}
	year, month, day := int32(2026), int32(8), int32(22)
	if got := localDateValue(commonLocalDate(year, month, day)); got != "2026-08-22" {
		t.Fatalf("localDateValue() = %q", got)
	}
}

func TestRandomDelayBoundaries(t *testing.T) {
	t.Parallel()

	if got := randomJitter(0); got != 0 {
		t.Fatalf("randomJitter(0) = %s", got)
	}
	if got := randomJitter(time.Nanosecond); got != 0 {
		t.Fatalf("randomJitter(1ns) = %s", got)
	}
	if got := randomJitterFrom(bytes.NewReader(make([]byte, 8)), 10*time.Nanosecond); got != 0 {
		t.Fatalf("randomJitterFrom(zeroes) = %s", got)
	}
	if got := randomJitterFrom(failingRandomReader{}, 10*time.Nanosecond); got != 0 {
		t.Fatalf("randomJitterFrom(error) = %s", got)
	}
	if got := RandomDelayBetween(time.Nanosecond, 2*time.Nanosecond); got != time.Nanosecond {
		t.Fatalf("RandomDelayBetween(1ns, 2ns) = %s", got)
	}
	if got := randomDelayBetween(bytes.NewReader(make([]byte, 8)), time.Second, 2*time.Second); got != time.Second {
		t.Fatalf("randomDelayBetween(zeroes) = %s", got)
	}
	if got := randomDelayBetween(failingRandomReader{}, time.Second, 2*time.Second); got != time.Second {
		t.Fatalf("randomDelayBetween(error) = %s", got)
	}
	if got := randomDelayBetween(bytes.NewReader(nil), 0, time.Second); got != 0 {
		t.Fatalf("randomDelayBetween(invalid) = %s", got)
	}
}

func TestPrepareSeatSelectionFailureBoundaries(t *testing.T) {
	t.Parallel()
	snapshot := gatewaySeatSnapshot()
	available := gatewayAvailableSeats(snapshot, []domain.LiveSeat{{Label: "H10", Available: true}})
	job := monitorFixtureForTest("Movie", []string{"2026-08-22"})
	preset := presetFixtureForTest("preset", "user", "theater", "auditorium-1", []string{"H10"})
	showtime := showtimeProtoFromDomain(domain.Showtime{
		ID: "showtime", ProviderID: "cgv", SourceKey: "source", MovieID: "movie_1", Movie: "Movie",
		TheaterID: "theater", AuditoriumID: "auditorium-1", Date: "2026-08-22", StartsAt: "20:00", EndsAt: "22:00",
		AvailableSeats: 1, Capacity: 1,
	})

	t.Run("prepare payment", func(t *testing.T) {
		gateway := &paymentBoundaryGateway{workerGateway: &workerGateway{}, prepareErr: errInjected}
		worker := NewBookingWorker(BookingWorkerDependencies{
			Reservations: &reservationRepositoryFake{}, Booking: gateway, IDs: &sequenceIDs{},
		})
		if _, err := worker.prepareSeatSelection(t.Context(), job, preset, showtime, snapshot, available); !errors.Is(err, errInjected) {
			t.Fatalf("prepareSeatSelection(payment) = %v", err)
		}
	})

	t.Run("cancelled context", func(t *testing.T) {
		ctx, cancel := context.WithCancel(t.Context())
		gateway := &paymentBoundaryGateway{workerGateway: &workerGateway{}, cancel: cancel}
		worker := NewBookingWorker(BookingWorkerDependencies{
			Reservations: &reservationRepositoryFake{}, Booking: gateway, IDs: &sequenceIDs{},
		})
		if _, err := worker.prepareSeatSelection(ctx, job, preset, showtime, snapshot, available); !errors.Is(err, context.Canceled) {
			t.Fatalf("prepareSeatSelection(cancelled) = %v", err)
		}
	})

	t.Run("reservation persistence", func(t *testing.T) {
		gateway := &paymentBoundaryGateway{workerGateway: &workerGateway{}}
		worker := NewBookingWorker(BookingWorkerDependencies{
			Reservations: &reservationRepositoryFake{putErr: errInjected}, Booking: gateway, IDs: &sequenceIDs{},
		})
		if _, err := worker.prepareSeatSelection(t.Context(), job, preset, showtime, snapshot, available); !errors.Is(err, errInjected) {
			t.Fatalf("prepareSeatSelection(persistence) = %v", err)
		}
	})
}

func TestBookingWorkerCoversMalformedResourcesAndCompletionFailures(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	worker := NewBookingWorker(BookingWorkerDependencies{
		Monitors: &monitorRepositoryFake{}, Reservations: &workerRepository{},
		Booking: &emptyCancellationGateway{}, IDs: &sequenceIDs{},
		Clock: fixedClock{time.Now()}, Waiter: noWaiter{},
	})
	if _, err := worker.RunClaimedShowtime(ctx, &clientpb.Resource{}, &clientpb.Resource{}, nil, nil, nil); err == nil {
		t.Fatal("RunClaimedShowtime accepted a malformed monitor resource")
	}
	monitor := monitorFixtureForTest("Movie", []string{"2026-08-10"})
	if _, err := worker.RunClaimedShowtime(ctx, resourceForMonitor(monitor, 0), &clientpb.Resource{}, nil, nil, nil); err == nil {
		t.Fatal("RunClaimedShowtime accepted a malformed preset resource")
	}
	if _, err := worker.complete(ctx, monitor, nil, time.Now(), 0); err == nil {
		t.Fatal("complete accepted a missing reservation")
	}
	if _, err := worker.putMonitor(ctx, nil, 0); err == nil {
		t.Fatal("putMonitor accepted a nil monitor")
	}
}

func TestCancellationServiceCoversMissingRequestsAndDraftIdentity(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	now := time.Date(2026, time.August, 9, 10, 0, 0, 0, time.UTC)
	repository := &reservationRepositoryFake{
		reservation: bookedReservationFixtureForTest(),
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

func TestCancellationServiceCoversConflictAndRefreshBoundaries(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	now := time.Date(2026, time.August, 22, 10, 0, 0, 0, time.UTC)

	conflicting := bookedReservationFixtureForTest()
	conflicting.SetCancellationCommitting(clientpb.ReservationCancellationCommitting_builder{}.Build())
	service := NewCancellationService(
		&reservationRepositoryFake{reservation: conflicting},
		&bookingGatewayFake{draft: cancellationResultFixtureForTest()}, fixedClock{now},
	)
	if _, err := service.Cancel(ctx, cancellationRequest("user", true)); !errors.Is(err, ErrConflict) {
		t.Fatalf("Cancel(committing) = %v", err)
	}
	if cancellationResultForReservation(nil) == nil {
		t.Fatal("cancellationResultForReservation(nil) returned nil")
	}

	readFailure := &reservationReadRepository{err: errInjected}
	service = NewCancellationService(readFailure, &bookingGatewayFake{}, fixedClock{now})
	if _, _, err := service.currentReservation(ctx, "reservation", "user"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("currentReservation(read failure) = %v", err)
	}
	readFailure.err = nil
	readFailure.resource = &clientpb.Resource{}
	if _, _, err := service.currentReservation(ctx, "reservation", "user"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("currentReservation(malformed) = %v", err)
	}
	readFailure.resource = resourceForReservation(bookedReservationFixtureForTest(), 0)
	if _, _, err := service.currentReservation(ctx, "reservation", "other"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("currentReservation(owner) = %v", err)
	}

	t.Run("cancelled during preparation", func(t *testing.T) {
		reservations := &reservationSequenceRepository{reservation: bookedReservationFixtureForTest()}
		booking := &bookingGatewayFake{draft: cancellationResultFixtureForTest()}
		booking.prepareCancellationHook = func(*clientpb.Reservation) {
			reservations.reservation.SetCancelled(clientpb.ReservationCancelled_builder{}.Build())
		}
		service := NewCancellationService(reservations, booking, fixedClock{now})
		if _, err := service.Cancel(ctx, cancellationRequest("user", true)); err != nil {
			t.Fatalf("Cancel(cancelled race) = %v", err)
		}
	})

	t.Run("conflict during preparation", func(t *testing.T) {
		reservations := &reservationSequenceRepository{reservation: bookedReservationFixtureForTest()}
		booking := &bookingGatewayFake{draft: cancellationResultFixtureForTest()}
		booking.prepareCancellationHook = func(*clientpb.Reservation) {
			reservations.reservation.SetCancellationUnknown(clientpb.ReservationCancellationUnknown_builder{}.Build())
		}
		service := NewCancellationService(reservations, booking, fixedClock{now})
		if _, err := service.Cancel(ctx, cancellationRequest("user", true)); !errors.Is(err, ErrConflict) {
			t.Fatalf("Cancel(conflicting race) = %v", err)
		}
	})

	t.Run("refresh read failure", func(t *testing.T) {
		reservations := &reservationSequenceRepository{reservation: bookedReservationFixtureForTest(), getErrAt: 2}
		service := NewCancellationService(
			reservations, &bookingGatewayFake{draft: cancellationResultFixtureForTest()}, fixedClock{now},
		)
		if _, err := service.Cancel(ctx, cancellationRequest("user", true)); !errors.Is(err, ErrNotFound) {
			t.Fatalf("Cancel(refresh read) = %v", err)
		}
	})

	t.Run("final read failure", func(t *testing.T) {
		reservations := &reservationSequenceRepository{reservation: bookedReservationFixtureForTest(), getErrAt: 3}
		service := NewCancellationService(
			reservations, &bookingGatewayFake{draft: cancellationResultFixtureForTest()}, fixedClock{now},
		)
		if _, err := service.Cancel(ctx, cancellationRequest("user", true)); !errors.Is(err, ErrNotFound) {
			t.Fatalf("Cancel(final read) = %v", err)
		}
	})

	t.Run("state changed after provider confirmation", func(t *testing.T) {
		reservations := &reservationSequenceRepository{reservation: bookedReservationFixtureForTest()}
		booking := &bookingGatewayFake{draft: cancellationResultFixtureForTest()}
		booking.commitCancellationHook = func() {
			reservations.reservation.SetBooked(clientpb.ReservationBooked_builder{}.Build())
		}
		service := NewCancellationService(reservations, booking, fixedClock{now})
		if _, err := service.Cancel(ctx, cancellationRequest("user", true)); !errors.Is(err, ErrConflict) {
			t.Fatalf("Cancel(changed state) = %v", err)
		}
	})
}

func TestMonitorServiceRejectsMalformedStoredResources(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	now := time.Date(2026, time.August, 9, 10, 0, 0, 0, time.UTC)
	request := monitorMutationForTest(1, "", "monitor", "user", "preset", "movie", "Movie", []string{"2026-08-10"}, nil, 0, "", "")
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

	monitors := &monitorRepositoryFake{job: monitorFixtureForTest("Movie", []string{"2026-08-10"}), revision: 1}
	malformedPresets := &malformedPresetRepository{
		presetRepositoryFake: presets,
		getResource:          &clientpb.Resource{},
	}
	service = NewMonitorService(monitors, malformedPresets, &sequenceIDs{}, fixedClock{now})
	if _, err := service.Update(ctx, request); !errors.Is(err, ErrNotFound) {
		t.Fatalf("Update(malformed preset) = %v", err)
	}
	create := monitorMutationForTest(0, "", "", "user", "preset", "movie", "Movie", []string{"2026-08-10"}, nil, 0, "", "")
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

type paymentBoundaryGateway struct {
	*workerGateway
	prepareErr error
	cancel     context.CancelFunc
}

func (gateway *paymentBoundaryGateway) PreparePayment(
	ctx context.Context,
	showtime *catalogpb.Showtime,
	seats []string,
) (*clientpb.Reservation, error) {
	if gateway.prepareErr != nil {
		return nil, gateway.prepareErr
	}
	if gateway.cancel != nil {
		gateway.cancel()
	}
	return gateway.workerGateway.PreparePayment(ctx, showtime, seats)
}

type reservationReadRepository struct {
	resource *clientpb.Resource
	err      error
}

func (*reservationReadRepository) PutReservation(context.Context, *clientpb.Resource) error {
	return nil
}

func (repository *reservationReadRepository) GetReservation(context.Context, string) (*clientpb.Resource, error) {
	return repository.resource, repository.err
}

func (*reservationReadRepository) ListReservationsByUser(context.Context, string) ([]*clientpb.Resource, error) {
	return nil, nil
}

type failingRandomReader struct{}

func (failingRandomReader) Read([]byte) (int, error) { return 0, errInjected }

func commonLocalDate(year, month, day int32) *commonpb.LocalDate {
	return commonpb.LocalDate_builder{Year: &year, Month: &month, Day: &day}.Build()
}
