package application

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/cineko-org/client/internal/domain"
	catalogpb "github.com/cineko-org/contracts/v3/gen/go/cineko/catalog"
	clientpb "github.com/cineko-org/contracts/v3/gen/go/cineko/client"
	observationpb "github.com/cineko-org/contracts/v3/gen/go/cineko/observation"
	seatmappb "github.com/cineko-org/contracts/v3/gen/go/cineko/seatmap"
	"google.golang.org/protobuf/types/known/timestamppb"
)

type centralFenceRepository struct {
	*workerRepository
	putCalls     int
	putErrAt     int
	putRevisions []int64
	nextRevision int64
}

func (repository *centralFenceRepository) PutMonitor(_ context.Context, resource *clientpb.Resource) error {
	repository.putCalls++
	if repository.putCalls == repository.putErrAt {
		return errors.New("put monitor failed")
	}
	job, _, err := monitorMessage(resource)
	if err != nil {
		return err
	}
	repository.putRevisions = append(repository.putRevisions, resource.GetIdentity().GetRevision())
	repository.job = cloneMonitor(job)
	if repository.nextRevision > 0 {
		resource.GetIdentity().SetRevision(repository.nextRevision)
		repository.nextRevision++
	}
	return nil
}

type exactShowtimeGateway struct {
	*workerGateway
	opened    *catalogpb.Showtime
	openErr   error
	openCalls int
}

type refreshingShowtimeGateway struct {
	*exactShowtimeGateway
	selections   []domain.SeatSelection
	refreshErr   error
	refreshCalls int
	onRefresh    func(int)
}

func (gateway *refreshingShowtimeGateway) RefreshSeatSelection(
	_ context.Context,
	task *observationpb.SeatAvailabilityTask,
) (*seatmappb.LiveSeatObservation, error) {
	gateway.refreshCalls++
	if gateway.onRefresh != nil {
		gateway.onRefresh(gateway.refreshCalls)
	}
	gateway.opened = task.GetShowtime()
	if gateway.refreshErr != nil {
		return nil, gateway.refreshErr
	}
	index := min(gateway.refreshCalls-1, len(gateway.selections)-1)
	selection := gateway.selections[index]
	snapshot := seatSnapshotForDomain()
	return gatewayLiveObservation(snapshot, selection.LiveSeats), nil
}

func (gateway *exactShowtimeGateway) OpenSeatSelection(
	_ context.Context,
	task *observationpb.SeatAvailabilityTask,
	_ int,
) (*seatmappb.LiveSeatObservation, error) {
	gateway.openCalls++
	gateway.opened = task.GetShowtime()
	if gateway.openErr != nil {
		return nil, gateway.openErr
	}
	snapshot := gatewaySeatSnapshot()
	return gatewayLiveObservation(snapshot, gateway.live), nil
}

func seatSnapshotForDomain() *seatmappb.Snapshot {
	return gatewaySeatSnapshot()
}

func TestRunClaimedShowtimeUsesCentralFenceAndExactPayload(t *testing.T) {
	now := time.Date(2026, time.August, 12, 10, 0, 0, 0, time.UTC)
	base := &workerRepository{
		job:        monitorFixtureForTest("영화", []string{"2026-08-20"}),
		preset:     presetFixtureForTest("preset", "user", "theater", "auditorium", []string{"H10"}),
		theater:    domain.Theater{ID: "theater"},
		auditorium: domain.Auditorium{ID: "auditorium", TheaterID: "theater"},
		seatMap: domain.SeatMap{AuditoriumID: "auditorium", Seats: []domain.Seat{{
			ID: "seat", AuditoriumID: "auditorium", Label: "H10", Row: "H", Number: 10,
			X: .5, Y: .5, Type: domain.SeatTypeStandard,
		}}},
	}
	repository := &centralFenceRepository{workerRepository: base}
	observations := &liveObservationRepositoryFake{}
	gateway := &exactShowtimeGateway{workerGateway: &workerGateway{
		live: []domain.LiveSeat{{Label: "H10", Available: true}},
	}}
	showtime := domain.Showtime{
		ID: "source", ProviderID: "cgv", SourceKey: "0056/2026-08-20/0007/0003", MovieID: "movie_1", Movie: "영화", TheaterID: "theater", AuditoriumID: "auditorium",
		Date: "2026-08-20", StartsAt: "20:00", EndsAt: "22:00",
		AvailableSeats: 10, Capacity: 100,
	}
	worker := NewBookingWorker(BookingWorkerDependencies{
		Monitors: repository, Reservations: repository,
		Booking: gateway, Observations: observations, IDs: &sequenceIDs{},
		Clock: fixedClock{now: now}, Waiter: noWaiter{},
	})
	reservation, err := runClaimedShowtime(t.Context(), worker, base, showtime)
	if err != nil {
		t.Fatal(err)
	}
	if gateway.opened.GetId() != showtime.ID || gateway.opened.GetStartsAt().AsTime().In(domain.KoreaLocation).Format("15:04") != showtime.StartsAt {
		t.Fatalf("opened showtime = %+v", gateway.opened)
	}
	if reservation.GetReservation().GetPrepared() == nil || base.job.GetState().GetTriggered() == nil {
		t.Fatalf("reservation/monitor = %+v / %+v", reservation, base.job)
	}
	if len(observations.requests) != 1 {
		t.Fatalf("live observation requests = %d", len(observations.requests))
	}
	report := observations.requests[0]
	if report.GetAvailability().GetShowtimeId() == "" {
		t.Fatalf("live observation request = %s", report)
	}
}

