package application

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/cineko-org/client/internal/domain"
	catalogpb "github.com/cineko-org/contracts/v3/gen/go/cineko/catalog"
	clientpb "github.com/cineko-org/contracts/v3/gen/go/cineko/client"
	seatmappb "github.com/cineko-org/contracts/v3/gen/go/cineko/seatmap"
)

type claimedSeatWatchGateway interface {
	BookingGateway
}

type watchClaimedRepository struct {
	*workerRepository
	getErr               error
	latest               *clientpb.Resource
	putMonitorErr        error
	putReservationCancel context.CancelFunc
}

func (repository *watchClaimedRepository) GetMonitor(ctx context.Context, id string) (*clientpb.Resource, error) {
	if repository.getErr != nil {
		return nil, repository.getErr
	}
	if repository.latest != nil {
		return repository.latest, nil
	}
	return repository.workerRepository.GetMonitor(ctx, id)
}

func (repository *watchClaimedRepository) PutMonitor(ctx context.Context, resource *clientpb.Resource) error {
	if repository.putMonitorErr != nil {
		return repository.putMonitorErr
	}
	return repository.workerRepository.PutMonitor(ctx, resource)
}

func (repository *watchClaimedRepository) PutReservation(ctx context.Context, resource *clientpb.Resource) error {
	err := repository.workerRepository.PutReservation(ctx, resource)
	if repository.putReservationCancel != nil {
		repository.putReservationCancel()
	}
	return err
}

type paymentErrorGateway struct {
	*exactShowtimeGateway
	err error
}

func (gateway *paymentErrorGateway) PreparePayment(
	context.Context,
	*catalogpb.Showtime,
	[]string,
) (*clientpb.Reservation, error) {
	return nil, gateway.err
}

type waiterError struct{ err error }

func (waiter waiterError) Wait(context.Context, time.Duration) error { return waiter.err }

type liveObservationFailureSequence struct {
	*liveObservationRepositoryFake
	failAt int
	err    error
}

func (repository *liveObservationFailureSequence) SubmitLiveSeatObservation(
	ctx context.Context,
	observation *seatmappb.LiveSeatObservation,
) (*seatmappb.Snapshot, error) {
	if len(repository.requests)+1 == repository.failAt {
		repository.requests = append(repository.requests, observation)
		return nil, repository.err
	}
	return repository.liveObservationRepositoryFake.SubmitLiveSeatObservation(ctx, observation)
}

func TestAttemptClaimedShowtimeRefreshesSamePageUntilSeatAppears(t *testing.T) {
	now := time.Date(2026, time.August, 12, 10, 0, 0, 0, time.UTC)
	repository := claimedSeatWatchRepository()
	gateway := &refreshingShowtimeGateway{
		exactShowtimeGateway: &exactShowtimeGateway{workerGateway: &workerGateway{
			live: []domain.LiveSeat{{Label: "H10", Available: false}},
		}},
		selections: []domain.SeatSelection{{
			SeatMap: gatewaySeatMap(), LiveSeats: []domain.LiveSeat{{Label: "H10", Available: true}},
		}},
	}
	worker := claimedSeatWatchWorker(repository, gateway, now)
	reservation, err := runClaimedShowtime(t.Context(), worker, repository, claimedSeatWatchShowtime())
	if err != nil {
		t.Fatal(err)
	}
	if reservation == nil || reservation.GetReservation() == nil || !reservation.GetReservation().HasPrepared() {
		t.Fatalf("reservation = %v, want prepared", reservation)
	}
	if gateway.openCalls != 1 || gateway.refreshCalls != 1 {
		t.Fatalf("seat-page open/refresh calls = %d/%d, want 1/1", gateway.openCalls, gateway.refreshCalls)
	}
}

