package application

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/cineko-org/client/internal/domain"
	catalogpb "github.com/cineko-org/contracts/v3/gen/go/cineko/catalog"
	clientpb "github.com/cineko-org/contracts/v3/gen/go/cineko/client"
)

type recordingWaiter struct {
	delays []time.Duration
	err    error
}

func (waiter *recordingWaiter) Wait(_ context.Context, delay time.Duration) error {
	waiter.delays = append(waiter.delays, delay)
	return waiter.err
}

type sequenceClock struct {
	values []time.Time
	index  int
}

func (clock *sequenceClock) Now() time.Time {
	index := min(clock.index, len(clock.values)-1)
	clock.index++
	return clock.values[index]
}

func TestClaimedSeatWatchPolicyNormalization(t *testing.T) {
	defaults := (ClaimedSeatWatchPolicy{}).normalized()
	if defaults != defaultClaimedSeatWatchPolicy() {
		t.Fatalf("defaults = %+v", defaults)
	}
	invalid := (ClaimedSeatWatchPolicy{Window: -time.Second, RefreshLimit: 1, MinInterval: time.Second}).normalized()
	if invalid != (ClaimedSeatWatchPolicy{}) {
		t.Fatalf("invalid policy = %+v", invalid)
	}
	adjusted := (ClaimedSeatWatchPolicy{
		Window: time.Minute, RefreshLimit: 1,
		MinInterval: 2 * time.Second, MaxInterval: time.Second,
	}).normalized()
	if adjusted.MaxInterval != adjusted.MinInterval {
		t.Fatalf("adjusted policy = %+v", adjusted)
	}
}

func TestAttemptClaimedShowtimeWithoutRefreshCapability(t *testing.T) {
	repository := claimedSeatWatchRepository()
	gateway := &exactShowtimeGateway{workerGateway: &workerGateway{
		live: []domain.LiveSeat{{Label: "H10", Available: false}},
	}}
	worker := claimedSeatWatchWorker(repository, gateway, time.Now())
	showtime := claimedSeatWatchShowtime()
	job, preset, showtimeMessage := claimedSeatWatchInputs(repository, showtime)
	_, err := worker.attemptClaimedShowtime(
		t.Context(), job, preset, showtimeMessage,
	)
	if !errors.Is(err, ErrSeatUnavailable) || gateway.openCalls != 1 {
		t.Fatalf("single attempt = %v, opens %d", err, gateway.openCalls)
	}
}

func TestAttemptClaimedShowtimeBoundsWaitAndRefresh(t *testing.T) {
	repository := claimedSeatWatchRepository()
	refreshErr := errors.New("refresh stopped")
	gateway := &refreshingShowtimeGateway{
		exactShowtimeGateway: &exactShowtimeGateway{workerGateway: &workerGateway{
			live: []domain.LiveSeat{{Label: "H10", Available: false}},
		}},
		refreshErr: refreshErr,
	}
	waiter := &recordingWaiter{}
	now := time.Now()
	worker := newClaimedSeatWatchTestWorker(
		repository, gateway, fixedClock{now: now}, waiter,
		ClaimedSeatWatchPolicy{
			Window: 500 * time.Microsecond, RefreshLimit: 1,
			MinInterval: time.Millisecond, MaxInterval: time.Millisecond,
		},
	)
	showtime := claimedSeatWatchShowtime()
	job, preset, showtimeMessage := claimedSeatWatchInputs(repository, showtime)
	_, err := worker.attemptClaimedShowtime(
		t.Context(), job, preset, showtimeMessage,
	)
	if !errors.Is(err, refreshErr) || len(waiter.delays) != 1 || waiter.delays[0] != 500*time.Microsecond {
		t.Fatalf("bounded refresh = %v, delays %v", err, waiter.delays)
	}
}