func TestRunClaimedShowtimeReportsOneShotSoldOutObservation(t *testing.T) {
	now := time.Date(2026, time.August, 12, 10, 0, 0, 0, time.UTC)
	base := claimedSeatWatchRepository()
	observations := &liveObservationRepositoryFake{}
	gateway := &exactShowtimeGateway{workerGateway: &workerGateway{
		live: []domain.LiveSeat{{Label: "H10", Available: false}},
	}}
	worker := NewBookingWorker(BookingWorkerDependencies{
		Monitors: base, Reservations: base, Booking: gateway,
		Observations: observations, IDs: &sequenceIDs{},
		Clock: fixedClock{now: now}, Waiter: noWaiter{},
	})
	_, err := runClaimedShowtime(t.Context(), worker, base, claimedSeatWatchShowtime())
	if !errors.Is(err, ErrSeatUnavailable) {
		t.Fatalf("one-shot sold-out error = %v", err)
	}
	if len(observations.requests) != 1 {
		t.Fatalf("one-shot sold-out requests = %d, want 1", len(observations.requests))
	}
}

func TestRunClaimedShowtimeSurfacesTerminalSoldOutReportFailure(t *testing.T) {
	now := time.Date(2026, time.August, 12, 10, 0, 0, 0, time.UTC)
	base := claimedSeatWatchRepository()
	reportErr := errors.New("local store rejected terminal observation")
	gateway := &exactShowtimeGateway{workerGateway: &workerGateway{
		live: []domain.LiveSeat{{Label: "H10", Available: false}},
	}}
	worker := NewBookingWorker(BookingWorkerDependencies{
		Monitors: base, Reservations: base, Booking: gateway,
		Observations: &liveObservationRepositoryFake{err: reportErr}, IDs: &sequenceIDs{},
		Clock: fixedClock{now: now}, Waiter: noWaiter{},
	})
	_, err := runClaimedShowtime(t.Context(), worker, base, claimedSeatWatchShowtime())
	if !errors.Is(err, ErrSeatUnavailable) || !errors.Is(err, reportErr) {
		t.Fatalf("terminal sold-out report error = %v", err)
	}
}

func TestRunClaimedShowtimeAttemptErrorKeepsMonitorRunning(t *testing.T) {
	now := time.Date(2026, time.August, 12, 10, 0, 0, 0, time.UTC)
	base := &workerRepository{
		job:        monitorFixtureForTest("영화", []string{"2026-08-20"}),
		preset:     presetFixtureForTest("preset", "user", "theater", "auditorium", []string{"H10"}),
		theater:    domain.Theater{ID: "theater"},
		auditorium: domain.Auditorium{ID: "auditorium"},
	}
	attemptErr := errors.New("seat page failed")
	gateway := &exactShowtimeGateway{
		workerGateway: &workerGateway{live: []domain.LiveSeat{{Label: "H10", Available: true}}},
		openErr:       attemptErr,
	}
	worker := NewBookingWorker(BookingWorkerDependencies{
		Monitors: base, Reservations: base,
		Booking: gateway, Observations: &liveObservationRepositoryFake{},
		IDs: &sequenceIDs{}, Clock: fixedClock{now: now}, Waiter: noWaiter{},
	})

	_, err := runClaimedShowtime(t.Context(), worker, base, claimedSeatWatchShowtime())
	if !errors.Is(err, attemptErr) {
		t.Fatalf("attempt error = %v, want %v", err, attemptErr)
	}
	if got := monitorStateName(base.job); got != "running" {
		t.Fatalf("monitor state = %q, want running", got)
	}
	if base.job.GetState().GetFailed() != nil {
		t.Fatal("attempt error marked the monitor failed")
	}
	if checkedAt := base.job.GetLastCheckedAt(); checkedAt == nil || !checkedAt.AsTime().Equal(now) {
		t.Fatalf("last checked at = %v, want %v", checkedAt, now)
	}
}