func TestWatchClaimedShowtimeCompletesOnlyAgainstLatestActiveMonitor(t *testing.T) {
	now := time.Date(2026, time.August, 12, 10, 0, 0, 0, time.UTC)
	base := claimedSeatWatchRepository()
	repository := &watchClaimedRepository{workerRepository: base}
	gateway := &exactShowtimeGateway{workerGateway: &workerGateway{
		live: []domain.LiveSeat{{Label: "H10", Available: true}},
	}}
	worker := claimedSeatWatchWorker(base, gateway, now)
	worker.monitors = repository
	worker.reservations = repository

	reservation, err := watchClaimedShowtime(t.Context(), worker, base, claimedSeatWatchShowtime())
	if err != nil {
		t.Fatal(err)
	}
	if reservation == nil || !reservation.GetReservation().HasPrepared() {
		t.Fatalf("reservation = %v, want prepared", reservation)
	}
	if monitorStateName(base.job) != "triggered" || base.job.GetReservationId() == "" {
		t.Fatalf("monitor = %s/%q, want triggered reservation", monitorStateName(base.job), base.job.GetReservationId())
	}
}

func TestWatchClaimedShowtimeFailureBoundaries(t *testing.T) {
	now := time.Date(2026, time.August, 12, 10, 0, 0, 0, time.UTC)
	newWorker := func(repository *watchClaimedRepository, gateway BookingGateway) *BookingWorker {
		worker := claimedSeatWatchWorker(repository.workerRepository, gateway, now)
		worker.monitors = repository
		worker.reservations = repository
		return worker
	}
	availableGateway := func() *exactShowtimeGateway {
		return &exactShowtimeGateway{workerGateway: &workerGateway{
			live: []domain.LiveSeat{{Label: "H10", Available: true}},
		}}
	}

	t.Run("attempt", func(t *testing.T) {
		base := claimedSeatWatchRepository()
		repository := &watchClaimedRepository{workerRepository: base}
		attemptErr := errors.New("seat page failed")
		gateway := availableGateway()
		gateway.openErr = attemptErr
		_, err := watchClaimedShowtime(t.Context(), newWorker(repository, gateway), base, claimedSeatWatchShowtime())
		if !errors.Is(err, attemptErr) {
			t.Fatalf("error = %v, want %v", err, attemptErr)
		}
	})

	t.Run("cancelled after hold", func(t *testing.T) {
		base := claimedSeatWatchRepository()
		ctx, cancel := context.WithCancel(t.Context())
		repository := &watchClaimedRepository{workerRepository: base, putReservationCancel: cancel}
		_, err := watchClaimedShowtime(ctx, newWorker(repository, availableGateway()), base, claimedSeatWatchShowtime())
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("error = %v, want context cancellation", err)
		}
	})

	t.Run("latest lookup", func(t *testing.T) {
		base := claimedSeatWatchRepository()
		lookupErr := errors.New("monitor lookup failed")
		repository := &watchClaimedRepository{workerRepository: base, getErr: lookupErr}
		_, err := watchClaimedShowtime(t.Context(), newWorker(repository, availableGateway()), base, claimedSeatWatchShowtime())
		if !errors.Is(err, lookupErr) {
			t.Fatalf("error = %v, want %v", err, lookupErr)
		}
	})

	t.Run("malformed latest monitor", func(t *testing.T) {
		base := claimedSeatWatchRepository()
		repository := &watchClaimedRepository{workerRepository: base, latest: &clientpb.Resource{}}
		if _, err := watchClaimedShowtime(t.Context(), newWorker(repository, availableGateway()), base, claimedSeatWatchShowtime()); err == nil {
			t.Fatal("malformed latest monitor accepted")
		}
	})

	t.Run("inactive latest monitor", func(t *testing.T) {
		base := claimedSeatWatchRepository()
		latest := cloneMonitor(base.job)
		setMonitorState(latest, "stopped", "user_disabled")
		repository := &watchClaimedRepository{workerRepository: base, latest: resourceForMonitor(latest, 4)}
		_, err := watchClaimedShowtime(t.Context(), newWorker(repository, availableGateway()), base, claimedSeatWatchShowtime())
		if !errors.Is(err, ErrConflict) {
			t.Fatalf("error = %v, want conflict", err)
		}
	})

	t.Run("completion persistence", func(t *testing.T) {
		base := claimedSeatWatchRepository()
		putErr := errors.New("monitor persistence failed")
		repository := &watchClaimedRepository{workerRepository: base, putMonitorErr: putErr}
		_, err := watchClaimedShowtime(t.Context(), newWorker(repository, availableGateway()), base, claimedSeatWatchShowtime())
		if !errors.Is(err, putErr) {
			t.Fatalf("error = %v, want %v", err, putErr)
		}
	})
}

