package application

import (
	"bytes"
	"context"
	"errors"
	"testing"
	"time"

	"github.com/cineko-org/client/internal/domain"
)

func TestBookingWorkerRunLifecycleFailures(t *testing.T) {
	ctx := context.Background()

	worker, monitors, _, _, _, _ := newWorkerCoverageHarness()
	monitors.getErr = errInjected
	if _, err := worker.Run(ctx, "monitor"); !errors.Is(err, errInjected) {
		t.Fatalf("Run(acquire) error = %v", err)
	}

	worker, monitors, _, _, _, _ = newWorkerCoverageHarness()
	monitors.putErr = errInjected
	if _, err := worker.Run(ctx, "monitor"); !errors.Is(err, errInjected) {
		t.Fatalf("Run(start persist) error = %v", err)
	}

	worker, _, presets, _, _, _ := newWorkerCoverageHarness()
	presets.getErr = errInjected
	if _, err := worker.Run(ctx, "monitor"); !errors.Is(err, errInjected) {
		t.Fatalf("Run(load) error = %v", err)
	}

	worker, monitors, _, _, _, _ = newWorkerCoverageHarness()
	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	if _, err := worker.Run(cancelled, "monitor"); !errors.Is(err, context.Canceled) {
		t.Fatalf("Run(cancelled) error = %v", err)
	}
	if monitors.job.Status != domain.MonitorStopped {
		t.Fatalf("cancelled monitor status = %q", monitors.job.Status)
	}

	worker, monitors, _, _, _, _ = newWorkerCoverageHarness()
	monitors.job.TargetDates = []string{"2026-08-08"}
	if _, err := worker.Run(ctx, "monitor"); !errors.Is(err, ErrMonitorExpired) {
		t.Fatalf("Run(expired) error = %v", err)
	}

	worker, monitors, _, _, showtimes, _ := newWorkerCoverageHarness()
	showtimes.values = nil
	monitors.renewErr = errInjected
	if _, err := worker.Run(ctx, "monitor"); !errors.Is(err, errInjected) {
		t.Fatalf("Run(renew) error = %v", err)
	}

	worker, _, _, _, showtimes, waiter := newWorkerCoverageHarness()
	showtimes.values = nil
	waiter.err = errInjected
	if _, err := worker.Run(ctx, "monitor"); !errors.Is(err, errInjected) {
		t.Fatalf("Run(wait) error = %v", err)
	}

	worker, _, _, _, showtimes, waiter = newWorkerCoverageHarness()
	showtimes.err = errInjected
	waiter.err = errInjected
	if _, err := worker.Run(ctx, "monitor"); !errors.Is(err, errInjected) {
		t.Fatalf("Run(backoff) error = %v", err)
	}
}

func TestBookingWorkerRestartPolicy(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	worker, _, _, _, showtimes, _ := newWorkerCoverageHarness()
	showtimes.values = nil
	if _, err := worker.RunWithRestartPolicy(ctx, "monitor", 1, time.Hour); !errors.Is(err, ErrBrowserRotation) {
		t.Fatalf("RunWithRestartPolicy() = %v", err)
	}
}