func TestRunClaimedShowtimeCarriesAuthoritativeMonitorRevisionAcrossAttemptFailure(t *testing.T) {
	now := time.Date(2026, time.August, 12, 10, 0, 0, 0, time.UTC)
	monitor, preset, theater, auditorium, showtime := validClaimedValues()
	base := &workerRepository{
		job:        monitor,
		preset:     preset,
		theater:    domain.Theater{ID: theater.GetId()},
		auditorium: domain.Auditorium{ID: auditorium.GetId(), TheaterID: theater.GetId()},
	}
	repository := &centralFenceRepository{workerRepository: base, nextRevision: 41}
	attemptErr := errors.New("seat page failed")
	gateway := &exactShowtimeGateway{
		workerGateway: &workerGateway{live: []domain.LiveSeat{{Label: "H10", Available: true}}},
		openErr:       attemptErr,
	}
	worker := NewBookingWorker(BookingWorkerDependencies{
		Monitors: repository, Reservations: repository, Booking: gateway,
		Observations: &liveObservationRepositoryFake{}, IDs: &sequenceIDs{},
		Clock: fixedClock{now: now}, Waiter: noWaiter{},
	})

	monitorResource := resourceForMonitor(cloneMonitor(monitor), 7)
	presetResource := resourceForPreset(clonePreset(preset), 9)
	_, err := worker.RunClaimedShowtime(t.Context(), monitorResource, presetResource, theater, auditorium, showtime)
	if !errors.Is(err, attemptErr) {
		t.Fatalf("attempt error = %v, want %v", err, attemptErr)
	}
	if len(repository.putRevisions) != 2 || repository.putRevisions[0] != 7 || repository.putRevisions[1] != 41 {
		t.Fatalf("monitor CAS revisions = %v, want [7 41]", repository.putRevisions)
	}
	if monitorStateName(base.job) != "running" || base.job.GetLastCheckedAt() == nil {
		t.Fatalf("monitor after attempt failure = %s / %v", monitorStateName(base.job), base.job.GetLastCheckedAt())
	}
}

func claimedSeatWatchRepository() *workerRepository {
	return &workerRepository{
		job:     monitorFixtureForTest("영화", []string{"2026-08-20"}),
		preset:  presetFixtureForTest("preset", "user", "theater", "auditorium", []string{"H10"}),
		theater: domain.Theater{ID: "theater"}, auditorium: domain.Auditorium{ID: "auditorium"},
	}
}

func claimedSeatWatchWorker(
	repository *workerRepository,
	gateway claimedSeatWatchGateway,
	now time.Time,
) *BookingWorker {
	observations := &liveObservationRepositoryFake{}
	return NewBookingWorker(BookingWorkerDependencies{
		Monitors: repository, Reservations: repository,
		Booking: gateway, Observations: observations, IDs: &sequenceIDs{},
		Clock: fixedClock{now: now}, Waiter: noWaiter{},
		Jitter: func(time.Duration) time.Duration { return 0 },
		ClaimedWatch: ClaimedSeatWatchPolicy{
			Window: time.Second, RefreshLimit: 3,
			MinInterval: time.Millisecond, MaxInterval: time.Millisecond,
		},
	})
}

func claimedSeatWatchShowtime() domain.Showtime {
	return domain.Showtime{
		ID: "source", ProviderID: "cgv", SourceKey: "0056/2026-08-20/0007/0003",
		MovieID: "movie_1", Movie: "영화", TheaterID: "theater", AuditoriumID: "auditorium",
		Date: "2026-08-20", StartsAt: "20:00", EndsAt: "22:00",
		AvailableSeats: 1, Capacity: 100,
	}
}

func TestClaimedBookingRejectsPayloadMismatch(t *testing.T) {
	now := time.Date(2026, time.August, 12, 10, 0, 0, 0, time.UTC)
	job, preset, theater, auditorium, showtime := validClaimedValues()
	showtime.GetMovie().SetId("movie_2")
	if err := validateClaimedBooking(job, preset, theater, auditorium, showtime, now); err == nil {
		t.Fatal("mismatched execution showtime accepted")
	}
}

