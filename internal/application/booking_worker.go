package application

import (
	"context"
	cryptorand "crypto/rand"
	"errors"
	"fmt"
	"io"
	"math/big"
	"strings"
	"time"

	"buf.build/go/protovalidate"
	"github.com/cineko-org/client/internal/domain"
	catalogpb "github.com/cineko-org/contracts/v3/gen/go/cineko/catalog"
	clientpb "github.com/cineko-org/contracts/v3/gen/go/cineko/client"
	observationpb "github.com/cineko-org/contracts/v3/gen/go/cineko/observation"
	seatmappb "github.com/cineko-org/contracts/v3/gen/go/cineko/seatmap"
)

type BookingWorker struct {
	monitors      MonitorRepository
	reservations  ReservationRepository
	booking       BookingGateway
	observations  LiveSeatObservationRepository
	ranker        domain.SeatRanker
	ids           IDGenerator
	clock         Clock
	waiter        Waiter
	jitter        func(time.Duration) time.Duration
	claimedWatch  ClaimedSeatWatchPolicy
	reportTimeout time.Duration
}

const defaultLiveSeatReportTimeout = 2 * time.Second

type BookingWorkerDependencies struct {
	Monitors      MonitorRepository
	Reservations  ReservationRepository
	Booking       BookingGateway
	Observations  LiveSeatObservationRepository
	IDs           IDGenerator
	Clock         Clock
	Waiter        Waiter
	Jitter        func(time.Duration) time.Duration
	ClaimedWatch  ClaimedSeatWatchPolicy
	ReportTimeout time.Duration
}

func NewBookingWorker(dependencies BookingWorkerDependencies) *BookingWorker {
	jitter := dependencies.Jitter
	if jitter == nil {
		jitter = randomJitter
	}
	reportTimeout := dependencies.ReportTimeout
	if reportTimeout <= 0 {
		reportTimeout = defaultLiveSeatReportTimeout
	}
	return &BookingWorker{
		monitors:     dependencies.Monitors,
		reservations: dependencies.Reservations,
		booking:      dependencies.Booking,
		observations: dependencies.Observations,
		ids:          dependencies.IDs, clock: dependencies.Clock, waiter: dependencies.Waiter,
		jitter:        jitter,
		claimedWatch:  dependencies.ClaimedWatch.normalized(),
		reportTimeout: reportTimeout,
	}
}