func TestWatchClaimedShowtimeValidatesItsInputs(t *testing.T) {
	now := time.Date(2026, time.August, 12, 10, 0, 0, 0, time.UTC)
	job, preset, theater, auditorium, showtime := validClaimedValues()
	worker := NewBookingWorker(BookingWorkerDependencies{Clock: fixedClock{now: now}})
	monitorResource := resourceForMonitor(job, 0)
	presetResource := resourceForPreset(preset, 0)

	if _, err := worker.WatchClaimedShowtime(t.Context(), &clientpb.Resource{}, presetResource, theater, auditorium, showtime); err == nil {
		t.Fatal("malformed monitor resource accepted")
	}
	if _, err := worker.WatchClaimedShowtime(t.Context(), monitorResource, &clientpb.Resource{}, theater, auditorium, showtime); err == nil {
		t.Fatal("malformed preset resource accepted")
	}
	showtime.GetMovie().SetId("different")
	if _, err := worker.WatchClaimedShowtime(t.Context(), monitorResource, presetResource, theater, auditorium, showtime); err == nil {
		t.Fatal("mismatched showtime accepted")
	}
}

func TestAttemptClaimedShowtimeFailureBoundaries(t *testing.T) {
	now := time.Date(2026, time.August, 12, 10, 0, 0, 0, time.UTC)
	newUnavailableGateway := func() *refreshingShowtimeGateway {
		return &refreshingShowtimeGateway{
			exactShowtimeGateway: &exactShowtimeGateway{workerGateway: &workerGateway{
				live: []domain.LiveSeat{{Label: "H10", Available: false}},
			}},
			selections: []domain.SeatSelection{{
				SeatMap: gatewaySeatMap(), LiveSeats: []domain.LiveSeat{{Label: "H10", Available: false}},
			}},
		}
	}

	t.Run("wait", func(t *testing.T) {
		base := claimedSeatWatchRepository()
		gateway := newUnavailableGateway()
		worker := claimedSeatWatchWorker(base, gateway, now)
		waitErr := errors.New("refresh wait failed")
		worker.waiter = waiterError{err: waitErr}
		_, err := runClaimedShowtime(t.Context(), worker, base, claimedSeatWatchShowtime())
		if !errors.Is(err, waitErr) {
			t.Fatalf("error = %v, want %v", err, waitErr)
		}
	})

	t.Run("refresh", func(t *testing.T) {
		base := claimedSeatWatchRepository()
		gateway := newUnavailableGateway()
		refreshErr := errors.New("refresh failed")
		gateway.refreshErr = refreshErr
		_, err := runClaimedShowtime(t.Context(), claimedSeatWatchWorker(base, gateway, now), base, claimedSeatWatchShowtime())
		if !errors.Is(err, refreshErr) {
			t.Fatalf("error = %v, want %v", err, refreshErr)
		}
	})

	t.Run("refreshed observation report", func(t *testing.T) {
		base := claimedSeatWatchRepository()
		gateway := newUnavailableGateway()
		reportErr := errors.New("refreshed observation failed")
		observations := &liveObservationFailureSequence{
			liveObservationRepositoryFake: &liveObservationRepositoryFake{}, failAt: 2, err: reportErr,
		}
		worker := claimedSeatWatchWorker(base, gateway, now)
		worker.observations = observations
		_, err := runClaimedShowtime(t.Context(), worker, base, claimedSeatWatchShowtime())
		if !errors.Is(err, ErrSeatUnavailable) || !errors.Is(err, reportErr) {
			t.Fatalf("error = %v, want unavailable plus %v", err, reportErr)
		}
	})

	t.Run("refresh limit", func(t *testing.T) {
		base := claimedSeatWatchRepository()
		gateway := newUnavailableGateway()
		worker := claimedSeatWatchWorker(base, gateway, now)
		_, err := runClaimedShowtime(t.Context(), worker, base, claimedSeatWatchShowtime())
		if !errors.Is(err, ErrSeatUnavailable) || gateway.refreshCalls != 3 {
			t.Fatalf("error/calls = %v/%d, want unavailable after 3 refreshes", err, gateway.refreshCalls)
		}
	})

	t.Run("provider seat race", func(t *testing.T) {
		base := claimedSeatWatchRepository()
		gateway := &paymentErrorGateway{
			exactShowtimeGateway: &exactShowtimeGateway{workerGateway: &workerGateway{
				live: []domain.LiveSeat{{Label: "H10", Available: true}},
			}},
			err: errors.New("seat is no longer selectable"),
		}
		worker := claimedSeatWatchWorker(base, gateway, now)
		worker.claimedWatch = ClaimedSeatWatchPolicy{}
		_, err := runClaimedShowtime(t.Context(), worker, base, claimedSeatWatchShowtime())
		if !errors.Is(err, ErrSeatUnavailable) {
			t.Fatalf("error = %v, want seat unavailable", err)
		}
	})
}

