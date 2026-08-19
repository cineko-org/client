package application

import (
	"context"
	cryptorand "crypto/rand"
	"errors"
	"io"
	"math/big"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/cineko-org/client/internal/domain"
)

const monitorLeaseTTL = 10 * time.Minute

type BookingWorker struct {
	monitors     MonitorRepository
	presets      PresetRepository
	theaters     TheaterRepository
	auditoriums  AuditoriumRepository
	reservations ReservationRepository
	showtimes    ShowtimeGateway
	booking      BookingGateway
	ranker       domain.SeatRanker
	ids          IDGenerator
	clock        Clock
	waiter       Waiter
	jitter       func(time.Duration) time.Duration
	workerID     string
}

type BookingWorkerDependencies struct {
	Monitors     MonitorRepository
	Presets      PresetRepository
	Theaters     TheaterRepository
	Auditoriums  AuditoriumRepository
	Reservations ReservationRepository
	Showtimes    ShowtimeGateway
	Booking      BookingGateway
	IDs          IDGenerator
	Clock        Clock
	Waiter       Waiter
	Jitter       func(time.Duration) time.Duration
	WorkerID     string
}

// ClaimedBooking is the complete, immutable input selected by a Central
// execution lease. Unlike a normal monitor run, it already contains the exact
// observed showtime and therefore must never perform another schedule lookup.
type ClaimedBooking struct {
	Monitor    domain.MonitorJob
	Preset     domain.Preset
	Theater    domain.Theater
	Auditorium domain.Auditorium
	Showtime   domain.Showtime
}

func NewBookingWorker(dependencies BookingWorkerDependencies) *BookingWorker {
	jitter := dependencies.Jitter
	if jitter == nil {
		jitter = randomJitter
	}
	return &BookingWorker{
		monitors: dependencies.Monitors, presets: dependencies.Presets,
		theaters: dependencies.Theaters, auditoriums: dependencies.Auditoriums,
		reservations: dependencies.Reservations,
		showtimes:    dependencies.Showtimes, booking: dependencies.Booking,
		ids: dependencies.IDs, clock: dependencies.Clock, waiter: dependencies.Waiter,
		jitter: jitter, workerID: dependencies.WorkerID,
	}
}

func (worker *BookingWorker) Run(ctx context.Context, monitorID string) (domain.Reservation, error) {
	return worker.run(ctx, monitorID, 0, 0)
}

var ErrBrowserRotation = errors.New("browser restart policy reached")

// RunWithRestartPolicy bounds a Chrome process while leaving the logical
// monitor running. The caller may open a fresh process with the same session
// key and continue safely.
func (worker *BookingWorker) RunWithRestartPolicy(
	ctx context.Context,
	monitorID string,
	maxAttempts int,
	maxAge time.Duration,
) (domain.Reservation, error) {
	return worker.run(ctx, monitorID, maxAttempts, maxAge)
}

func (worker *BookingWorker) run(
	ctx context.Context,
	monitorID string,
	maxAttempts int,
	maxAge time.Duration,
) (domain.Reservation, error) {
	job, err := worker.startMonitor(ctx, monitorID)
	if err != nil {
		return domain.Reservation{}, err
	}
	defer func() {
		_ = worker.monitors.ReleaseMonitor(context.WithoutCancel(ctx), monitorID, worker.workerID)
	}()

	preset, theater, auditorium, err := worker.loadBookingContext(ctx, job)
	if err != nil {
		return domain.Reservation{}, worker.fail(ctx, job, err)
	}
	backoff := job.PollInterval
	startedAt := worker.clock.Now()
	attempts := 0
	for {
		if stopErr := worker.stopReason(ctx, job); stopErr != nil {
			return domain.Reservation{}, worker.stop(ctx, job, stopErr)
		}

		if maxAttempts > 0 && attempts >= maxAttempts || maxAge > 0 && worker.clock.Now().Sub(startedAt) >= maxAge {
			return domain.Reservation{}, ErrBrowserRotation
		}
		reservation, attemptErr := worker.attempt(ctx, job, preset, theater, auditorium)
		attempts++
		result, done, nextBackoff, handleErr := worker.handleAttempt(ctx, &job, reservation, attemptErr, backoff)
		if handleErr != nil || done {
			return result, handleErr
		}
		backoff = nextBackoff

		if renewErr := worker.monitors.RenewMonitor(
			ctx, job.ID, worker.workerID, worker.clock.Now(), monitorLeaseTTL,
		); renewErr != nil {
			return domain.Reservation{}, worker.fail(ctx, job, renewErr)
		}
		if waitErr := worker.waiter.Wait(ctx, backoff+worker.jitter(pollJitterRange(job, backoff))); waitErr != nil {
			return domain.Reservation{}, worker.stopAfterWait(ctx, job, waitErr)
		}
	}
}