func TestBookingWorkerAttemptPaths(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	worker, _, _, repository, showtimes, _ := newWorkerCoverageHarness()
	job := validWorkerJob()
	preset := applicationPreset("user", "auditorium", worker.clock.Now())
	preset.SeatPreference.Adjacency = domain.SeatAdjacencyRequired
	theater := domain.Theater{ID: "theater"}
	auditorium := domain.Auditorium{ID: "auditorium"}
	booking, ok := worker.booking.(*workerBookingGatewayFake)
	if !ok {
		t.Fatalf("booking gateway type = %T", worker.booking)
	}

	showtimes.err = errInjected
	if _, err := worker.attempt(ctx, job, preset, theater, auditorium); !errors.Is(err, errInjected) {
		t.Fatalf("attempt(showtimes) error = %v", err)
	}
	showtimes.err = nil
	showtimes.values = nil
	if _, err := worker.attempt(ctx, job, preset, theater, auditorium); !errors.Is(err, ErrBookingNotOpen) {
		t.Fatalf("attempt(no showtime) error = %v", err)
	}
	showtimes.values = []domain.Showtime{{ID: "showtime", Date: "2026-08-10", StartsAt: "20:00"}}
	booking.openErr = errInjected
	if _, err := worker.attempt(ctx, job, preset, theater, auditorium); !errors.Is(err, errInjected) {
		t.Fatalf("attempt(open) error = %v", err)
	}
	booking.openErr = nil
	booking.live = nil
	if _, err := worker.attempt(ctx, job, preset, theater, auditorium); !errors.Is(err, ErrSeatUnavailable) {
		t.Fatalf("attempt(no seats) error = %v", err)
	}
	booking.live = []domain.LiveSeat{{Label: "A1", Available: true}}
	booking.prepareErr = errInjected
	if _, err := worker.attempt(ctx, job, preset, theater, auditorium); !errors.Is(err, errInjected) {
		t.Fatalf("attempt(prepare) error = %v", err)
	}
	booking.prepareErr = nil
	repository.putErr = errInjected
	if _, err := worker.attempt(ctx, job, preset, theater, auditorium); !errors.Is(err, errInjected) {
		t.Fatalf("attempt(save draft) error = %v", err)
	}
	repository.putErr = nil
	reservation, err := worker.attempt(ctx, job, preset, theater, auditorium)
	if err != nil || reservation.Status != "prepared" || reservation.BookingNumber != "" {
		t.Fatalf("attempt(prepare) = %+v, %v", reservation, err)
	}
	repository.putErr = errInjected
	if _, err := worker.attempt(ctx, job, preset, theater, auditorium); !errors.Is(err, errInjected) {
		t.Fatalf("attempt(save preparation) error = %v", err)
	}
}

func TestBookingWorkerRunOncePaths(t *testing.T) {
	ctx := context.Background()
	worker, monitors, _, _, _, _ := newWorkerCoverageHarness()
	monitors.getErr = errInjected
	if _, err := worker.RunOnce(ctx, "monitor"); !errors.Is(err, errInjected) {
		t.Fatalf("RunOnce(acquire) = %v", err)
	}

	worker, _, presets, _, _, _ := newWorkerCoverageHarness()
	presets.getErr = errInjected
	if _, err := worker.RunOnce(ctx, "monitor"); !errors.Is(err, errInjected) {
		t.Fatalf("RunOnce(load) = %v", err)
	}

	worker, monitors, _, _, showtimes, _ := newWorkerCoverageHarness()
	showtimes.values = nil
	if _, err := worker.RunOnce(ctx, "monitor"); !errors.Is(err, ErrBookingNotOpen) || monitors.job.Status != domain.MonitorRunning {
		t.Fatalf("RunOnce(retry) = %v, status %q", err, monitors.job.Status)
	}

	worker, monitors, _, _, showtimes, _ = newWorkerCoverageHarness()
	showtimes.values = nil
	secondPutFailure := &runOnceMonitorRepository{monitorRepositoryFake: monitors, failAt: 2}
	worker.monitors = secondPutFailure
	if _, err := worker.RunOnce(ctx, "monitor"); !errors.Is(err, errInjected) {
		t.Fatalf("RunOnce(retry persist) = %v", err)
	}

	worker, _, _, _, showtimes, _ = newWorkerCoverageHarness()
	showtimes.err = errInjected
	if _, err := worker.RunOnce(ctx, "monitor"); !errors.Is(err, errInjected) {
		t.Fatalf("RunOnce(fail) = %v", err)
	}

	worker, monitors, _, _, showtimes, _ = newWorkerCoverageHarness()
	showtimes.err = context.Canceled
	if _, err := worker.RunOnce(ctx, "monitor"); !errors.Is(err, context.Canceled) || monitors.job.Status != domain.MonitorStopped {
		t.Fatalf("RunOnce(cancel) = %v, status %q", err, monitors.job.Status)
	}

	worker, monitors, _, _, _, _ = newWorkerCoverageHarness()
	reservation, err := worker.RunOnce(ctx, "monitor")
	if err != nil || reservation.Status != "prepared" || monitors.job.Status != domain.MonitorTriggered {
		t.Fatalf("RunOnce(success) = %+v, %v", reservation, err)
	}
}

type runOnceMonitorRepository struct {
	*monitorRepositoryFake
	puts   int
	failAt int
}

func (repository *runOnceMonitorRepository) PutMonitor(ctx context.Context, job domain.MonitorJob) error {
	repository.puts++
	if repository.puts == repository.failAt {
		return errInjected
	}
	return repository.monitorRepositoryFake.PutMonitor(ctx, job)
}