func TestClaimedBookingValidationBoundaries(t *testing.T) {
	now := time.Date(2026, time.August, 12, 10, 0, 0, 0, time.UTC)
	tests := []struct {
		name   string
		mutate func(*clientpb.Monitor, *clientpb.Preset, *catalogpb.Theater, *catalogpb.Auditorium, *catalogpb.Showtime)
	}{
		{"inactive monitor", func(job *clientpb.Monitor, _ *clientpb.Preset, _ *catalogpb.Theater, _ *catalogpb.Auditorium, _ *catalogpb.Showtime) {
			setMonitorState(job, "stopped", "")
		}},
		{"preset mismatch", func(_ *clientpb.Monitor, preset *clientpb.Preset, _ *catalogpb.Theater, _ *catalogpb.Auditorium, _ *catalogpb.Showtime) {
			preset.SetId("different")
		}},
		{"showtime mismatch", func(_ *clientpb.Monitor, _ *clientpb.Preset, _ *catalogpb.Theater, _ *catalogpb.Auditorium, showtime *catalogpb.Showtime) {
			showtime.GetAuditorium().SetId("different")
		}},
		{"impossible availability", func(_ *clientpb.Monitor, _ *clientpb.Preset, _ *catalogpb.Theater, _ *catalogpb.Auditorium, showtime *catalogpb.Showtime) {
			showtime.SetAvailableSeats(showtime.GetCapacity() + 1)
		}},
		{"incomplete schedule", func(_ *clientpb.Monitor, _ *clientpb.Preset, _ *catalogpb.Theater, _ *catalogpb.Auditorium, showtime *catalogpb.Showtime) {
			showtime.SetEndsAt(nil)
		}},
		{"started showtime", func(_ *clientpb.Monitor, _ *clientpb.Preset, _ *catalogpb.Theater, _ *catalogpb.Auditorium, showtime *catalogpb.Showtime) {
			showtime.SetStartsAt(timestamppb.New(now))
		}},
		{"outside schedule", func(_ *clientpb.Monitor, _ *clientpb.Preset, _ *catalogpb.Theater, _ *catalogpb.Auditorium, showtime *catalogpb.Showtime) {
			showtime.SetStartsAt(timestamppb.New(time.Date(2026, time.August, 21, 20, 0, 0, 0, time.UTC)))
			showtime.SetEndsAt(timestamppb.New(time.Date(2026, time.August, 21, 22, 0, 0, 0, time.UTC)))
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			job, preset, theater, auditorium, showtime := validClaimedValues()
			test.mutate(job, preset, theater, auditorium, showtime)
			if err := validateClaimedBooking(job, preset, theater, auditorium, showtime, now); err == nil {
				t.Fatal("invalid claimed booking accepted")
			}
		})
	}
	job, preset, theater, auditorium, showtime := validClaimedValues()
	if err := validateClaimedBooking(job, preset, theater, auditorium, showtime, now); err != nil {
		t.Fatalf("valid claimed booking rejected: %v", err)
	}
	showtime.SetAvailableSeats(0)
	showtime.SetSoldOut(true)
	if err := validateClaimedBooking(job, preset, theater, auditorium, showtime, now); err != nil {
		t.Fatalf("zero-seat cancellation watch rejected: %v", err)
	}
}

func validClaimedValues() (*clientpb.Monitor, *clientpb.Preset, *catalogpb.Theater, *catalogpb.Auditorium, *catalogpb.Showtime) {
	monitor := monitorFixtureForTest("영화", []string{"2026-08-20"})
	preset := presetFixtureForTest("preset", "user", "theater", "auditorium", []string{"H10"})
	theater := coverageTheater(domain.Theater{ID: "theater"})
	auditorium := coverageAuditorium(domain.Auditorium{ID: "auditorium", TheaterID: "theater"})
	showtime := showtimeProtoFromDomain(domain.Showtime{ID: "source", ProviderID: "cgv", SourceKey: "0056/2026-08-20/0007/0003", MovieID: "movie_1", Movie: "영화", TheaterID: "theater", AuditoriumID: "auditorium", Date: "2026-08-20", StartsAt: "20:00", EndsAt: "22:00", AvailableSeats: 1, Capacity: 100})
	return monitor, preset, theater, auditorium, showtime
}

