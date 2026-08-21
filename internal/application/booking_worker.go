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
	catalogpb "github.com/cineko-org/contracts/gen/go/cineko/catalog"
	clientpb "github.com/cineko-org/contracts/gen/go/cineko/client"
	seatmappb "github.com/cineko-org/contracts/gen/go/cineko/seatmap"
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
	claimedWatch ClaimedSeatWatchPolicy
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
	ClaimedWatch ClaimedSeatWatchPolicy
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
		claimedWatch: dependencies.ClaimedWatch.normalized(),
	}
}

func (worker *BookingWorker) Run(ctx context.Context, monitorID string) (*clientpb.Resource, error) {
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
) (*clientpb.Resource, error) {
	return worker.run(ctx, monitorID, maxAttempts, maxAge)
}

func (worker *BookingWorker) run(
	ctx context.Context,
	monitorID string,
	maxAttempts int,
	maxAge time.Duration,
) (*clientpb.Resource, error) {
	job, err := worker.startMonitor(ctx, monitorID)
	if err != nil {
		return nil, err
	}
	defer func() {
		_ = worker.monitors.ReleaseMonitor(context.WithoutCancel(ctx), monitorID, worker.workerID)
	}()

	preset, theater, auditorium, err := worker.loadBookingContext(ctx, job)
	if err != nil {
		return nil, worker.fail(ctx, job, err)
	}
	backoff := monitorPollInterval(job)
	startedAt := worker.clock.Now()
	attempts := 0
	for {
		if stopErr := worker.stopReason(ctx, job); stopErr != nil {
			return nil, worker.stop(ctx, job, stopErr)
		}

		if maxAttempts > 0 && attempts >= maxAttempts || maxAge > 0 && worker.clock.Now().Sub(startedAt) >= maxAge {
			return nil, ErrBrowserRotation
		}
		reservation, attemptErr := worker.attempt(ctx, job, preset, theater, auditorium)
		attempts++
		result, done, nextBackoff, handleErr := worker.handleAttempt(ctx, job, reservation, attemptErr, backoff)
		if handleErr != nil || done {
			return result, handleErr
		}
		backoff = nextBackoff

		if renewErr := worker.monitors.RenewMonitor(
			ctx, job.GetId(), worker.workerID, worker.clock.Now(), monitorLeaseTTL,
		); renewErr != nil {
			return nil, worker.fail(ctx, job, renewErr)
		}
		if waitErr := worker.waiter.Wait(ctx, backoff+worker.jitter(pollJitterRange(job, backoff))); waitErr != nil {
			return nil, worker.stopAfterWait(ctx, job, waitErr)
		}
	}
}

// RunOnce performs one continuous booking attempt and releases the monitor
// lease immediately when the target is no longer bookable. Opening monitors
// use it after a disposable scan detects availability so other saved targets
// are never starved by a long-lived browser session.
func (worker *BookingWorker) RunOnce(ctx context.Context, monitorID string) (*clientpb.Resource, error) {
	job, err := worker.startMonitor(ctx, monitorID)
	if err != nil {
		return nil, err
	}
	defer func() {
		_ = worker.monitors.ReleaseMonitor(context.WithoutCancel(ctx), monitorID, worker.workerID)
	}()
	preset, theater, auditorium, err := worker.loadBookingContext(ctx, job)
	if err != nil {
		return nil, worker.fail(ctx, job, err)
	}
	reservation, attemptErr := worker.attempt(ctx, job, preset, theater, auditorium)
	now := worker.clock.Now()
	monitorRecordCheck(job, now, attemptErr)
	if attemptErr == nil {
		result, _, _, completeErr := worker.complete(ctx, job, reservation, now)
		if completeErr != nil {
			return nil, completeErr
		}
		return result, nil
	}
	if errors.Is(attemptErr, context.Canceled) || errors.Is(attemptErr, context.DeadlineExceeded) {
		return nil, worker.stop(ctx, job, attemptErr)
	}
	if errors.Is(attemptErr, ErrBookingNotOpen) || errors.Is(attemptErr, ErrSeatUnavailable) {
		monitorTransition(job, "running", now)
		if err := worker.putMonitor(ctx, job, 0); err != nil {
			return nil, err
		}
		return nil, attemptErr
	}
	return nil, worker.fail(ctx, job, attemptErr)
}