func TestAttemptClaimedShowtimeStopsOnWaitAndDeadline(t *testing.T) {
	repository := claimedSeatWatchRepository()
	showtime := claimedSeatWatchShowtime()
	job, preset, showtimeMessage := claimedSeatWatchInputs(repository, showtime)
	newGateway := func() *refreshingShowtimeGateway {
		return &refreshingShowtimeGateway{
			exactShowtimeGateway: &exactShowtimeGateway{workerGateway: &workerGateway{
				live: []domain.LiveSeat{{Label: "H10", Available: false}},
			}},
			selections: []domain.SeatSelection{{
				SeatMap: gatewaySeatMap(), LiveSeats: []domain.LiveSeat{{Label: "H10", Available: false}},
			}},
		}
	}
	t.Run("wait error", func(t *testing.T) {
		waitErr := errors.New("wait failed")
		gateway := newGateway()
		worker := newClaimedSeatWatchTestWorker(
			repository, gateway, fixedClock{now: time.Now()}, &recordingWaiter{err: waitErr},
			ClaimedSeatWatchPolicy{
				Window: time.Second, RefreshLimit: 1,
				MinInterval: time.Millisecond, MaxInterval: time.Millisecond,
			},
		)
		_, err := worker.attemptClaimedShowtime(
			t.Context(), job, preset, showtimeMessage,
		)
		if !errors.Is(err, waitErr) || gateway.refreshCalls != 0 {
			t.Fatalf("wait result = %v, refreshes %d", err, gateway.refreshCalls)
		}
	})
	t.Run("deadline", func(t *testing.T) {
		now := time.Now()
		gateway := newGateway()
		worker := newClaimedSeatWatchTestWorker(
			repository, gateway, &sequenceClock{values: []time.Time{now, now.Add(2 * time.Second)}},
			&recordingWaiter{}, ClaimedSeatWatchPolicy{
				Window: time.Second, RefreshLimit: 1,
				MinInterval: time.Millisecond, MaxInterval: time.Millisecond,
			},
		)
		_, err := worker.attemptClaimedShowtime(
			t.Context(), job, preset, showtimeMessage,
		)
		if !errors.Is(err, ErrSeatUnavailable) || gateway.refreshCalls != 0 {
			t.Fatalf("deadline result = %v, refreshes %d", err, gateway.refreshCalls)
		}
	})
	t.Run("refresh limit", func(t *testing.T) {
		gateway := newGateway()
		worker := newClaimedSeatWatchTestWorker(
			repository, gateway, fixedClock{now: time.Now()}, &recordingWaiter{},
			ClaimedSeatWatchPolicy{
				Window: time.Second, RefreshLimit: 1,
				MinInterval: time.Millisecond, MaxInterval: time.Millisecond,
			},
		)
		_, err := worker.attemptClaimedShowtime(
			t.Context(), job, preset, showtimeMessage,
		)
		if !errors.Is(err, ErrSeatUnavailable) || gateway.refreshCalls != 1 {
			t.Fatalf("limit result = %v, refreshes %d", err, gateway.refreshCalls)
		}
	})
}

func claimedSeatWatchInputs(repository *workerRepository, showtime domain.Showtime) (*clientpb.Monitor, *clientpb.Preset, *catalogpb.Showtime) {
	return cloneMonitor(repository.job), clonePreset(repository.preset), showtimeProtoFromDomain(showtime)
}

type claimedSeatWatchGateway interface {
	BookingGateway
}

func newClaimedSeatWatchTestWorker(
	repository *workerRepository,
	gateway claimedSeatWatchGateway,
	clock Clock,
	waiter Waiter,
	policy ClaimedSeatWatchPolicy,
) *BookingWorker {
	return NewBookingWorker(BookingWorkerDependencies{
		Monitors: repository, Reservations: repository,
		Booking: gateway, IDs: &sequenceIDs{}, Clock: clock, Waiter: waiter,
		Jitter: func(time.Duration) time.Duration { return 0 }, ClaimedWatch: policy,
	})
}
