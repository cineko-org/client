package webui

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/cineko-org/client/internal/application"
	"github.com/cineko-org/client/internal/domain"
	"github.com/cineko-org/client/internal/testsupport/memoryrepo"
	contracts "github.com/cineko-org/contracts/v3"
)

type executionAutomation struct {
	*webPaymentAutomation
	opened     chan domain.Showtime
	closed     chan struct{}
	closeOnce  sync.Once
	findCalled atomic.Bool
}

func (automation *executionAutomation) FindShowtimes(
	context.Context,
	application.ShowtimeQuery,
) ([]domain.Showtime, error) {
	automation.findCalled.Store(true)
	return nil, nil
}

func (automation *executionAutomation) OpenSeatSelection(
	ctx context.Context,
	showtime domain.Showtime,
	_ int,
) (domain.SeatSelection, error) {
	automation.opened <- showtime
	<-ctx.Done()
	return domain.SeatSelection{}, ctx.Err()
}

func (automation *executionAutomation) Close() {
	automation.closeOnce.Do(func() { close(automation.closed) })
}

func TestExecuteAvailabilityClosesBrowserWhenCentralFenceIsCancelled(t *testing.T) {
	ctx := t.Context()
	store := memoryrepo.New()
	now := time.Date(2026, time.August, 12, 10, 0, 0, 0, time.UTC)
	theater := domain.Theater{
		ID: "theater", ProviderID: contracts.ProviderCGV, SourceKey: "서울/용산", Region: "서울", Name: "용산",
	}
	auditorium := domain.Auditorium{
		ID: "auditorium", TheaterID: theater.ID, SourceKey: theater.SourceKey + "/IMAX", Name: "IMAX",
	}
	preset := domain.Preset{
		ID: "preset", UserID: "user", TheaterID: theater.ID, AuditoriumID: auditorium.ID,
		SeatCount: 1, SeatPreference: domain.SeatPreference{
			CandidateSeats: []string{"H10"}, Adjacency: domain.SeatAdjacencyRequired,
		},
	}
	monitor := domain.MonitorJob{
		ID: "monitor", UserID: "user", PresetID: preset.ID, MovieID: "movie_1", Movie: "영화",
		TargetDates: []string{"2026-08-20"}, PollInterval: time.Minute,
		Status: domain.MonitorPending,
	}
	seatMap := domain.SeatMap{AuditoriumID: auditorium.ID, Seats: []domain.Seat{{
		ID: "seat", AuditoriumID: auditorium.ID, Label: "H10", Row: "H", Number: 10,
		X: .5, Y: .5, Type: domain.SeatTypeStandard,
	}}}
	for _, put := range []func() error{
		func() error { return store.PutTheater(ctx, theater) },
		func() error { return store.PutAuditorium(ctx, auditorium) },
		func() error { return store.PutPreset(ctx, preset) },
		func() error { return store.PutMonitor(ctx, monitor) },
		func() error { return store.PutSeatMap(ctx, seatMap) },
	} {
		if err := put(); err != nil {
			t.Fatal(err)
		}
	}
	automation := &executionAutomation{
		webPaymentAutomation: &webPaymentAutomation{
			webProbeAutomation: &webProbeAutomation{probes: &atomic.Int32{}}, closed: &atomic.Int32{},
		},
		opened: make(chan domain.Showtime, 1), closed: make(chan struct{}),
	}
	server := &Server{
		repository: store, rootContext: ctx, ids: &webAtomicIDs{}, clock: webTestClock{now},
		paymentSessions: make(map[string]*paymentSession),
		factory: func(context.Context, bool, AutomationPurpose, string) (Automation, error) {
			return automation, nil
		},
	}
	executionContext, cancel := context.WithCancel(ctx)
	done := make(chan error, 1)
	showtime := domain.Showtime{
		ID: "source", ProviderID: contracts.ProviderCGV, SourceKey: "0056/2026-08-20/0007/0003",
		MovieID: "movie_1", Movie: "영화", AuditoriumID: auditorium.ID, AuditoriumName: auditorium.Name,
		Date: "2026-08-20", StartsAt: "20:00", EndsAt: "22:00",
		AvailableSeats: 10, Capacity: 100,
	}
	go func() { done <- server.ExecuteAvailability(executionContext, monitor.ID, showtime) }()
	var opened domain.Showtime
	select {
	case opened = <-automation.opened:
	case <-time.After(time.Second):
		t.Fatal("exact showtime was not opened")
	}
	if opened.ID != showtime.ID || opened.StartsAt != showtime.StartsAt || automation.findCalled.Load() {
		t.Fatalf("opened/find = %+v/%t", opened, automation.findCalled.Load())
	}
	cancel()
	select {
	case <-automation.closed:
	case <-time.After(100 * time.Millisecond):
		t.Fatal("fence cancellation did not close the browser")
	}
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("ExecuteAvailability() = %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("ExecuteAvailability() did not stop after fence cancellation")
	}
}

