package application

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/cineko-org/client/internal/domain"
)

func TestBestShowtimeDoesNotTrustPossiblyStaleSeatCount(t *testing.T) {
	t.Parallel()

	showtime, ok := bestShowtime([]domain.Showtime{
		{ID: "sold-out", SoldOut: true, AvailableSeats: 2},
		{ID: "open", Date: "2026-08-10", StartsAt: "20:30", AvailableSeats: 0},
	})
	if !ok || showtime.ID != "open" {
		t.Fatalf("bestShowtime() = %+v, %t", showtime, ok)
	}
}

func TestBookingWorkerStopsAtPreparedPaymentWhenCommitIsDisabled(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, time.August, 9, 12, 0, 0, 0, time.UTC)
	repository := &workerRepository{
		job: domain.MonitorJob{
			ID: "monitor-1", UserID: "user-1", PresetID: "preset-1", MovieID: "movie_1", Movie: "오디세이",
			TargetDates: []string{"2026-08-10"}, PollInterval: 5 * time.Second,
		},
		preset: domain.Preset{
			ID: "preset-1", UserID: "user-1", TheaterID: "theater-1", AuditoriumID: "auditorium-1",
			SeatCount: 1, SeatPreference: domain.SeatPreference{CandidateSeats: []string{"H10"}, Adjacency: domain.SeatAdjacencyRequired},
		},
		theater:    domain.Theater{ID: "theater-1"},
		auditorium: domain.Auditorium{ID: "auditorium-1"},
		seatMap: domain.SeatMap{AuditoriumID: "auditorium-1", Seats: []domain.Seat{{
			Label: "H10", Row: "H", Number: 10, X: .5, Y: .55, Type: domain.SeatTypeStandard,
		}}},
	}
	gateway := &workerGateway{
		showtimes: []domain.Showtime{{ID: "showtime-1", Date: "2026-08-10", StartsAt: "20:30"}},
		live:      []domain.LiveSeat{{Label: "H10", Available: true}},
	}
	worker := NewBookingWorker(BookingWorkerDependencies{
		Monitors: repository, Presets: repository, Theaters: repository,
		Auditoriums: repository, Reservations: repository,
		Showtimes: gateway, Booking: gateway, IDs: &sequenceIDs{},
		Clock: fixedClock{now: now}, Waiter: noWaiter{}, WorkerID: "worker-1",
	})

	reservation, err := worker.Run(context.Background(), "monitor-1")
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if reservation.Status != "prepared" || repository.job.Status != domain.MonitorTriggered {
		t.Fatalf("reservation/job = %+v / %+v", reservation, repository.job)
	}
	if repository.reservation.ID == "" {
		t.Fatal("prepared reservation was not persisted")
	}
	wantDates := []string{"2026-08-10"}
	if fmt.Sprint(gateway.lastQuery.TargetDates) != fmt.Sprint(wantDates) {
		t.Fatalf("FindShowtimes target dates = %v, want %v", gateway.lastQuery.TargetDates, wantDates)
	}
}

func TestMonitorServiceDefaultsRollingWeekdayHorizon(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, time.August, 9, 12, 0, 0, 0, time.UTC)
	repository := &workerRepository{preset: domain.Preset{ID: "preset-1", UserID: "user-1"}}
	service := NewMonitorService(repository, repository, &sequenceIDs{}, fixedClock{now: now})

	job, err := service.Create(context.Background(), CreateMonitorRequest{
		UserID: "user-1", PresetID: "preset-1", MovieID: "movie_1", Movie: "오디세이",
		TargetWeekdays: []int{int(time.Saturday)}, PollInterval: 5 * time.Second,
	})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	if job.SearchHorizonDays != domain.DefaultSearchHorizonDays {
		t.Fatalf("SearchHorizonDays = %d, want %d", job.SearchHorizonDays, domain.DefaultSearchHorizonDays)
	}
}

func TestMonitorServiceRejectsExpiredExactDates(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, time.August, 9, 12, 0, 0, 0, time.UTC)
	repository := &workerRepository{preset: domain.Preset{ID: "preset-1", UserID: "user-1"}}
	service := NewMonitorService(repository, repository, &sequenceIDs{}, fixedClock{now: now})

	_, err := service.Create(context.Background(), CreateMonitorRequest{
		UserID: "user-1", PresetID: "preset-1", MovieID: "movie_1", Movie: "오디세이",
		TargetDates: []string{"2026-08-08"}, PollInterval: 5 * time.Second,
	})
	if !errors.Is(err, ErrMonitorExpired) {
		t.Fatalf("Create() error = %v, want %v", err, ErrMonitorExpired)
	}
}

func TestCancellationMonitorFailsWhenShowtimeIsNotOpen(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, time.August, 9, 12, 0, 0, 0, time.UTC)
	repository := &workerRepository{
		job: domain.MonitorJob{
			ID: "monitor-1", UserID: "user-1", PresetID: "preset-1", MovieID: "movie_1", Movie: "오디세이",
			Mode: domain.MonitorModeCancellation, TargetDates: []string{"2026-08-10"},
			PollInterval: 5 * time.Second,
		},
		preset: domain.Preset{
			ID: "preset-1", UserID: "user-1", TheaterID: "theater-1", AuditoriumID: "auditorium-1",
			SeatCount: 1, SeatPreference: domain.SeatPreference{CandidateSeats: []string{"H10"}, Adjacency: domain.SeatAdjacencyRequired},
		},
		theater: domain.Theater{ID: "theater-1"}, auditorium: domain.Auditorium{ID: "auditorium-1"},
		seatMap: domain.SeatMap{AuditoriumID: "auditorium-1"},
	}
	gateway := &workerGateway{}
	worker := NewBookingWorker(BookingWorkerDependencies{
		Monitors: repository, Presets: repository, Theaters: repository,
		Auditoriums: repository, Reservations: repository,
		Showtimes: gateway, Booking: gateway, IDs: &sequenceIDs{},
		Clock: fixedClock{now: now}, Waiter: noWaiter{}, WorkerID: "worker-1",
	})

	_, err := worker.Run(context.Background(), "monitor-1")
	if !errors.Is(err, ErrBookingNotOpen) {
		t.Fatalf("Run() error = %v, want %v", err, ErrBookingNotOpen)
	}
	if repository.job.Status != domain.MonitorFailed {
		t.Fatalf("monitor status = %s, want failed", repository.job.Status)
	}
}

