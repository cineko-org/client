package webui

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/cineko-org/client/internal/domain"
	"github.com/cineko-org/client/internal/testsupport/memoryrepo"
	catalogpb "github.com/cineko-org/contracts/v3/gen/go/cineko/catalog"
	clientpb "github.com/cineko-org/contracts/v3/gen/go/cineko/client"
	seatmappb "github.com/cineko-org/contracts/v3/gen/go/cineko/seatmap"
)

type executionAutomation struct {
	*webPaymentAutomation
	opened    chan *catalogpb.Showtime
	closed    chan struct{}
	closeOnce sync.Once
}

func (automation *executionAutomation) OpenSeatSelection(
	ctx context.Context,
	showtime *catalogpb.Showtime,
	_ int,
) (*seatmappb.Snapshot, []*seatmappb.Seat, error) {
	automation.opened <- showtime
	<-ctx.Done()
	return nil, nil, ctx.Err()
}

func (*executionAutomation) PreparePayment(context.Context, *catalogpb.Showtime, []string) (*clientpb.Reservation, error) {
	return nil, nil
}

func (*executionAutomation) PrepareCancellation(context.Context, *clientpb.Reservation) (*clientpb.WebUICancellationResult, error) {
	return nil, nil
}

func (automation *executionAutomation) Close() {
	automation.closeOnce.Do(func() { close(automation.closed) })
}

func TestExecuteAvailabilityClosesBrowserWhenCentralFenceIsCancelled(t *testing.T) {
	ctx := t.Context()
	store := memoryrepo.New()
	now := time.Date(2026, time.August, 12, 10, 0, 0, 0, time.UTC)
	theater := domain.Theater{
		ID: "theater", ProviderID: "cgv", SourceKey: "서울/용산", Region: "서울", Name: "용산",
	}
	auditorium := domain.Auditorium{
		ID: "auditorium", TheaterID: theater.ID, SourceKey: theater.SourceKey + "/IMAX", Name: "IMAX",
	}
	preset := presetProtoFixture(theater.ID, auditorium.ID, []string{"H10"})
	monitor := monitorProtoFixture(
		preset.GetId(), "movie_1", "영화", []string{"2026-08-20"},
		clientpb.MonitorState_builder{Pending: clientpb.MonitorPending_builder{}.Build()}.Build(), "",
	)
	seatMap := domain.SeatMap{AuditoriumID: auditorium.ID, Seats: []domain.Seat{{
		ID: "seat", AuditoriumID: auditorium.ID, Label: "H10", Row: "H", Number: 10,
		X: .5, Y: .5, Type: domain.SeatTypeStandard,
	}}}
	for _, put := range []func() error{
		func() error { return store.PutTheater(ctx, theaterProtoForTest(theater)) },
		func() error { return store.PutAuditorium(ctx, auditoriumToProto(auditorium)) },
		func() error { return store.PutPreset(ctx, resourceFromPreset(preset)) },
		func() error { return store.PutMonitor(ctx, resourceFromMonitor(monitor)) },
		func() error { return store.PutSeatMap(ctx, seatMapSnapshot(seatMap)) },
	} {
		if err := put(); err != nil {
			t.Fatal(err)
		}
	}
	automation := &executionAutomation{
		webPaymentAutomation: &webPaymentAutomation{
			webProbeAutomation: &webProbeAutomation{probes: &atomic.Int32{}}, closed: &atomic.Int32{},
		},
		opened: make(chan *catalogpb.Showtime, 1), closed: make(chan struct{}),
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
		ID: "source", ProviderID: "cgv", SourceKey: "0056/2026-08-20/0007/0003",
		MovieID: "movie_1", Movie: "영화", TheaterID: theater.ID, AuditoriumID: auditorium.ID, AuditoriumName: auditorium.Name,
		Date: "2026-08-20", StartsAt: "20:00", EndsAt: "22:00",
		AvailableSeats: 10, Capacity: 100,
	}
	go func() {
		done <- server.ExecuteAvailability(executionContext, monitor.GetId(), showtimeProtoForTest(showtime))
	}()
	var opened *catalogpb.Showtime
	select {
	case opened = <-automation.opened:
	case <-time.After(time.Second):
		t.Fatal("exact showtime was not opened")
	}
	if opened.GetId() != showtime.ID ||
		opened.GetStartsAt().AsTime().In(domain.KoreaLocation).Format("15:04") != showtime.StartsAt {
		t.Fatalf("opened showtime = %+v", opened)
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
		ID: "theater", ProviderID: "cgv", SourceKey: "서울/용산", Region: "서울", Name: "용산",
	}
	auditorium := domain.Auditorium{
		ID: "auditorium", TheaterID: theater.ID, SourceKey: theater.SourceKey + "/IMAX", Name: "IMAX",
	}
	preset := presetProtoFixture(theater.ID, auditorium.ID, []string{"H10"})
	monitor := monitorProtoFixture(
		preset.GetId(), "movie_1", "영화", []string{"2026-08-20"},
		clientpb.MonitorState_builder{Pending: clientpb.MonitorPending_builder{}.Build()}.Build(), "",
	)
	seatMap := domain.SeatMap{AuditoriumID: auditorium.ID}
	for _, put := range []func() error{
		func() error { return store.PutTheater(ctx, theaterProtoForTest(theater)) },
		func() error { return store.PutAuditorium(ctx, auditoriumToProto(auditorium)) },
		func() error { return store.PutPreset(ctx, resourceFromPreset(preset)) },
		func() error { return store.PutMonitor(ctx, resourceFromMonitor(monitor)) },
		func() error { return store.PutSeatMap(ctx, seatMapSnapshot(seatMap)) },
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
		done <- server.ExecuteAvailability(executionContext, monitor.GetId(), showtimeProtoForTest(domain.Showtime{
			ID: "source", ProviderID: "cgv", SourceKey: "0056/2026-08-20/0007/0003",
			MovieID: "movie_1", Movie: "영화", TheaterID: theater.ID, AuditoriumID: auditorium.ID, AuditoriumName: auditorium.Name,
			Date: "2026-08-20", StartsAt: "20:00", EndsAt: "22:00",
			AvailableSeats: 10, Capacity: 100,
		}))
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