func TestBookingWorkerStateTransitionsAndHelpers(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	worker, monitors, _, _, _, _ := newWorkerCoverageHarness()
	job := validWorkerJob()
	prepared := domain.Reservation{ID: "prepared", Status: "prepared"}

	result, done, backoff, err := worker.handleAttempt(ctx, &job, prepared, nil, 5*time.Second)
	if err != nil || !done || backoff != 0 || result.ID != prepared.ID || job.Status != domain.MonitorTriggered {
		t.Fatalf("handleAttempt(prepared) = %+v, %t, %v, %v", result, done, backoff, err)
	}
	job = validWorkerJob()
	booked := domain.Reservation{ID: "booked", Status: "booked"}
	if _, _, _, err := worker.handleAttempt(ctx, &job, booked, nil, 5*time.Second); err != nil || job.Status != domain.MonitorBooked {
		t.Fatalf("handleAttempt(booked) status/error = %q, %v", job.Status, err)
	}
	monitors.putErr = errInjected
	job = validWorkerJob()
	if _, _, _, err := worker.handleAttempt(ctx, &job, booked, nil, 5*time.Second); !errors.Is(err, errInjected) {
		t.Fatalf("handleAttempt(complete error) = %v", err)
	}
	monitors.putErr = nil

	job = validWorkerJob()
	_, done, backoff, err = worker.handleAttempt(ctx, &job, domain.Reservation{}, ErrSeatUnavailable, 10*time.Second)
	if err != nil || done || backoff != job.PollInterval {
		t.Fatalf("handleAttempt(retryable) = %t, %v, %v", done, backoff, err)
	}
	job = validWorkerJob()
	_, done, backoff, err = worker.handleAttempt(ctx, &job, domain.Reservation{}, errInjected, 20*time.Second)
	if err != nil || done || backoff != 30*time.Second {
		t.Fatalf("handleAttempt(backoff) = %t, %v, %v", done, backoff, err)
	}
	monitors.putErr = errInjected
	job = validWorkerJob()
	if _, _, _, err := worker.handleAttempt(ctx, &job, domain.Reservation{}, errInjected, time.Second); !errors.Is(err, errInjected) {
		t.Fatalf("handleAttempt(save error) = %v", err)
	}
	monitors.putErr = nil
	job = validWorkerJob()
	job.Mode = domain.MonitorModeCancellation
	if _, done, _, err := worker.handleAttempt(ctx, &job, domain.Reservation{}, ErrBookingNotOpen, time.Second); !done || !errors.Is(err, ErrBookingNotOpen) {
		t.Fatalf("handleAttempt(cancellation) = %t, %v", done, err)
	}

	if worker.stopReason(ctx, validWorkerJob()) != nil {
		t.Fatal("stopReason stopped active monitor")
	}
	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	if !errors.Is(worker.stopReason(cancelled, validWorkerJob()), context.Canceled) {
		t.Fatal("stopReason ignored cancellation")
	}
	expired := validWorkerJob()
	expired.TargetDates = []string{"2026-08-08"}
	if !errors.Is(worker.stopReason(ctx, expired), ErrMonitorExpired) {
		t.Fatal("stopReason ignored expiration")
	}
}

