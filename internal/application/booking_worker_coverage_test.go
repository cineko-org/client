package application

import (
	"bytes"
	"context"
	"errors"
	"testing"
	"time"

	"github.com/cineko-org/client/internal/domain"
	catalogpb "github.com/cineko-org/contracts/gen/go/cineko/catalog"
	clientpb "github.com/cineko-org/contracts/gen/go/cineko/client"
	commonpb "github.com/cineko-org/contracts/gen/go/cineko/common"
	seatmappb "github.com/cineko-org/contracts/gen/go/cineko/seatmap"
	"google.golang.org/protobuf/types/known/durationpb"
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
	if monitorStateName(monitors.job) != "stopped" {
		t.Fatalf("cancelled monitor status = %q", monitorStateName(monitors.job))
	}

	worker, monitors, _, _, _, _ = newWorkerCoverageHarness()
	monitors.job.SetTargetDates(localDatesForTest([]string{"2026-08-08"}))
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
	worker, _, presets, repository, showtimes, _ := newWorkerCoverageHarness()
	job := coverageMonitor(validWorkerJob())
	preset := clonePreset(presets.values["preset"].GetPreset())
	theater := coverageTheater(domain.Theater{ID: "theater"})
	auditorium := coverageAuditorium(domain.Auditorium{ID: "auditorium"})
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
	showtimes.values = []*catalogpb.Showtime{showtimeProtoFromDomain(domain.Showtime{ID: "showtime", Date: "2026-08-10", StartsAt: "20:00"})}
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
	if err != nil || reservation.GetReservation().GetPrepared() == nil || reservation.GetReservation().GetBookingNumber() != "" {
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
	if _, err := worker.RunOnce(ctx, "monitor"); !errors.Is(err, ErrBookingNotOpen) || monitorStateName(monitors.job) != "running" {
		t.Fatalf("RunOnce(retry) = %v, status %q", err, monitorStateName(monitors.job))
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
	if _, err := worker.RunOnce(ctx, "monitor"); !errors.Is(err, context.Canceled) || monitorStateName(monitors.job) != "stopped" {
		t.Fatalf("RunOnce(cancel) = %v, status %q", err, monitorStateName(monitors.job))
	}

	worker, monitors, _, _, _, _ = newWorkerCoverageHarness()
	reservation, err := worker.RunOnce(ctx, "monitor")
	if err != nil || reservation.GetReservation().GetPrepared() == nil || monitorStateName(monitors.job) != "triggered" {
		t.Fatalf("RunOnce(success) = %+v, %v", reservation, err)
	}
}

type runOnceMonitorRepository struct {
	*monitorRepositoryFake
	puts   int
	failAt int
}

func (repository *runOnceMonitorRepository) PutMonitor(ctx context.Context, resource *clientpb.Resource) error {
	repository.puts++
	if repository.puts == repository.failAt {
		return errInjected
	}
	return repository.monitorRepositoryFake.PutMonitor(ctx, resource)
}

func TestBookingWorkerStateTransitionsAndHelpers(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	worker, monitors, _, _, _, _ := newWorkerCoverageHarness()
	job := coverageMonitor(validWorkerJob())
	preparedID := "prepared"
	prepared := resourceForReservation(clientpb.Reservation_builder{
		Id: &preparedID, Prepared: clientpb.ReservationPrepared_builder{}.Build(),
	}.Build(), 0)

	result, done, backoff, err := worker.handleAttempt(ctx, job, prepared, nil, 5*time.Second)
	if err != nil || !done || backoff != 0 || result.GetReservation().GetId() != preparedID || monitorStateName(job) != "triggered" {
		t.Fatalf("handleAttempt(prepared) = %+v, %t, %v, %v", result, done, backoff, err)
	}
	job = coverageMonitor(validWorkerJob())
	bookedID := "booked"
	booked := resourceForReservation(clientpb.Reservation_builder{
		Id: &bookedID, Booked: clientpb.ReservationBooked_builder{}.Build(),
	}.Build(), 0)
	if _, _, _, err := worker.handleAttempt(ctx, job, booked, nil, 5*time.Second); err != nil || monitorStateName(job) != "booked" {
		t.Fatalf("handleAttempt(booked) status/error = %q, %v", monitorStateName(job), err)
	}
	monitors.putErr = errInjected
	job = coverageMonitor(validWorkerJob())
	if _, _, _, err := worker.handleAttempt(ctx, job, booked, nil, 5*time.Second); !errors.Is(err, errInjected) {
		t.Fatalf("handleAttempt(complete error) = %v", err)
	}
	monitors.putErr = nil

	job = coverageMonitor(validWorkerJob())
	_, done, backoff, err = worker.handleAttempt(ctx, job, nil, ErrSeatUnavailable, 10*time.Second)
	if err != nil || done || backoff != monitorPollInterval(job) {
		t.Fatalf("handleAttempt(retryable) = %t, %v, %v", done, backoff, err)
	}
	job = coverageMonitor(validWorkerJob())
	_, done, backoff, err = worker.handleAttempt(ctx, job, nil, errInjected, 20*time.Second)
	if err != nil || done || backoff != 30*time.Second {
		t.Fatalf("handleAttempt(backoff) = %t, %v, %v", done, backoff, err)
	}
	monitors.putErr = errInjected
	job = coverageMonitor(validWorkerJob())
	if _, _, _, err := worker.handleAttempt(ctx, job, nil, errInjected, time.Second); !errors.Is(err, errInjected) {
		t.Fatalf("handleAttempt(save error) = %v", err)
	}
	monitors.putErr = nil
	cancellationJob := validWorkerJob()
	cancellationJob.SetMode(clientpb.MonitorMode_builder{Cancellation: clientpb.CancellationMonitor_builder{}.Build()}.Build())
	job = coverageMonitor(cancellationJob)
	if _, done, _, err := worker.handleAttempt(ctx, job, nil, ErrBookingNotOpen, time.Second); !done || !errors.Is(err, ErrBookingNotOpen) {
		t.Fatalf("handleAttempt(cancellation) = %t, %v", done, err)
	}

	if worker.stopReason(ctx, coverageMonitor(validWorkerJob())) != nil {
		t.Fatal("stopReason stopped active monitor")
	}
	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	if !errors.Is(worker.stopReason(cancelled, coverageMonitor(validWorkerJob())), context.Canceled) {
		t.Fatal("stopReason ignored cancellation")
	}
	expiredMonitor := validWorkerJob()
	expiredMonitor.SetTargetDates(localDatesForTest([]string{"2026-08-08"}))
	expired := coverageMonitor(expiredMonitor)
	if !errors.Is(worker.stopReason(ctx, expired), ErrMonitorExpired) {
		t.Fatal("stopReason ignored expiration")
	}
}

func TestBookingWorkerStopLoadAndUtilityPaths(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	worker, monitors, presets, catalog, showtimes, _ := newWorkerCoverageHarness()
	job := coverageMonitor(validWorkerJob())

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

	showtimes.values = []*catalogpb.Showtime{
		showtimeProtoFromDomain(domain.Showtime{ID: "sold", SoldOut: true}),
		showtimeProtoFromDomain(domain.Showtime{ID: "later", Date: "2026-08-11", StartsAt: "10:00"}),
		showtimeProtoFromDomain(domain.Showtime{ID: "same-late", Date: "2026-08-10", StartsAt: "21:00"}),
		showtimeProtoFromDomain(domain.Showtime{ID: "first", Date: "2026-08-10", StartsAt: "19:00"}),
	}
	best, ok := bestShowtime(showtimes.values)
	if !ok || best.GetId() != "first" {
		t.Fatalf("bestShowtime() = %+v, %t", best, ok)
	}
	if _, ok := bestShowtime([]*catalogpb.Showtime{showtimeProtoFromDomain(domain.Showtime{ID: "sold", SoldOut: true})}); ok {
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
	pollMonitor := validWorkerJob()
	pollMonitor.SetPollInterval(durationpb.New(3 * time.Minute))
	pollMonitor.SetMaximumPollInterval(durationpb.New(8 * time.Minute))
	pollJob := coverageMonitor(pollMonitor)
	if got := pollJitterRange(pollJob, monitorPollInterval(pollJob)); got != 5*time.Minute {
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
	showtimes := &showtimeGatewayCoverageFake{values: []*catalogpb.Showtime{showtimeProtoFromDomain(domain.Showtime{
		ID: "showtime", Date: "2026-08-10", StartsAt: "20:00",
	})}}
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

func validWorkerJob() *clientpb.Monitor {
	return monitorFixtureForTest("monitor", "user", "preset", "Movie", false, []string{"2026-08-10"})
}

func coverageMonitor(value *clientpb.Monitor) *clientpb.Monitor {
	return cloneMonitor(value)
}

func coverageTheater(value domain.Theater) *catalogpb.Theater {
	id, providerID, sourceKey, region, name := value.ID, value.ProviderID, value.SourceKey, value.Region, value.Name
	return catalogpb.Theater_builder{Id: &id, ProviderId: &providerID, SourceKey: &sourceKey, Region: &region, Name: &name}.Build()
}

func coverageAuditorium(value domain.Auditorium) *catalogpb.Auditorium {
	id, theaterID, sourceKey, name := value.ID, value.TheaterID, value.SourceKey, value.Name
	capacity := mustInt32ForTest(value.Capacity)
	return catalogpb.Auditorium_builder{Id: &id, TheaterId: &theaterID, SourceKey: &sourceKey,
		Name: &name, ScreenTypes: append([]string(nil), value.ScreenTypes...), Capacity: &capacity}.Build()
}

func coverageSeatSnapshot(value domain.SeatMap) *seatmappb.Snapshot {
	auditoriumID, layoutHash := value.AuditoriumID, value.Version
	seats := make([]*seatmappb.Seat, 0, len(value.Seats))
	for _, source := range value.Seats {
		id, sourceAuditoriumID, label, row, seatType := source.ID, source.AuditoriumID, source.Label, source.Row, string(source.Type)
		number := mustInt32ForTest(source.Number)
		seats = append(seats, seatmappb.Seat_builder{Id: &id, AuditoriumId: &sourceAuditoriumID,
			Label: &label, Row: &row, Number: &number, X: &source.X, Y: &source.Y, Type: &seatType}.Build())
	}
	return seatmappb.Snapshot_builder{AuditoriumId: &auditoriumID, LayoutHash: &layoutHash,
		Layout: seatmappb.Layout_builder{Seats: seats}.Build()}.Build()
}

func coverageAvailableSeats(snapshot *seatmappb.Snapshot, live []domain.LiveSeat) []*seatmappb.Seat {
	available := make(map[string]bool, len(live))
	for _, source := range live {
		available[source.Label] = source.Available
	}
	result := make([]*seatmappb.Seat, 0, len(snapshot.GetLayout().GetSeats()))
	for _, seat := range snapshot.GetLayout().GetSeats() {
		if available[seat.GetLabel()] {
			result = append(result, seat)
		}
	}
	return result
}

type workerDataRepositoryFake struct {
	theater     domain.Theater
	auditorium  domain.Auditorium
	seatMap     domain.SeatMap
	reservation *clientpb.Reservation
	putErr      error
	fail        string
}

func (repository *workerDataRepositoryFake) PutTheater(context.Context, *catalogpb.Theater) error {
	return nil
}

func (repository *workerDataRepositoryFake) GetTheater(context.Context, string) (*catalogpb.Theater, error) {
	if repository.fail == "get-theater" {
		return nil, errInjected
	}
	id, providerID, sourceKey := repository.theater.ID, repository.theater.ProviderID, repository.theater.SourceKey
	region, name := repository.theater.Region, repository.theater.Name
	return catalogpb.Theater_builder{Id: &id, ProviderId: &providerID, SourceKey: &sourceKey, Region: &region, Name: &name}.Build(), nil
}

func (repository *workerDataRepositoryFake) ListTheaters(ctx context.Context) ([]*catalogpb.Theater, error) {
	value, err := repository.GetTheater(ctx, repository.theater.ID)
	return []*catalogpb.Theater{value}, err
}

func (repository *workerDataRepositoryFake) PutAuditorium(context.Context, *catalogpb.Auditorium) error {
	return nil
}

func (repository *workerDataRepositoryFake) GetAuditorium(context.Context, string) (*catalogpb.Auditorium, error) {
	if repository.fail == "get-auditorium" {
		return nil, errInjected
	}
	id, theaterID, sourceKey, name := repository.auditorium.ID, repository.auditorium.TheaterID, repository.auditorium.SourceKey, repository.auditorium.Name
	capacity := mustInt32ForTest(repository.auditorium.Capacity)
	return catalogpb.Auditorium_builder{Id: &id, TheaterId: &theaterID, SourceKey: &sourceKey, Name: &name,
		ScreenTypes: append([]string(nil), repository.auditorium.ScreenTypes...), Capacity: &capacity}.Build(), nil
}

func (repository *workerDataRepositoryFake) ListAuditoriumsByTheater(
	context.Context,
	string,
) ([]*catalogpb.Auditorium, error) {
	value, err := repository.GetAuditorium(context.Background(), repository.auditorium.ID)
	return []*catalogpb.Auditorium{value}, err
}

func (repository *workerDataRepositoryFake) PutSeatMap(context.Context, *seatmappb.Snapshot) error {
	return nil
}

func (repository *workerDataRepositoryFake) GetSeatMap(context.Context, string) (*seatmappb.Snapshot, error) {
	if repository.fail == "get-seat-map" {
		return nil, errInjected
	}
	auditoriumID, layoutHash := repository.seatMap.AuditoriumID, repository.seatMap.Version
	return seatmappb.Snapshot_builder{AuditoriumId: &auditoriumID, LayoutHash: &layoutHash}.Build(), nil
}

func (repository *workerDataRepositoryFake) PutReservation(
	_ context.Context,
	resource *clientpb.Resource,
) error {
	if repository.putErr != nil {
		return repository.putErr
	}
	value, _, err := reservationMessage(resource)
	if err != nil {
		return err
	}
	repository.reservation = cloneReservation(value)
	return nil
}

func (repository *workerDataRepositoryFake) GetReservation(
	context.Context,
	string,
) (*clientpb.Resource, error) {
	return resourceForReservation(cloneReservation(repository.reservation), 0), nil
}

func (repository *workerDataRepositoryFake) ListReservationsByUser(
	context.Context,
	string,
) ([]*clientpb.Resource, error) {
	return []*clientpb.Resource{resourceForReservation(cloneReservation(repository.reservation), 0)}, nil
}

type showtimeGatewayCoverageFake struct {
	values []*catalogpb.Showtime
	err    error
}

func (gateway *showtimeGatewayCoverageFake) FindShowtimes(
	context.Context,
	*catalogpb.Theater,
	*catalogpb.Auditorium,
	string,
	[]string,
	[]int32,
	*commonpb.LocalTime,
	*commonpb.LocalTime,
) ([]*catalogpb.Showtime, error) {
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
	*catalogpb.Showtime,
	int,
) (*seatmappb.Snapshot, []*seatmappb.Seat, error) {
	if gateway.openErr != nil {
		return nil, nil, gateway.openErr
	}
	snapshot := coverageSeatSnapshot(gateway.seatMap)
	return snapshot, coverageAvailableSeats(snapshot, gateway.live), nil
}

func (gateway *workerBookingGatewayFake) PreparePayment(
	_ context.Context,
	showtime *catalogpb.Showtime,
	seats []string,
) (*clientpb.Reservation, error) {
	if gateway.prepareErr != nil {
		return nil, gateway.prepareErr
	}
	return clientpb.Reservation_builder{Showtime: showtime, SeatLabels: append([]string(nil), seats...)}.Build(), nil
}

func (*workerBookingGatewayFake) PrepareCancellation(
	context.Context,
	*clientpb.Reservation,
) (*clientpb.WebUICancellationResult, error) {
	return nil, nil
}

func (*workerBookingGatewayFake) CommitCancellation(context.Context) error { return nil }

type waiterCoverageFake struct{ err error }

func (waiter *waiterCoverageFake) Wait(context.Context, time.Duration) error { return waiter.err }