// RunOnce performs one continuous booking attempt and releases the monitor
// lease immediately when the target is no longer bookable. Opening monitors
// use it after a disposable scan detects availability so other saved targets
// are never starved by a long-lived browser session.
func (worker *BookingWorker) RunOnce(ctx context.Context, monitorID string) (domain.Reservation, error) {
	job, err := worker.startMonitor(ctx, monitorID)
	if err != nil {
		return domain.Reservation{}, err
	}
	defer func() {
		_ = worker.monitors.ReleaseMonitor(context.WithoutCancel(ctx), monitorID, worker.workerID)
	}()
	preset, theater, auditorium, err := worker.loadBookingContext(ctx, job)
	if err != nil {
		return domain.Reservation{}, worker.fail(ctx, job, err)
	}
	reservation, attemptErr := worker.attempt(ctx, job, preset, theater, auditorium)
	now := worker.clock.Now()
	job.RecordCheck(now, attemptErr)
	if attemptErr == nil {
		result, _, _, completeErr := worker.complete(ctx, &job, reservation, now)
		return result, completeErr
	}
	if errors.Is(attemptErr, context.Canceled) || errors.Is(attemptErr, context.DeadlineExceeded) {
		return domain.Reservation{}, worker.stop(ctx, job, attemptErr)
	}
	if errors.Is(attemptErr, ErrBookingNotOpen) || errors.Is(attemptErr, ErrSeatUnavailable) {
		job.Transition(domain.MonitorRunning, now)
		if err := worker.monitors.PutMonitor(ctx, job); err != nil {
			return domain.Reservation{}, err
		}
		return domain.Reservation{}, attemptErr
	}
	return domain.Reservation{}, worker.fail(ctx, job, attemptErr)
}

// RunClaimedShowtime prepares the exact showtime carried by a Central
// execution command. The Central command lease is the sole execution fence;
// taking an additional in-process monitor lease here would not prevent another
// Client installation from executing and could incorrectly imply otherwise.
func (worker *BookingWorker) RunClaimedShowtime(
	ctx context.Context,
	claimed ClaimedBooking,
) (domain.Reservation, error) {
	if err := claimed.Validate(worker.clock.Now()); err != nil {
		return domain.Reservation{}, err
	}
	job := claimed.Monitor
	job.Transition(domain.MonitorRunning, worker.clock.Now())
	if err := worker.monitors.PutMonitor(ctx, job); err != nil {
		return domain.Reservation{}, err
	}
	reservation, attemptErr := worker.attemptShowtime(
		ctx, job, claimed.Preset, claimed.Showtime,
	)
	if err := ctx.Err(); err != nil {
		return domain.Reservation{}, err
	}
	now := worker.clock.Now()
	job.RecordCheck(now, attemptErr)
	if attemptErr != nil {
		// Central owns retry/exhaustion for execution commands. Keep the monitor
		// eligible while Central decides whether this failed lease is retried.
		if err := worker.monitors.PutMonitor(ctx, job); err != nil {
			return domain.Reservation{}, errors.Join(attemptErr, err)
		}
		return domain.Reservation{}, attemptErr
	}
	result, _, _, completeErr := worker.complete(ctx, &job, reservation, now)
	return result, completeErr
}

func (claimed ClaimedBooking) Validate(now time.Time) error {
	if err := claimed.validateMonitorContext(); err != nil {
		return err
	}
	if err := claimed.validateShowtimeContext(); err != nil {
		return err
	}
	return claimed.validateSchedule(now)
}

func (claimed ClaimedBooking) validateMonitorContext() error {
	job, preset := claimed.Monitor, claimed.Preset
	if job.Status != domain.MonitorPending && job.Status != domain.MonitorRunning {
		return ErrConflict
	}
	if preset.ID == "" || preset.ID != job.PresetID || preset.TheaterID != claimed.Theater.ID ||
		preset.AuditoriumID != claimed.Auditorium.ID {
		return errors.New("claimed booking context does not match the monitor preset")
	}
	return nil
}

func (claimed ClaimedBooking) validateShowtimeContext() error {
	job, showtime := claimed.Monitor, claimed.Showtime
	if showtime.ID == "" || !strings.EqualFold(strings.TrimSpace(showtime.Movie), strings.TrimSpace(job.Movie)) ||
		showtime.AuditoriumID != claimed.Auditorium.ID || showtime.TheaterID != claimed.Theater.ID {
		return errors.New("claimed showtime does not match the monitor")
	}
	if showtime.SoldOut || showtime.AvailableSeats < 1 || showtime.Capacity < showtime.AvailableSeats {
		return ErrBookingNotOpen
	}
	return nil
}