func TestExecuteAvailabilityCancelsBrowserFactoryWhenFenceIsLost(t *testing.T) {
	ctx := t.Context()
	store := memoryrepo.New()
	now := time.Date(2026, time.August, 12, 10, 0, 0, 0, time.UTC)
	theater := domain.Theater{
		ID: "theater", ProviderID: contracts.ProviderCGV, SourceKey: "서울/용산", Region: "서울", Name: "용산",
	}
	auditorium := domain.Auditorium{
		ID: "auditorium", TheaterID: theater.ID, SourceKey: theater.SourceKey + "/IMAX", Name: "IMAX",
	}
	preset := domain.Preset{
		ID: "preset", UserID: "user", TheaterID: theater.ID, AuditoriumID: auditorium.ID,
		SeatCount: 1, SeatPreference: domain.SeatPreference{
			CandidateSeats: []string{"H10"}, Adjacency: domain.SeatAdjacencyRequired,
		},
	}
	monitor := domain.MonitorJob{
		ID: "monitor", UserID: "user", PresetID: preset.ID, MovieID: "movie_1", Movie: "영화",
		TargetDates: []string{"2026-08-20"}, PollInterval: time.Minute,
		Status: domain.MonitorPending,
	}
	seatMap := domain.SeatMap{AuditoriumID: auditorium.ID}
	for _, put := range []func() error{
		func() error { return store.PutTheater(ctx, theater) },
		func() error { return store.PutAuditorium(ctx, auditorium) },
		func() error { return store.PutPreset(ctx, preset) },
		func() error { return store.PutMonitor(ctx, monitor) },
		func() error { return store.PutSeatMap(ctx, seatMap) },
	} {
		if err := put(); err != nil {
			t.Fatal(err)
		}
	}
	factoryStarted := make(chan struct{})
	factoryCancelled := make(chan struct{})
	server := &Server{
		repository: store, rootContext: ctx, ids: &webAtomicIDs{}, clock: webTestClock{now},
		paymentSessions: make(map[string]*paymentSession),
		factory: func(openContext context.Context, _ bool, _ AutomationPurpose, _ string) (Automation, error) {
			close(factoryStarted)
			<-openContext.Done()
			close(factoryCancelled)
			return nil, openContext.Err()
		},
	}
	executionContext, cancel := context.WithCancel(ctx)
	done := make(chan error, 1)
	go func() {
		done <- server.ExecuteAvailability(executionContext, monitor.ID, domain.Showtime{
			ID: "source", ProviderID: contracts.ProviderCGV, SourceKey: "0056/2026-08-20/0007/0003",
			MovieID: "movie_1", Movie: "영화", AuditoriumID: auditorium.ID, AuditoriumName: auditorium.Name,
			Date: "2026-08-20", StartsAt: "20:00", EndsAt: "22:00",
			AvailableSeats: 10, Capacity: 100,
		})
	}()
	select {
	case <-factoryStarted:
	case <-time.After(time.Second):
		t.Fatal("browser factory did not start")
	}
	cancel()
	select {
	case <-factoryCancelled:
	case <-time.After(100 * time.Millisecond):
		t.Fatal("fence cancellation did not interrupt browser factory")
	}
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("ExecuteAvailability() = %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("ExecuteAvailability() did not stop after factory cancellation")
	}
}