// RunClaimedShowtime prepares the exact showtime carried by a Central
// execution command. The Central command lease is the sole execution fence;
// taking an additional in-process monitor lease here would not prevent another
// Client installation from executing and could incorrectly imply otherwise.
func (worker *BookingWorker) RunClaimedShowtime(
	ctx context.Context,
	monitorResource *clientpb.Resource,
	presetResource *clientpb.Resource,
	theaterMessage *catalogpb.Theater,
	auditoriumMessage *catalogpb.Auditorium,
	showtimeMessage *catalogpb.Showtime,
) (*clientpb.Resource, error) {
	job, revision, err := monitorMessage(monitorResource)
	if err != nil {
		return nil, err
	}
	preset, _, err := presetMessage(presetResource)
	if err != nil {
		return nil, err
	}
	if err := validateClaimedBooking(job, preset, theaterMessage, auditoriumMessage, showtimeMessage, worker.clock.Now()); err != nil {
		return nil, err
	}
	monitorTransition(job, "running", worker.clock.Now())
	if err := worker.putMonitor(ctx, job, revision); err != nil {
		return nil, err
	}
	reservation, attemptErr := worker.attemptClaimedShowtime(ctx, job, preset, showtimeMessage)
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	now := worker.clock.Now()
	monitorRecordCheck(job, now, attemptErr)
	if attemptErr != nil {
		// Central owns retry/exhaustion for execution commands. Keep the monitor
		// eligible while Central decides whether this failed lease is retried.
		if err := worker.putMonitor(ctx, job, revision); err != nil {
			return nil, errors.Join(attemptErr, err)
		}
		return nil, attemptErr
	}
	result, _, _, completeErr := worker.complete(ctx, job, reservation, now)
	if completeErr != nil {
		return nil, completeErr
	}
	return result, nil
}

func validateClaimedBooking(job *clientpb.Monitor, preset *clientpb.Preset, theater *catalogpb.Theater, auditorium *catalogpb.Auditorium, showtime *catalogpb.Showtime, now time.Time) error {
	if err := validateClaimedMonitorContext(job, preset, theater, auditorium); err != nil {
		return err
	}
	if err := validateClaimedShowtimeContext(job, theater, auditorium, showtime); err != nil {
		return err
	}
	return validateClaimedSchedule(job, showtime, now)
}

func validateClaimedMonitorContext(job *clientpb.Monitor, preset *clientpb.Preset, theater *catalogpb.Theater, auditorium *catalogpb.Auditorium) error {
	state := monitorStateName(job)
	if state != "pending" && state != "running" {
		return ErrConflict
	}
	if preset == nil || preset.GetId() == "" || preset.GetId() != job.GetPresetId() || theater == nil || auditorium == nil ||
		preset.GetTheaterId() != theater.GetId() || preset.GetAuditoriumId() != auditorium.GetId() {
		return errors.New("claimed booking context does not match the monitor preset")
	}
	return nil
}

func validateClaimedShowtimeContext(job *clientpb.Monitor, theater *catalogpb.Theater, auditorium *catalogpb.Auditorium, showtime *catalogpb.Showtime) error {
	if theater == nil || auditorium == nil || showtime == nil || showtime.GetMovie() == nil || showtime.GetAuditorium() == nil {
		return errors.New("claimed booking context is incomplete")
	}
	if !claimedShowtimeIdentityMatches(job, theater, auditorium, showtime) {
		return errors.New("claimed showtime does not match the monitor")
	}
	if showtime.GetSoldOut() || showtime.GetAvailableSeats() < 1 || showtime.GetCapacity() < showtime.GetAvailableSeats() {
		return ErrBookingNotOpen
	}
	return nil
}

