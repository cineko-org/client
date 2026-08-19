package application

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/cineko-org/client/internal/domain"
)

type centralFenceRepository struct {
	*workerRepository
	acquireCalled bool
	putCalls      int
	putErrAt      int
}

func (repository *centralFenceRepository) AcquireMonitor(
	context.Context,
	string,
	string,
	time.Time,
	time.Duration,
) (domain.MonitorJob, error) {
	repository.acquireCalled = true
	return domain.MonitorJob{}, errors.New("local monitor lease must not be acquired")
}

func (repository *centralFenceRepository) PutMonitor(_ context.Context, job domain.MonitorJob) error {
	repository.putCalls++
	if repository.putCalls == repository.putErrAt {
		return errors.New("put monitor failed")
	}
	repository.job = job
	return nil
}

type exactShowtimeGateway struct {
	*workerGateway
	findCalled bool
	opened     domain.Showtime
	openErr    error
}

func (gateway *exactShowtimeGateway) FindShowtimes(
	context.Context,
	ShowtimeQuery,
) ([]domain.Showtime, error) {
	gateway.findCalled = true
	return nil, errors.New("schedule lookup must not run")
}

func (gateway *exactShowtimeGateway) OpenSeatSelection(
	_ context.Context,
	showtime domain.Showtime,
	_ int,
) (domain.SeatSelection, error) {
	gateway.opened = showtime
	return domain.SeatSelection{SeatMap: gatewaySeatMap(), LiveSeats: gateway.live}, gateway.openErr
}

func TestRunClaimedShowtimeUsesCentralFenceAndExactPayload(t *testing.T) {
	now := time.Date(2026, time.August, 12, 10, 0, 0, 0, time.UTC)
	base := &workerRepository{
		job: domain.MonitorJob{
			ID: "monitor", UserID: "user", PresetID: "preset", Movie: "영화",
			TargetDates: []string{"2026-08-20"}, PollInterval: 5 * time.Second,
			Status: domain.MonitorPending,
		},
		preset: domain.Preset{
			ID: "preset", UserID: "user", TheaterID: "theater", AuditoriumID: "auditorium",
			SeatCount: 1, SeatPreference: domain.SeatPreference{
				CandidateSeats: []string{"H10"}, Adjacency: domain.SeatAdjacencyRequired,
			},
		},
		theater:    domain.Theater{ID: "theater"},
		auditorium: domain.Auditorium{ID: "auditorium", TheaterID: "theater"},
		seatMap: domain.SeatMap{AuditoriumID: "auditorium", Seats: []domain.Seat{{
			ID: "seat", AuditoriumID: "auditorium", Label: "H10", Row: "H", Number: 10,
			X: .5, Y: .5, Type: domain.SeatTypeStandard,
		}}},
	}
	repository := &centralFenceRepository{workerRepository: base}
	gateway := &exactShowtimeGateway{workerGateway: &workerGateway{
		live: []domain.LiveSeat{{Label: "H10", Available: true}},
	}}
	showtime := domain.Showtime{
		ID: "source", Movie: "영화", TheaterID: "theater", AuditoriumID: "auditorium",
		Date: "2026-08-20", StartsAt: "20:00", EndsAt: "22:00",
		AvailableSeats: 10, Capacity: 100,
	}
	worker := NewBookingWorker(BookingWorkerDependencies{
		Monitors: repository, Presets: repository, Theaters: repository,
		Auditoriums: repository, Reservations: repository,
		Showtimes: gateway, Booking: gateway, IDs: &sequenceIDs{},
		Clock: fixedClock{now: now}, Waiter: noWaiter{}, WorkerID: "irrelevant-local-worker",
	})
	reservation, err := worker.RunClaimedShowtime(t.Context(), ClaimedBooking{
		Monitor: base.job, Preset: base.preset, Theater: base.theater,
		Auditorium: base.auditorium, Showtime: showtime,
	})
	if err != nil {
		t.Fatal(err)
	}
	if repository.acquireCalled || gateway.findCalled {
		t.Fatalf("local lease/schedule lookup called = %t/%t", repository.acquireCalled, gateway.findCalled)
	}
	if gateway.opened.ID != showtime.ID || gateway.opened.StartsAt != showtime.StartsAt {
		t.Fatalf("opened showtime = %+v", gateway.opened)
	}
	if reservation.Status != "prepared" || base.job.Status != domain.MonitorTriggered {
		t.Fatalf("reservation/monitor = %+v / %+v", reservation, base.job)
	}
}

func TestClaimedBookingRejectsPayloadMismatch(t *testing.T) {
	now := time.Date(2026, time.August, 12, 10, 0, 0, 0, time.UTC)
	claimed := ClaimedBooking{
		Monitor: domain.MonitorJob{
			ID: "monitor", PresetID: "preset", Movie: "영화", Status: domain.MonitorPending,
			TargetDates: []string{"2026-08-20"},
		},
		Preset:     domain.Preset{ID: "preset", TheaterID: "theater", AuditoriumID: "auditorium"},
		Theater:    domain.Theater{ID: "theater"},
		Auditorium: domain.Auditorium{ID: "auditorium"},
		Showtime: domain.Showtime{
			ID: "source", Movie: "다른 영화", TheaterID: "theater", AuditoriumID: "auditorium",
			Date: "2026-08-20", StartsAt: "20:00", EndsAt: "22:00",
			AvailableSeats: 1, Capacity: 100,
		},
	}
	if err := claimed.Validate(now); err == nil {
		t.Fatal("mismatched execution showtime accepted")
	}
}