func (claimed ClaimedBooking) validateSchedule(now time.Time) error {
	job, showtime := claimed.Monitor, claimed.Showtime
	if showtime.Date == "" || showtime.StartsAt == "" || showtime.EndsAt == "" {
		return errors.New("claimed showtime schedule is incomplete")
	}
	if !slices.Contains(job.ResolveTargetDates(now), showtime.Date) ||
		job.EarliestTime != "" && showtime.StartsAt < job.EarliestTime ||
		job.LatestTime != "" && showtime.StartsAt > job.LatestTime {
		return errors.New("claimed showtime is outside the monitor schedule")
	}
	return nil
}

func (worker *BookingWorker) startMonitor(ctx context.Context, monitorID string) (domain.MonitorJob, error) {
	job, err := worker.monitors.AcquireMonitor(
		ctx, monitorID, worker.workerID, worker.clock.Now(), monitorLeaseTTL,
	)
	if err != nil {
		return domain.MonitorJob{}, err
	}
	job.Transition(domain.MonitorRunning, worker.clock.Now())
	if err := worker.monitors.PutMonitor(ctx, job); err != nil {
		return domain.MonitorJob{}, err
	}
	return job, nil
}

func (worker *BookingWorker) stopReason(ctx context.Context, job domain.MonitorJob) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if job.Expired(worker.clock.Now()) {
		return ErrMonitorExpired
	}
	return nil
}

func (worker *BookingWorker) handleAttempt(
	ctx context.Context,
	job *domain.MonitorJob,
	reservation domain.Reservation,
	attemptErr error,
	backoff time.Duration,
) (domain.Reservation, bool, time.Duration, error) {
	now := worker.clock.Now()
	job.RecordCheck(now, attemptErr)
	if attemptErr == nil {
		return worker.complete(ctx, job, reservation, now)
	}
	if errors.Is(attemptErr, ErrBookingNotOpen) &&
		job.EffectiveMode() == domain.MonitorModeCancellation {
		return domain.Reservation{}, true, backoff, worker.fail(ctx, *job, attemptErr)
	}
	if err := worker.monitors.PutMonitor(ctx, *job); err != nil {
		return domain.Reservation{}, true, backoff, err
	}
	if errors.Is(attemptErr, ErrBookingNotOpen) || errors.Is(attemptErr, ErrSeatUnavailable) {
		return domain.Reservation{}, false, job.PollInterval, nil
	}
	return domain.Reservation{}, false, min(backoff*2, 30*time.Second), nil
}

func (worker *BookingWorker) complete(
	ctx context.Context,
	job *domain.MonitorJob,
	reservation domain.Reservation,
	now time.Time,
) (domain.Reservation, bool, time.Duration, error) {
	job.ReservationID = reservation.ID
	status := domain.MonitorBooked
	if reservation.Status == "prepared" {
		status = domain.MonitorTriggered
	}
	job.Transition(status, now)
	if err := worker.monitors.PutMonitor(ctx, *job); err != nil {
		return domain.Reservation{}, true, 0, err
	}
	return reservation, true, 0, nil
}

func (worker *BookingWorker) stop(ctx context.Context, job domain.MonitorJob, cause error) error {
	if errors.Is(cause, context.Canceled) || errors.Is(cause, context.DeadlineExceeded) {
		job.Transition(domain.MonitorStopped, worker.clock.Now())
		_ = worker.monitors.PutMonitor(context.WithoutCancel(ctx), job)
		return cause
	}
	return worker.fail(ctx, job, cause)
}

func (worker *BookingWorker) stopAfterWait(ctx context.Context, job domain.MonitorJob, cause error) error {
	if ctx.Err() != nil {
		job.Transition(domain.MonitorStopped, worker.clock.Now())
		_ = worker.monitors.PutMonitor(context.WithoutCancel(ctx), job)
	}
	return cause
}

func (worker *BookingWorker) attempt(
	ctx context.Context,
	job domain.MonitorJob,
	preset domain.Preset,
	theater domain.Theater,
	auditorium domain.Auditorium,
) (domain.Reservation, error) {
	showtimes, err := worker.showtimes.FindShowtimes(ctx, ShowtimeQuery{
		Movie: job.Movie, Theater: theater, Auditorium: auditorium,
		TargetDates:  job.ResolveTargetDates(worker.clock.Now()),
		EarliestTime: job.EarliestTime, LatestTime: job.LatestTime,
	})
	if err != nil {
		return domain.Reservation{}, err
	}
	showtime, ok := bestShowtime(showtimes)
	if !ok {
		return domain.Reservation{}, ErrBookingNotOpen
	}
	return worker.attemptShowtime(ctx, job, preset, showtime)
}