func claimedShowtimeIdentityMatches(
	job *clientpb.Monitor,
	theater *catalogpb.Theater,
	auditorium *catalogpb.Auditorium,
	showtime *catalogpb.Showtime,
) bool {
	movieID := strings.TrimSpace(job.GetMovieId())
	return showtime.GetId() != "" &&
		strings.TrimSpace(showtime.GetProviderId()) != "" &&
		strings.TrimSpace(showtime.GetSourceKey()) != "" &&
		movieID != "" &&
		strings.TrimSpace(showtime.GetMovie().GetId()) == movieID &&
		showtime.GetAuditorium().GetId() == auditorium.GetId() &&
		showtime.GetTheaterId() == theater.GetId()
}

func validateClaimedSchedule(job *clientpb.Monitor, showtime *catalogpb.Showtime, now time.Time) error {
	if showtime == nil || showtime.GetStartsAt() == nil || showtime.GetEndsAt() == nil {
		return errors.New("claimed showtime schedule is incomplete")
	}
	internalShowtime := showtimeDomainFromProto(showtime)
	if internalShowtime.Date == "" || !slices.Contains(monitorResolveTargetDates(job, now), internalShowtime.Date) ||
		!(domain.ScheduleWindow{
			Weekdays: int32Values(job.GetTargetWeekdays()),
			Earliest: localTimeValue(job.GetEarliestTime()),
			Latest:   localTimeValue(job.GetLatestTime()),
		}.MatchesShowtime(internalShowtime)) {
		return errors.New("claimed showtime is outside the monitor schedule")
	}
	return nil
}

func (worker *BookingWorker) startMonitor(ctx context.Context, monitorID string) (*clientpb.Monitor, error) {
	resource, err := worker.monitors.AcquireMonitor(
		ctx, monitorID, worker.workerID, worker.clock.Now(), monitorLeaseTTL,
	)
	if err != nil {
		return nil, err
	}
	job, revision, err := monitorMessage(resource)
	if err != nil {
		return nil, err
	}
	job = cloneMonitor(job)
	monitorTransition(job, "running", worker.clock.Now())
	if err := worker.putMonitor(ctx, job, revision); err != nil {
		return nil, err
	}
	return job, nil
}

func (worker *BookingWorker) stopReason(ctx context.Context, job *clientpb.Monitor) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if monitorIsExpired(job, worker.clock.Now()) {
		return ErrMonitorExpired
	}
	return nil
}

func (worker *BookingWorker) handleAttempt(
	ctx context.Context,
	job *clientpb.Monitor,
	reservation *clientpb.Resource,
	attemptErr error,
	backoff time.Duration,
) (*clientpb.Resource, bool, time.Duration, error) {
	now := worker.clock.Now()
	monitorRecordCheck(job, now, attemptErr)
	if attemptErr == nil {
		return worker.complete(ctx, job, reservation, now)
	}
	if errors.Is(attemptErr, ErrBookingNotOpen) && monitorModeIsCancellation(job) {
		return nil, true, backoff, worker.fail(ctx, job, attemptErr)
	}
	if err := worker.putMonitor(ctx, job, 0); err != nil {
		return nil, true, backoff, err
	}
	if errors.Is(attemptErr, ErrBookingNotOpen) || errors.Is(attemptErr, ErrSeatUnavailable) {
		return nil, false, monitorPollInterval(job), nil
	}
	return nil, false, min(backoff*2, 30*time.Second), nil
}

func (worker *BookingWorker) complete(
	ctx context.Context,
	job *clientpb.Monitor,
	reservation *clientpb.Resource,
	now time.Time,
) (*clientpb.Resource, bool, time.Duration, error) {
	if reservation == nil || reservation.GetReservation() == nil {
		return nil, true, 0, errors.New("reservation resource is required")
	}
	job.SetReservationId(reservation.GetReservation().GetId())
	status := "booked"
	if reservation.GetReservation().HasPrepared() {
		status = "triggered"
	}
	monitorTransition(job, status, now)
	if err := worker.putMonitor(ctx, job, 0); err != nil {
		return nil, true, 0, err
	}
	return reservation, true, 0, nil
}