func TestRunClaimedShowtimeFailureBoundaries(t *testing.T) {
	now := time.Date(2026, time.August, 12, 10, 0, 0, 0, time.UTC)
	makeWorker := func(putErrAt int, openErr error) (*BookingWorker, *centralFenceRepository, domain.Showtime) {
		base := &workerRepository{
			job:        monitorFixtureForTest("영화", []string{"2026-08-20"}),
			preset:     presetFixtureForTest("preset", "user", "theater", "auditorium", []string{"H10"}),
			theater:    domain.Theater{ID: "theater"},
			auditorium: domain.Auditorium{ID: "auditorium"},
			seatMap: domain.SeatMap{AuditoriumID: "auditorium", Seats: []domain.Seat{{
				ID: "seat", AuditoriumID: "auditorium", Label: "H10", Row: "H", Number: 10,
				X: .5, Y: .5, Type: domain.SeatTypeStandard,
			}}},
		}
		repository := &centralFenceRepository{workerRepository: base, putErrAt: putErrAt}
		gateway := &exactShowtimeGateway{
			workerGateway: &workerGateway{live: []domain.LiveSeat{{Label: "H10", Available: true}}},
			openErr:       openErr,
		}
		worker := NewBookingWorker(BookingWorkerDependencies{
			Monitors: repository, Reservations: repository,
			Booking: gateway, Observations: &liveObservationRepositoryFake{}, IDs: &sequenceIDs{},
			Clock: fixedClock{now: now}, Waiter: noWaiter{},
		})
		showtime := domain.Showtime{
			ID: "source", ProviderID: "cgv", SourceKey: "0056/2026-08-20/0007/0003", MovieID: "movie_1", Movie: "영화", TheaterID: "theater", AuditoriumID: "auditorium",
			Date: "2026-08-20", StartsAt: "20:00", EndsAt: "22:00",
			AvailableSeats: 1, Capacity: 100,
		}
		return worker, repository, showtime
	}
	t.Run("invalid claim", func(t *testing.T) {
		worker, repository, showtime := makeWorker(0, nil)
		showtime.ID = ""
		if _, err := runClaimedShowtime(t.Context(), worker, repository.workerRepository, showtime); err == nil {
			t.Fatal("invalid claim accepted")
		}
	})
	t.Run("initial persistence", func(t *testing.T) {
		worker, repository, showtime := makeWorker(1, nil)
		if _, err := runClaimedShowtime(t.Context(), worker, repository.workerRepository, showtime); err == nil {
			t.Fatal("initial monitor persistence failure ignored")
		}
	})
	t.Run("attempt and failure persistence", func(t *testing.T) {
		worker, repository, showtime := makeWorker(2, errors.New("seat page failed"))
		if _, err := runClaimedShowtime(t.Context(), worker, repository.workerRepository, showtime); err == nil {
			t.Fatal("joined execution failure ignored")
		}
	})
	t.Run("attempt failure", func(t *testing.T) {
		worker, repository, showtime := makeWorker(0, errors.New("seat page failed"))
		if _, err := runClaimedShowtime(t.Context(), worker, repository.workerRepository, showtime); err == nil {
			t.Fatal("execution failure ignored")
		}
	})
	t.Run("cancelled fence", func(t *testing.T) {
		worker, repository, showtime := makeWorker(0, nil)
		ctx, cancel := context.WithCancel(t.Context())
		cancel()
		if _, err := runClaimedShowtime(ctx, worker, repository.workerRepository, showtime); !errors.Is(err, context.Canceled) {
			t.Fatalf("cancelled execution = %v", err)
		}
	})
	t.Run("completion persistence", func(t *testing.T) {
		worker, repository, showtime := makeWorker(2, nil)
		if _, err := runClaimedShowtime(t.Context(), worker, repository.workerRepository, showtime); err == nil {
			t.Fatal("completion persistence failure ignored")
		}
	})
}

func runClaimedShowtime(ctx context.Context, worker *BookingWorker, repository *workerRepository, showtime domain.Showtime) (*clientpb.Resource, error) {
	monitor := resourceForMonitor(cloneMonitor(repository.job), 0)
	preset := resourceForPreset(clonePreset(repository.preset), 0)
	var err error
	theater, err := repository.GetTheater(ctx, repository.theater.ID)
	if err != nil {
		return nil, err
	}
	auditorium, err := repository.GetAuditorium(ctx, repository.auditorium.ID)
	if err != nil {
		return nil, err
	}
	return worker.RunClaimedShowtime(ctx, monitor, preset, theater, auditorium, showtimeProtoFromDomain(showtime))
}