type workerRepository struct {
	job         domain.MonitorJob
	preset      domain.Preset
	theater     domain.Theater
	auditorium  domain.Auditorium
	seatMap     domain.SeatMap
	reservation domain.Reservation
}

func (repository *workerRepository) AcquireMonitor(
	_ context.Context, id, _ string, _ time.Time, _ time.Duration,
) (domain.MonitorJob, error) {
	if id != repository.job.ID {
		return domain.MonitorJob{}, ErrNotFound
	}
	return repository.job, nil
}

func (repository *workerRepository) RenewMonitor(
	context.Context, string, string, time.Time, time.Duration,
) error {
	return nil
}

func (repository *workerRepository) ReleaseMonitor(context.Context, string, string) error { return nil }
func (repository *workerRepository) PutMonitor(_ context.Context, job domain.MonitorJob) error {
	repository.job = job
	return nil
}
func (repository *workerRepository) GetMonitor(context.Context, string) (domain.MonitorJob, error) {
	return repository.job, nil
}
func (repository *workerRepository) ListMonitorsByUser(context.Context, string) ([]domain.MonitorJob, error) {
	return []domain.MonitorJob{repository.job}, nil
}
func (repository *workerRepository) DeleteMonitor(context.Context, string) error { return nil }
func (repository *workerRepository) GetPreset(context.Context, string) (domain.Preset, error) {
	return repository.preset, nil
}
func (repository *workerRepository) PutPreset(context.Context, domain.Preset) error { return nil }
func (repository *workerRepository) ListPresetsByUser(context.Context, string) ([]domain.Preset, error) {
	return []domain.Preset{repository.preset}, nil
}
func (repository *workerRepository) DeletePreset(context.Context, string) error { return nil }
func (repository *workerRepository) GetTheater(context.Context, string) (domain.Theater, error) {
	return repository.theater, nil
}
func (repository *workerRepository) PutTheater(context.Context, domain.Theater) error { return nil }
func (repository *workerRepository) ListTheaters(context.Context) ([]domain.Theater, error) {
	return []domain.Theater{repository.theater}, nil
}
func (repository *workerRepository) GetAuditorium(context.Context, string) (domain.Auditorium, error) {
	return repository.auditorium, nil
}
func (repository *workerRepository) PutAuditorium(context.Context, domain.Auditorium) error {
	return nil
}
func (repository *workerRepository) ListAuditoriumsByTheater(context.Context, string) ([]domain.Auditorium, error) {
	return []domain.Auditorium{repository.auditorium}, nil
}
func (repository *workerRepository) GetSeatMap(context.Context, string) (domain.SeatMap, error) {
	return repository.seatMap, nil
}
func (repository *workerRepository) PutSeatMap(context.Context, domain.SeatMap) error { return nil }
func (repository *workerRepository) PutReservation(_ context.Context, value domain.Reservation) error {
	repository.reservation = value
	return nil
}
func (repository *workerRepository) GetReservation(context.Context, string) (domain.Reservation, error) {
	return repository.reservation, nil
}
func (repository *workerRepository) ListReservationsByUser(context.Context, string) ([]domain.Reservation, error) {
	return []domain.Reservation{repository.reservation}, nil
}

type workerGateway struct {
	showtimes []domain.Showtime
	live      []domain.LiveSeat
	lastQuery ShowtimeQuery
}

func (gateway *workerGateway) FindShowtimes(_ context.Context, query ShowtimeQuery) ([]domain.Showtime, error) {
	gateway.lastQuery = query
	return gateway.showtimes, nil
}
func (gateway *workerGateway) OpenSeatSelection(
	context.Context,
	domain.Showtime,
	int,
) (domain.SeatSelection, error) {
	return domain.SeatSelection{SeatMap: gatewaySeatMap(), LiveSeats: gateway.live}, nil
}

func gatewaySeatMap() domain.SeatMap {
	return domain.SeatMap{AuditoriumID: "auditorium-1", Seats: []domain.Seat{{
		Label: "H10", Row: "H", Number: 10, X: .5, Y: .55, Type: domain.SeatTypeStandard,
	}}}
}
func (gateway *workerGateway) PreparePayment(
	_ context.Context, showtime domain.Showtime, seats []string,
) (domain.BookingDraft, error) {
	return domain.BookingDraft{Showtime: showtime, SeatLabels: seats}, nil
}
func (gateway *workerGateway) PrepareCancellation(context.Context, domain.Reservation) (domain.CancellationDraft, error) {
	return domain.CancellationDraft{}, fmt.Errorf("not used")
}
func (gateway *workerGateway) CommitCancellation(context.Context) error {
	return fmt.Errorf("not used")
}

type fixedClock struct{ now time.Time }

func (clock fixedClock) Now() time.Time { return clock.now }

type sequenceIDs struct{ next int }

func (ids *sequenceIDs) NewID() string {
	ids.next++
	return fmt.Sprintf("id-%d", ids.next)
}

type noWaiter struct{}

func (noWaiter) Wait(context.Context, time.Duration) error { return nil }