// RunClaimedShowtime prepares one exact locally selected showtime.
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
	reservation, attemptErr := worker.attemptClaimedShowtime(
		ctx, job, preset, theaterMessage, auditoriumMessage, showtimeMessage,
	)
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	now := worker.clock.Now()
	monitorRecordCheck(job, now)
	if attemptErr != nil {
		// Keep the monitor eligible so the local supervisor can retry the target.
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

// WatchClaimedShowtime keeps one exact authenticated seat page alive without
// letting parallel watcher tabs race the monitor revision. Only the tab that
// actually prepares a provider hold performs the durable monitor transition.
func (worker *BookingWorker) WatchClaimedShowtime(
	ctx context.Context,
	monitorResource *clientpb.Resource,
	presetResource *clientpb.Resource,
	theaterMessage *catalogpb.Theater,
	auditoriumMessage *catalogpb.Auditorium,
	showtimeMessage *catalogpb.Showtime,
) (*clientpb.Resource, error) {
	job, _, err := monitorMessage(monitorResource)
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
	reservation, err := worker.attemptClaimedShowtime(
		ctx, job, preset, theaterMessage, auditoriumMessage, showtimeMessage,
	)
	if err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	latest, err := worker.monitors.GetMonitor(ctx, job.GetId())
	if err != nil {
		return nil, err
	}
	latestJob, revision, err := monitorMessage(latest)
	if err != nil {
		return nil, err
	}
	state := monitorStateName(latestJob)
	if state != "pending" && state != "running" {
		return nil, ErrConflict
	}
	return worker.complete(ctx, latestJob, reservation, worker.clock.Now(), revision)
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
	// Catalog availability is only a discovery hint. Zero seats and a sold-out
	// flag must still reach the authenticated browser because cancellation seats
	// can appear after the local monitor selected the showtime. Impossible counts still fail
	// closed before any provider interaction.
	if showtime.GetAvailableSeats() < 0 || showtime.GetCapacity() < showtime.GetAvailableSeats() {
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
	identity := showtime.GetIdentity().GetCgv()
	return showtime.GetId() != "" &&
		strings.TrimSpace(showtime.GetProviderId()) != "" &&
		identity != nil &&
		strings.TrimSpace(identity.GetSiteNo()) != "" &&
		identity.GetScheduleDate() != nil &&
		strings.TrimSpace(identity.GetScreenNo()) != "" &&
		strings.TrimSpace(identity.GetSequence()) != "" &&
		movieID != "" &&
		strings.TrimSpace(showtime.GetMovie().GetId()) == movieID &&
		showtime.GetAuditorium().GetId() == auditorium.GetId() &&
		showtime.GetTheaterId() == theater.GetId()
}

func validateClaimedSchedule(job *clientpb.Monitor, showtime *catalogpb.Showtime, now time.Time) error {
	if showtime == nil || showtime.GetStartsAt() == nil || showtime.GetEndsAt() == nil {
		return errors.New("claimed showtime schedule is incomplete")
	}
	if !showtime.GetStartsAt().AsTime().After(now) {
		return ErrBookingNotOpen
	}
	internalShowtime := showtimeDomainFromProto(showtime)
	if internalShowtime.Date == "" || !(domain.ScheduleWindow{
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

func (worker *BookingWorker) openAndPrepareShowtime(
	ctx context.Context,
	job *clientpb.Monitor,
	preset *clientpb.Preset,
	theater *catalogpb.Theater,
	auditorium *catalogpb.Auditorium,
	showtime *catalogpb.Showtime,
) (*clientpb.Resource, *seatmappb.LiveSeatObservation, error) {
	task := bookingSeatTask(theater, auditorium, showtime)
	observation, err := worker.booking.OpenSeatSelection(ctx, task, int(job.GetSeatCount()))
	if err != nil {
		return nil, nil, err
	}
	reservation, err := worker.prepareSeatSelection(ctx, job, preset, showtime, observation)
	return reservation, observation, err
}

func (worker *BookingWorker) attemptClaimedShowtime(
	ctx context.Context,
	job *clientpb.Monitor,
	preset *clientpb.Preset,
	theater *catalogpb.Theater,
	auditorium *catalogpb.Auditorium,
	showtime *catalogpb.Showtime,
) (*clientpb.Resource, error) {
	reservation, latestUnavailable, err := worker.openAndPrepareShowtime(
		ctx, job, preset, theater, auditorium, showtime,
	)
	if !errors.Is(err, ErrSeatUnavailable) {
		return reservation, err
	}
	if reportErr := worker.submitLiveSeatObservation(ctx, latestUnavailable); reportErr != nil {
		return nil, errors.Join(ErrSeatUnavailable, fmt.Errorf("report live no-seat observation: %w", reportErr))
	}
	refresher, supported := worker.booking.(LiveSeatSelectionRefresher)
	if !supported || worker.claimedWatch == (ClaimedSeatWatchPolicy{}) {
		return nil, ErrSeatUnavailable
	}
	task := bookingSeatTask(theater, auditorium, showtime)
	startedAt := worker.clock.Now()
	for attempt := 1; worker.claimedSeatWatchContinues(attempt, startedAt, showtime); attempt++ {
		delay := worker.jitter(worker.claimedWatch.MaxInterval - worker.claimedWatch.MinInterval)
		if err := worker.waiter.Wait(ctx, worker.claimedWatch.MinInterval+delay); err != nil {
			return nil, err
		}
		observation, refreshErr := refresher.RefreshSeatSelection(ctx, task)
		if refreshErr != nil {
			return nil, refreshErr
		}
		reservation, refreshErr = worker.prepareSeatSelection(ctx, job, preset, showtime, observation)
		if !errors.Is(refreshErr, ErrSeatUnavailable) {
			return reservation, refreshErr
		}
		if reportErr := worker.submitLiveSeatObservation(ctx, observation); reportErr != nil {
			return nil, errors.Join(ErrSeatUnavailable, fmt.Errorf("report refreshed no-seat observation: %w", reportErr))
		}
	}
	return nil, ErrSeatUnavailable
}

func (worker *BookingWorker) claimedSeatWatchContinues(
	attempt int,
	startedAt time.Time,
	showtime *catalogpb.Showtime,
) bool {
	if worker.claimedWatch.UntilShowtime {
		return showtime != nil && showtime.GetStartsAt() != nil && worker.clock.Now().Before(showtime.GetStartsAt().AsTime())
	}
	if attempt > worker.claimedWatch.RefreshLimit {
		return false
	}
	return worker.clock.Now().Before(startedAt.Add(worker.claimedWatch.Window))
}

func bookingSeatTask(
	theater *catalogpb.Theater,
	auditorium *catalogpb.Auditorium,
	showtime *catalogpb.Showtime,
) *observationpb.SeatAvailabilityTask {
	locale, timeZone := "ko-KR", "Asia/Seoul"
	return observationpb.SeatAvailabilityTask_builder{
		Theater: theater, Auditorium: auditorium, Showtime: showtime,
		Locale: &locale, TimeZone: &timeZone,
	}.Build()
}

func (worker *BookingWorker) prepareSeatSelection(
	ctx context.Context,
	job *clientpb.Monitor,
	preset *clientpb.Preset,
	showtime *catalogpb.Showtime,
	observation *seatmappb.LiveSeatObservation,
) (*clientpb.Resource, error) {
	if err := protovalidate.Validate(observation); err != nil {
		return nil, fmt.Errorf("validate live seat observation: %w", err)
	}
	snapshot, available := seatsFromLiveObservation(observation)
	selection := seatSelectionForRanking(snapshot, available)
	preference := seatPreferenceForRanking(preset.GetSeatPreference(), job.GetSeatType())
	ranked, err := worker.ranker.Rank(
		selection.SeatMap, selection.LiveSeats, int(job.GetSeatCount()), preference,
	)
	if err != nil || len(ranked) == 0 {
		return nil, ErrSeatUnavailable
	}
	labels := seatLabels(ranked[0].Seats)
	draft, err := worker.booking.PreparePayment(ctx, showtime, labels)
	if err != nil {
		if strings.Contains(strings.ToLower(err.Error()), "no longer selectable") {
			return nil, ErrSeatUnavailable
		}
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
	if err := worker.reservations.PutReservation(ctx, resource); err != nil {
		return nil, err
	}
	// The provider seat hold is the latency-critical boundary. Report its exact
	// observation only after payment preparation and durable local handoff; a
	// observation write failure must never discard the successful hold.
	_ = worker.submitLiveSeatObservation(ctx, observation)
	return resource, nil
}

// submitLiveSeatObservation makes the live provider read durable under the
// active execution lease after the seat hold has been handed off locally.
func (worker *BookingWorker) submitLiveSeatObservation(
	ctx context.Context,
	observation *seatmappb.LiveSeatObservation,
) error {
	reportContext, cancel := context.WithTimeout(ctx, worker.reportTimeout)
	defer cancel()
	return SubmitLiveSeatObservation(reportContext, worker.observations, observation)
}

// seatsFromLiveObservation keeps the provider layout and availability in one
// generated contract while translating available seat IDs only at the ranker
// boundary. An empty availability list is a valid live observation.
func seatsFromLiveObservation(observation *seatmappb.LiveSeatObservation) (*seatmappb.Snapshot, []*seatmappb.Seat) {
	if observation == nil || observation.GetLayout() == nil || observation.GetAvailability() == nil {
		return nil, nil
	}
	availableIDs := make(map[string]struct{}, len(observation.GetAvailability().GetAvailableSeats()))
	for _, available := range observation.GetAvailability().GetAvailableSeats() {
		if available != nil {
			availableIDs[available.GetSeatId()] = struct{}{}
		}
	}
	seats := make([]*seatmappb.Seat, 0, len(availableIDs))
	for _, seat := range observation.GetLayout().GetLayout().GetSeats() {
		if seat == nil {
			continue
		}
		if _, ok := availableIDs[seat.GetId()]; ok {
			seats = append(seats, seat)
		}
	}
	return observation.GetLayout(), seats
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