func (worker *BookingWorker) stop(ctx context.Context, job *clientpb.Monitor, cause error) error {
	if errors.Is(cause, context.Canceled) || errors.Is(cause, context.DeadlineExceeded) {
		monitorTransition(job, "stopped", worker.clock.Now())
		_ = worker.putMonitor(context.WithoutCancel(ctx), job, 0)
		return cause
	}
	return worker.fail(ctx, job, cause)
}

func (worker *BookingWorker) stopAfterWait(ctx context.Context, job *clientpb.Monitor, cause error) error {
	if ctx.Err() != nil {
		monitorTransition(job, "stopped", worker.clock.Now())
		_ = worker.putMonitor(context.WithoutCancel(ctx), job, 0)
	}
	return cause
}

func (worker *BookingWorker) attempt(
	ctx context.Context,
	job *clientpb.Monitor,
	preset *clientpb.Preset,
	theater *catalogpb.Theater,
	auditorium *catalogpb.Auditorium,
) (*clientpb.Resource, error) {
	showtimes, err := worker.showtimes.FindShowtimes(
		ctx,
		theater,
		auditorium,
		job.GetMovieId(),
		monitorResolveTargetDates(job, worker.clock.Now()),
		job.GetTargetWeekdays(),
		job.GetEarliestTime(),
		job.GetLatestTime(),
	)
	if err != nil {
		return nil, err
	}
	showtime, ok := bestShowtime(showtimes)
	if !ok {
		return nil, ErrBookingNotOpen
	}
	return worker.attemptShowtime(ctx, job, preset, showtime)
}

func (worker *BookingWorker) attemptShowtime(
	ctx context.Context,
	job *clientpb.Monitor,
	preset *clientpb.Preset,
	showtime *catalogpb.Showtime,
) (*clientpb.Resource, error) {
	snapshot, available, err := worker.booking.OpenSeatSelection(ctx, showtime, int(preset.GetSeatCount()))
	if err != nil {
		return nil, err
	}
	return worker.prepareSeatSelection(ctx, job, preset, showtime, snapshot, available)
}

func (worker *BookingWorker) attemptClaimedShowtime(
	ctx context.Context,
	job *clientpb.Monitor,
	preset *clientpb.Preset,
	showtime *catalogpb.Showtime,
) (*clientpb.Resource, error) {
	reservation, err := worker.attemptShowtime(ctx, job, preset, showtime)
	if !errors.Is(err, ErrSeatUnavailable) {
		return reservation, err
	}
	refresher, ok := worker.booking.(LiveSeatSelectionRefresher)
	policy := worker.claimedWatch
	if !ok || policy.Window <= 0 {
		return nil, ErrSeatUnavailable
	}

	startedAt := worker.clock.Now()
	deadline := startedAt.Add(policy.Window)
	for refresh := 0; refresh < policy.RefreshLimit; refresh++ {
		now := worker.clock.Now()
		if !now.Before(deadline) {
			break
		}
		delay := policy.MinInterval + worker.jitter(policy.MaxInterval-policy.MinInterval)
		remaining := deadline.Sub(now)
		if delay > remaining {
			delay = remaining
		}
		if waitErr := worker.waiter.Wait(ctx, delay); waitErr != nil {
			return nil, waitErr
		}
		snapshot, available, refreshErr := refresher.RefreshSeatSelection(ctx, showtime)
		if refreshErr != nil {
			return nil, refreshErr
		}
		reservation, refreshErr = worker.prepareSeatSelection(ctx, job, preset, showtime, snapshot, available)
		if !errors.Is(refreshErr, ErrSeatUnavailable) {
			return reservation, refreshErr
		}
	}
	return nil, ErrSeatUnavailable
}