func TestClaimedBookingValidationBoundaries(t *testing.T) {
	now := time.Date(2026, time.August, 12, 10, 0, 0, 0, time.UTC)
	valid := func() ClaimedBooking {
		return ClaimedBooking{
			Monitor: domain.MonitorJob{
				ID: "monitor", PresetID: "preset", Movie: "영화", Status: domain.MonitorPending,
				TargetDates: []string{"2026-08-20"},
			},
			Preset:     domain.Preset{ID: "preset", TheaterID: "theater", AuditoriumID: "auditorium"},
			Theater:    domain.Theater{ID: "theater"},
			Auditorium: domain.Auditorium{ID: "auditorium"},
			Showtime: domain.Showtime{
				ID: "source", Movie: "영화", TheaterID: "theater", AuditoriumID: "auditorium",
				Date: "2026-08-20", StartsAt: "20:00", EndsAt: "22:00",
				AvailableSeats: 1, Capacity: 100,
			},
		}
	}
	tests := []struct {
		name   string
		mutate func(*ClaimedBooking)
	}{
		{"inactive monitor", func(value *ClaimedBooking) { value.Monitor.Status = domain.MonitorStopped }},
		{"preset mismatch", func(value *ClaimedBooking) { value.Preset.ID = "different" }},
		{"showtime mismatch", func(value *ClaimedBooking) { value.Showtime.AuditoriumID = "different" }},
		{"unavailable showtime", func(value *ClaimedBooking) { value.Showtime.SoldOut = true }},
		{"incomplete schedule", func(value *ClaimedBooking) { value.Showtime.EndsAt = "" }},
		{"outside schedule", func(value *ClaimedBooking) { value.Showtime.Date = "2026-08-21" }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			value := valid()
			test.mutate(&value)
			if err := value.Validate(now); err == nil {
				t.Fatal("invalid claimed booking accepted")
			}
		})
	}
	if err := valid().Validate(now); err != nil {
		t.Fatalf("valid claimed booking rejected: %v", err)
	}
}

func TestRunClaimedShowtimeFailureBoundaries(t *testing.T) {
	now := time.Date(2026, time.August, 12, 10, 0, 0, 0, time.UTC)
	makeWorker := func(putErrAt int, openErr error) (*BookingWorker, *centralFenceRepository, ClaimedBooking) {
		base := &workerRepository{
			job: domain.MonitorJob{
				ID: "monitor", UserID: "user", PresetID: "preset", Movie: "영화",
				TargetDates: []string{"2026-08-20"}, Status: domain.MonitorPending,
			},
			preset: domain.Preset{
				ID: "preset", TheaterID: "theater", AuditoriumID: "auditorium", SeatCount: 1,
				SeatPreference: domain.SeatPreference{
					CandidateSeats: []string{"H10"}, Adjacency: domain.SeatAdjacencyRequired,
				},
			},
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
			Monitors: repository, Presets: repository, Theaters: repository,
			Auditoriums: repository, Reservations: repository,
			Showtimes: gateway, Booking: gateway, IDs: &sequenceIDs{},
			Clock: fixedClock{now: now}, Waiter: noWaiter{},
		})
		claimed := ClaimedBooking{
			Monitor: base.job, Preset: base.preset, Theater: base.theater, Auditorium: base.auditorium,
			Showtime: domain.Showtime{
				ID: "source", Movie: "영화", TheaterID: "theater", AuditoriumID: "auditorium",
				Date: "2026-08-20", StartsAt: "20:00", EndsAt: "22:00",
				AvailableSeats: 1, Capacity: 100,
			},
		}
		return worker, repository, claimed
	}
	t.Run("invalid claim", func(t *testing.T) {
		worker, _, claimed := makeWorker(0, nil)
		claimed.Showtime.ID = ""
		if _, err := worker.RunClaimedShowtime(t.Context(), claimed); err == nil {
			t.Fatal("invalid claim accepted")
		}
	})
	t.Run("initial persistence", func(t *testing.T) {
		worker, _, claimed := makeWorker(1, nil)
		if _, err := worker.RunClaimedShowtime(t.Context(), claimed); err == nil {
			t.Fatal("initial monitor persistence failure ignored")
		}
	})
	t.Run("attempt and failure persistence", func(t *testing.T) {
		worker, _, claimed := makeWorker(2, errors.New("seat page failed"))
		if _, err := worker.RunClaimedShowtime(t.Context(), claimed); err == nil {
			t.Fatal("joined execution failure ignored")
		}
	})
	t.Run("attempt failure", func(t *testing.T) {
		worker, _, claimed := makeWorker(0, errors.New("seat page failed"))
		if _, err := worker.RunClaimedShowtime(t.Context(), claimed); err == nil {
			t.Fatal("execution failure ignored")
		}
	})
	t.Run("cancelled fence", func(t *testing.T) {
		worker, _, claimed := makeWorker(0, nil)
		ctx, cancel := context.WithCancel(t.Context())
		cancel()
		if _, err := worker.RunClaimedShowtime(ctx, claimed); !errors.Is(err, context.Canceled) {
			t.Fatalf("cancelled execution = %v", err)
		}
	})
	t.Run("completion persistence", func(t *testing.T) {
		worker, _, claimed := makeWorker(2, nil)
		if _, err := worker.RunClaimedShowtime(t.Context(), claimed); err == nil {
			t.Fatal("completion persistence failure ignored")
		}
	})
}