func TestClaimedSeatWatchPolicyBoundaries(t *testing.T) {
	now := time.Date(2026, time.August, 12, 10, 0, 0, 0, time.UTC)
	worker := &BookingWorker{clock: fixedClock{now: now}}
	showtime := showtimeProtoFromDomain(claimedSeatWatchShowtime())

	worker.claimedWatch = ClaimedSeatWatchPolicy{UntilShowtime: true}
	if !worker.claimedSeatWatchContinues(1, now, showtime) || worker.claimedSeatWatchContinues(1, now, nil) {
		t.Fatal("until-showtime continuation boundary is incorrect")
	}
	worker.claimedWatch = ClaimedSeatWatchPolicy{Window: time.Second, RefreshLimit: 1}
	if worker.claimedSeatWatchContinues(2, now, showtime) {
		t.Fatal("refresh limit was ignored")
	}

	if normalized := (ClaimedSeatWatchPolicy{Window: time.Second}).normalized(); normalized != (ClaimedSeatWatchPolicy{}) {
		t.Fatalf("invalid policy normalized to %+v", normalized)
	}
	normalized := (ClaimedSeatWatchPolicy{
		UntilShowtime: true, Window: time.Second, RefreshLimit: 4,
		MinInterval: time.Second, MaxInterval: time.Millisecond,
	}).normalized()
	if normalized.Window != 0 || normalized.RefreshLimit != 0 || normalized.MaxInterval != normalized.MinInterval {
		t.Fatalf("until-showtime policy normalized to %+v", normalized)
	}
}

func watchClaimedShowtime(
	ctx context.Context,
	worker *BookingWorker,
	repository *workerRepository,
	showtime domain.Showtime,
) (*clientpb.Resource, error) {
	monitor := resourceForMonitor(cloneMonitor(repository.job), 0)
	preset := resourceForPreset(clonePreset(repository.preset), 0)
	theater, err := repository.GetTheater(ctx, repository.theater.ID)
	if err != nil {
		return nil, err
	}
	auditorium, err := repository.GetAuditorium(ctx, repository.auditorium.ID)
	if err != nil {
		return nil, err
	}
	return worker.WatchClaimedShowtime(
		ctx, monitor, preset, theater, auditorium, showtimeProtoFromDomain(showtime),
	)
}