func (worker *BookingWorker) prepareSeatSelection(
	ctx context.Context,
	job *clientpb.Monitor,
	preset *clientpb.Preset,
	showtime *catalogpb.Showtime,
	snapshot *seatmappb.Snapshot,
	available []*seatmappb.Seat,
) (*clientpb.Resource, error) {
	selection := seatSelectionForRanking(snapshot, available)
	preference := seatPreferenceForRanking(preset.GetSeatPreference())
	ranked, err := worker.ranker.Rank(
		selection.SeatMap, selection.LiveSeats, int(preset.GetSeatCount()), preference,
	)
	if err != nil || len(ranked) == 0 {
		return nil, ErrSeatUnavailable
	}
	labels := seatLabels(ranked[0].Seats)
	draft, err := worker.booking.PreparePayment(ctx, showtime, labels)
	if err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	reservationID, userID, monitorID := worker.ids.NewID(), job.GetUserId(), job.GetId()
	reservation := clientpb.Reservation_builder{
		Id: &reservationID, UserId: &userID, MonitorId: &monitorID,
		SeatLabels: append([]string(nil), draft.GetSeatLabels()...), TotalPrice: stringPointer(draft.GetTotalPrice()),
		Prepared: clientpb.ReservationPrepared_builder{}.Build(), Showtime: draft.GetShowtime(),
	}.Build()
	resource := resourceForReservation(reservation, 0)
	return resource, worker.reservations.PutReservation(ctx, resource)
}

func (worker *BookingWorker) loadBookingContext(
	ctx context.Context,
	job *clientpb.Monitor,
) (*clientpb.Preset, *catalogpb.Theater, *catalogpb.Auditorium, error) {
	presetResource, err := worker.presets.GetPreset(ctx, job.GetPresetId())
	if err != nil {
		return nil, nil, nil, err
	}
	preset, _, err := presetMessage(presetResource)
	if err != nil {
		return nil, nil, nil, err
	}
	theaterMessage, err := worker.theaters.GetTheater(ctx, preset.GetTheaterId())
	if err != nil {
		return nil, nil, nil, err
	}
	auditoriumMessage, err := worker.auditoriums.GetAuditorium(ctx, preset.GetAuditoriumId())
	if err != nil {
		return nil, nil, nil, err
	}
	return preset, theaterMessage, auditoriumMessage, nil
}

func (worker *BookingWorker) fail(ctx context.Context, job *clientpb.Monitor, cause error) error {
	now := worker.clock.Now()
	monitorTransition(job, "failed", now)
	setMonitorFailure(job, cause.Error())
	if err := worker.putMonitor(ctx, job, 0); err != nil {
		return errors.Join(cause, err)
	}
	return cause
}

func (worker *BookingWorker) putMonitor(ctx context.Context, job *clientpb.Monitor, revision int64) error {
	if job == nil {
		return errors.New("monitor is required")
	}
	return worker.monitors.PutMonitor(ctx, resourceForMonitor(job, revision))
}

func bestShowtime(showtimes []*catalogpb.Showtime) (*catalogpb.Showtime, bool) {
	available := make([]*catalogpb.Showtime, 0, len(showtimes))
	for _, showtime := range showtimes {
		if showtime != nil && !showtime.GetSoldOut() {
			available = append(available, showtime)
		}
	}
	if len(available) == 0 {
		return nil, false
	}
	sort.Slice(available, func(i, j int) bool {
		if available[i].GetStartsAt() == nil || available[j].GetStartsAt() == nil {
			return available[i].GetStartsAt() != nil
		}
		return available[i].GetStartsAt().AsTime().Before(available[j].GetStartsAt().AsTime())
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

func pollJitterRange(job *clientpb.Monitor, backoff time.Duration) time.Duration {
	interval := monitorPollInterval(job)
	if backoff == interval {
		return monitorPollIntervalMax(job) - interval
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