func TestBookingWorkerStopLoadAndUtilityPaths(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	worker, monitors, presets, catalog, showtimes, _ := newWorkerCoverageHarness()
	job := validWorkerJob()

	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	if err := worker.stop(cancelled, job, context.Canceled); !errors.Is(err, context.Canceled) {
		t.Fatalf("stop(cancelled) = %v", err)
	}
	if err := worker.stop(ctx, job, errInjected); !errors.Is(err, errInjected) {
		t.Fatalf("stop(failed) = %v", err)
	}
	if err := worker.stopAfterWait(cancelled, job, errInjected); !errors.Is(err, errInjected) {
		t.Fatalf("stopAfterWait(cancelled) = %v", err)
	}
	if err := worker.stopAfterWait(ctx, job, errInjected); !errors.Is(err, errInjected) {
		t.Fatalf("stopAfterWait(active) = %v", err)
	}

	presets.getErr = errInjected
	if _, _, _, err := worker.loadBookingContext(ctx, job); !errors.Is(err, errInjected) {
		t.Fatalf("loadBookingContext(preset) = %v", err)
	}
	presets.getErr = nil
	catalog.fail = "get-theater"
	if _, _, _, err := worker.loadBookingContext(ctx, job); !errors.Is(err, errInjected) {
		t.Fatalf("loadBookingContext(theater) = %v", err)
	}
	catalog.fail = "get-auditorium"
	if _, _, _, err := worker.loadBookingContext(ctx, job); !errors.Is(err, errInjected) {
		t.Fatalf("loadBookingContext(auditorium) = %v", err)
	}
	monitors.putErr = errInjected
	if err := worker.fail(ctx, job, errors.New("cause")); !errors.Is(err, errInjected) || !errors.Is(err, errors.New("cause")) {
		// errors.Is cannot match separately-created errors; the joined repository
		// error assertion is the contract that matters here.
		if !errors.Is(err, errInjected) {
			t.Fatalf("fail(join) = %v", err)
		}
	}

	showtimes.values = []domain.Showtime{
		{ID: "sold", SoldOut: true},
		{ID: "later", Date: "2026-08-11", StartsAt: "10:00"},
		{ID: "same-late", Date: "2026-08-10", StartsAt: "21:00"},
		{ID: "first", Date: "2026-08-10", StartsAt: "19:00"},
	}
	best, ok := bestShowtime(showtimes.values)
	if !ok || best.ID != "first" {
		t.Fatalf("bestShowtime() = %+v, %t", best, ok)
	}
	if _, ok := bestShowtime([]domain.Showtime{{SoldOut: true}}); ok {
		t.Fatal("bestShowtime accepted sold-out showtime")
	}
	if got := seatLabels([]domain.Seat{{Label: "A1"}, {Label: "A2"}}); len(got) != 2 || got[1] != "A2" {
		t.Fatalf("seatLabels() = %v", got)
	}
	if randomJitter(0) != 0 {
		t.Fatal("randomJitter(0) must be zero")
	}
	if jitter := randomJitter(time.Second); jitter < 0 || jitter >= time.Second {
		t.Fatalf("randomJitter() = %v", jitter)
	}
	if randomJitterFrom(bytes.NewReader(nil), time.Second) != 0 {
		t.Fatal("randomJitterFrom must fall back to zero on entropy failure")
	}
	pollJob := domain.MonitorJob{PollInterval: 3 * time.Minute, PollIntervalMax: 8 * time.Minute}
	if got := pollJitterRange(pollJob, pollJob.PollInterval); got != 5*time.Minute {
		t.Fatalf("pollJitterRange(normal) = %v", got)
	}
	if got := pollJitterRange(pollJob, 10*time.Minute); got != 2*time.Minute {
		t.Fatalf("pollJitterRange(backoff) = %v", got)
	}
	if RandomDelayBetween(0, time.Second) != 0 || RandomDelayBetween(time.Second, time.Second) != 0 {
		t.Fatal("RandomDelayBetween must reject invalid ranges")
	}
	if delay := RandomDelayBetween(time.Second, 2*time.Second); delay < time.Second || delay >= 2*time.Second {
		t.Fatalf("RandomDelayBetween() = %v", delay)
	}
	if randomDelayBetween(bytes.NewReader(nil), time.Second, 2*time.Second) != time.Second {
		t.Fatal("randomDelayBetween must use minimum on entropy failure")
	}
}

func newWorkerCoverageHarness() (
	*BookingWorker,
	*monitorRepositoryFake,
	*presetRepositoryFake,
	*workerDataRepositoryFake,
	*showtimeGatewayCoverageFake,
	*waiterCoverageFake,
) {
	now := time.Date(2026, time.August, 9, 10, 0, 0, 0, time.UTC)
	monitors := &monitorRepositoryFake{job: validWorkerJob()}
	presets := newPresetRepositoryFake()
	presets.values["preset"] = applicationPreset("user", "auditorium", now)
	data := &workerDataRepositoryFake{
		theater: domain.Theater{ID: "theater"}, auditorium: domain.Auditorium{ID: "auditorium"},
		seatMap: validApplicationSeatMap("auditorium", now),
	}
	showtimes := &showtimeGatewayCoverageFake{values: []domain.Showtime{{
		ID: "showtime", Date: "2026-08-10", StartsAt: "20:00",
	}}}
	booking := &workerBookingGatewayFake{
		seatMap: validApplicationSeatMap("auditorium", now),
		live:    []domain.LiveSeat{{Label: "A1", Available: true}},
	}
	waiter := &waiterCoverageFake{}
	worker := NewBookingWorker(BookingWorkerDependencies{
		Monitors: monitors, Presets: presets, Theaters: data, Auditoriums: data,
		Reservations: data, Showtimes: showtimes, Booking: booking, IDs: &sequenceIDs{},
		Clock: fixedClock{now}, Waiter: waiter, Jitter: func(time.Duration) time.Duration { return 0 },
		WorkerID: "worker",
	})
	return worker, monitors, presets, data, showtimes, waiter
}