func (worker *BookingWorker) attemptShowtime(
	ctx context.Context,
	job domain.MonitorJob,
	preset domain.Preset,
	showtime domain.Showtime,
) (domain.Reservation, error) {
	selection, err := worker.booking.OpenSeatSelection(ctx, showtime, preset.SeatCount)
	if err != nil {
		return domain.Reservation{}, err
	}
	ranked, err := worker.ranker.Rank(
		selection.SeatMap, selection.LiveSeats, preset.SeatCount, preset.SeatPreference,
	)
	if err != nil || len(ranked) == 0 {
		return domain.Reservation{}, ErrSeatUnavailable
	}
	labels := seatLabels(ranked[0].Seats)
	draft, err := worker.booking.PreparePayment(ctx, showtime, labels)
	if err != nil {
		return domain.Reservation{}, err
	}
	if err := ctx.Err(); err != nil {
		return domain.Reservation{}, err
	}
	reservation := domain.Reservation{
		ID: worker.ids.NewID(), UserID: job.UserID, MonitorID: job.ID,
		Draft: draft, Status: "prepared",
	}
	return reservation, worker.reservations.PutReservation(ctx, reservation)
}

func (worker *BookingWorker) loadBookingContext(
	ctx context.Context,
	job domain.MonitorJob,
) (domain.Preset, domain.Theater, domain.Auditorium, error) {
	preset, err := worker.presets.GetPreset(ctx, job.PresetID)
	if err != nil {
		return domain.Preset{}, domain.Theater{}, domain.Auditorium{}, err
	}
	theater, err := worker.theaters.GetTheater(ctx, preset.TheaterID)
	if err != nil {
		return domain.Preset{}, domain.Theater{}, domain.Auditorium{}, err
	}
	auditorium, err := worker.auditoriums.GetAuditorium(ctx, preset.AuditoriumID)
	if err != nil {
		return domain.Preset{}, domain.Theater{}, domain.Auditorium{}, err
	}
	return preset, theater, auditorium, nil
}

func (worker *BookingWorker) fail(ctx context.Context, job domain.MonitorJob, cause error) error {
	job.LastError = cause.Error()
	job.Transition(domain.MonitorFailed, worker.clock.Now())
	if err := worker.monitors.PutMonitor(ctx, job); err != nil {
		return errors.Join(cause, err)
	}
	return cause
}

func bestShowtime(showtimes []domain.Showtime) (domain.Showtime, bool) {
	available := make([]domain.Showtime, 0, len(showtimes))
	for _, showtime := range showtimes {
		if !showtime.SoldOut {
			available = append(available, showtime)
		}
	}
	if len(available) == 0 {
		return domain.Showtime{}, false
	}
	sort.Slice(available, func(i, j int) bool {
		if available[i].Date == available[j].Date {
			return available[i].StartsAt < available[j].StartsAt
		}
		return available[i].Date < available[j].Date
	})
	return available[0], true
}

func seatLabels(seats []domain.Seat) []string {
	labels := make([]string, len(seats))
	for index, seat := range seats {
		labels[index] = seat.Label
	}
	return labels
}

func randomJitter(limit time.Duration) time.Duration {
	return randomJitterFrom(cryptorand.Reader, limit)
}

func pollJitterRange(job domain.MonitorJob, backoff time.Duration) time.Duration {
	if backoff == job.PollInterval {
		return job.EffectivePollIntervalMax() - job.PollInterval
	}
	return backoff / 5
}

func randomJitterFrom(reader io.Reader, limit time.Duration) time.Duration {
	if limit <= 0 {
		return 0
	}
	value, err := cryptorand.Int(reader, big.NewInt(int64(limit)))
	if err != nil {
		return 0
	}
	return time.Duration(value.Int64())
}

func RandomDelayBetween(minimum, maximum time.Duration) time.Duration {
	return randomDelayBetween(cryptorand.Reader, minimum, maximum)
}

func randomDelayBetween(reader io.Reader, minimum, maximum time.Duration) time.Duration {
	if minimum <= 0 || maximum <= minimum {
		return 0
	}
	span := maximum - minimum
	value, err := cryptorand.Int(reader, big.NewInt(int64(span)))
	if err != nil {
		return minimum
	}
	return minimum + time.Duration(value.Int64())
}
