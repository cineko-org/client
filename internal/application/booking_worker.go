package application

import (
	"context"
	cryptorand "crypto/rand"
	"errors"
	"io"
	"math/big"
	"slices"
	"strings"
	"time"

	"github.com/cineko-org/client/internal/domain"
	catalogpb "github.com/cineko-org/contracts/gen/go/cineko/catalog"
	clientpb "github.com/cineko-org/contracts/gen/go/cineko/client"
	seatmappb "github.com/cineko-org/contracts/gen/go/cineko/seatmap"
)

type BookingWorker struct {
	monitors     MonitorRepository
	reservations ReservationRepository
	booking      BookingGateway
	ranker       domain.SeatRanker
	ids          IDGenerator
	clock        Clock
	waiter       Waiter
	jitter       func(time.Duration) time.Duration
	claimedWatch ClaimedSeatWatchPolicy
}

type BookingWorkerDependencies struct {
	Monitors     MonitorRepository
	Reservations ReservationRepository
	Booking      BookingGateway
	IDs          IDGenerator
	Clock        Clock
	Waiter       Waiter
	Jitter       func(time.Duration) time.Duration
	ClaimedWatch ClaimedSeatWatchPolicy
}

func NewBookingWorker(dependencies BookingWorkerDependencies) *BookingWorker {
	jitter := dependencies.Jitter
	if jitter == nil {
		jitter = randomJitter
	}
	return &BookingWorker{
		monitors:     dependencies.Monitors,
		reservations: dependencies.Reservations,
		booking:      dependencies.Booking,
		ids:          dependencies.IDs, clock: dependencies.Clock, waiter: dependencies.Waiter,
		jitter:       jitter,
		claimedWatch: dependencies.ClaimedWatch.normalized(),
	}
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
	if revision, err = worker.putMonitor(ctx, job, revision); err != nil {
		return nil, err
	}
	reservation, attemptErr := worker.attemptClaimedShowtime(ctx, job, preset, showtimeMessage)
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	now := worker.clock.Now()
	monitorRecordCheck(job, now)
	if attemptErr != nil {
		// Central owns retry/exhaustion for execution commands. Keep the monitor
		// eligible while Central decides whether this failed lease is retried.
		if _, err := worker.putMonitor(ctx, job, revision); err != nil {
			return nil, errors.Join(attemptErr, err)
		}
		return nil, attemptErr
	}
	result, completeErr := worker.complete(ctx, job, reservation, now, revision)
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

func (worker *BookingWorker) complete(
	ctx context.Context,
	job *clientpb.Monitor,
	reservation *clientpb.Resource,
	now time.Time,
	revision int64,
) (*clientpb.Resource, error) {
	if reservation == nil || reservation.GetReservation() == nil {
		return nil, errors.New("reservation resource is required")
	}
	job.SetReservationId(reservation.GetReservation().GetId())
	status := "booked"
	if reservation.GetReservation().HasPrepared() {
		status = "triggered"
	}
	monitorTransition(job, status, now)
	if _, err := worker.putMonitor(ctx, job, revision); err != nil {
		return nil, err
	}
	return reservation, nil
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

func (worker *BookingWorker) putMonitor(ctx context.Context, job *clientpb.Monitor, revision int64) (int64, error) {
	if job == nil {
		return revision, errors.New("monitor is required")
	}
	resource := resourceForMonitor(job, revision)
	if err := worker.monitors.PutMonitor(ctx, resource); err != nil {
		return revision, err
	}
	return resource.GetIdentity().GetRevision(), nil
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