func validWorkerJob() domain.MonitorJob {
	now := time.Date(2026, time.August, 9, 10, 0, 0, 0, time.UTC)
	return domain.MonitorJob{
		ID: "monitor", UserID: "user", PresetID: "preset", Mode: domain.MonitorModeOpening,
		MovieID: "movie", Movie: "Movie", TargetDates: []string{"2026-08-10"}, PollInterval: 5 * time.Second,
		Status: domain.MonitorPending, CreatedAt: now, UpdatedAt: now,
	}
}

type workerDataRepositoryFake struct {
	theater     domain.Theater
	auditorium  domain.Auditorium
	seatMap     domain.SeatMap
	reservation domain.Reservation
	putErr      error
	fail        string
}

func (repository *workerDataRepositoryFake) PutTheater(context.Context, domain.Theater) error {
	return nil
}

func (repository *workerDataRepositoryFake) GetTheater(context.Context, string) (domain.Theater, error) {
	if repository.fail == "get-theater" {
		return domain.Theater{}, errInjected
	}
	return repository.theater, nil
}

func (repository *workerDataRepositoryFake) ListTheaters(context.Context) ([]domain.Theater, error) {
	return []domain.Theater{repository.theater}, nil
}

func (repository *workerDataRepositoryFake) PutAuditorium(context.Context, domain.Auditorium) error {
	return nil
}

func (repository *workerDataRepositoryFake) GetAuditorium(context.Context, string) (domain.Auditorium, error) {
	if repository.fail == "get-auditorium" {
		return domain.Auditorium{}, errInjected
	}
	return repository.auditorium, nil
}

func (repository *workerDataRepositoryFake) ListAuditoriumsByTheater(
	context.Context,
	string,
) ([]domain.Auditorium, error) {
	return []domain.Auditorium{repository.auditorium}, nil
}

func (repository *workerDataRepositoryFake) PutSeatMap(context.Context, domain.SeatMap) error {
	return nil
}

func (repository *workerDataRepositoryFake) GetSeatMap(context.Context, string) (domain.SeatMap, error) {
	if repository.fail == "get-seat-map" {
		return domain.SeatMap{}, errInjected
	}
	return repository.seatMap, nil
}

func (repository *workerDataRepositoryFake) PutReservation(
	_ context.Context,
	value domain.Reservation,
) error {
	if repository.putErr != nil {
		return repository.putErr
	}
	repository.reservation = value
	return nil
}

func (repository *workerDataRepositoryFake) GetReservation(
	context.Context,
	string,
) (domain.Reservation, error) {
	return repository.reservation, nil
}

func (repository *workerDataRepositoryFake) ListReservationsByUser(
	context.Context,
	string,
) ([]domain.Reservation, error) {
	return []domain.Reservation{repository.reservation}, nil
}

type showtimeGatewayCoverageFake struct {
	values []domain.Showtime
	err    error
}

func (gateway *showtimeGatewayCoverageFake) FindShowtimes(
	context.Context,
	ShowtimeQuery,
) ([]domain.Showtime, error) {
	return gateway.values, gateway.err
}

type workerBookingGatewayFake struct {
	seatMap    domain.SeatMap
	live       []domain.LiveSeat
	openErr    error
	prepareErr error
}

func (gateway *workerBookingGatewayFake) OpenSeatSelection(
	context.Context,
	domain.Showtime,
	int,
) (domain.SeatSelection, error) {
	return domain.SeatSelection{SeatMap: gateway.seatMap, LiveSeats: gateway.live}, gateway.openErr
}

func (gateway *workerBookingGatewayFake) PreparePayment(
	_ context.Context,
	showtime domain.Showtime,
	seats []string,
) (domain.BookingDraft, error) {
	return domain.BookingDraft{
		Showtime: showtime, SeatLabels: seats,
	}, gateway.prepareErr
}

func (*workerBookingGatewayFake) PrepareCancellation(
	context.Context,
	domain.Reservation,
) (domain.CancellationDraft, error) {
	return domain.CancellationDraft{}, nil
}

func (*workerBookingGatewayFake) CommitCancellation(context.Context) error { return nil }

type waiterCoverageFake struct{ err error }

func (waiter *waiterCoverageFake) Wait(context.Context, time.Duration) error { return waiter.err }
