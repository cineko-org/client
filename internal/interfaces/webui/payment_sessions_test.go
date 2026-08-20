package webui

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/cineko-org/client/internal/application"
	"github.com/cineko-org/client/internal/domain"
	"github.com/cineko-org/client/internal/testsupport/memoryrepo"
)

type webPaymentAutomation struct {
	*webProbeAutomation
	closed *atomic.Int32
}

func (*webPaymentAutomation) FindShowtimes(context.Context, application.ShowtimeQuery) ([]domain.Showtime, error) {
	return []domain.Showtime{{ID: "showtime", Movie: "영화", Date: "2026-08-20", StartsAt: "20:00"}}, nil
}

func (*webPaymentAutomation) OpenSeatSelection(
	context.Context,
	domain.Showtime,
	int,
) (domain.SeatSelection, error) {
	return domain.SeatSelection{
		SeatMap: domain.SeatMap{AuditoriumID: "auditorium", Seats: []domain.Seat{{
			Label: "H10", Row: "H", Number: 10, X: .5, Y: .5, Type: domain.SeatTypeStandard,
		}}},
		LiveSeats: []domain.LiveSeat{{Label: "H10", Available: true}},
	}, nil
}

func (*webPaymentAutomation) PreparePayment(
	_ context.Context,
	showtime domain.Showtime,
	labels []string,
) (domain.BookingDraft, error) {
	return domain.BookingDraft{Showtime: showtime, SeatLabels: labels, TotalPrice: "20,000원"}, nil
}

func (automation *webPaymentAutomation) Close() { automation.closed.Add(1) }

func TestPreparedPaymentKeepsBrowserAndReusesMonitorOnRetry(t *testing.T) {
	ctx, cancelRoot := context.WithCancel(t.Context())
	defer cancelRoot()
	taskContext, cancelTask := context.WithCancel(ctx)
	store := memoryrepo.New()
	now := time.Date(2026, 8, 12, 20, 0, 0, 0, time.FixedZone("KST", 9*60*60))
	theater := domain.Theater{ID: "theater", Name: "용산"}
	auditorium := domain.Auditorium{ID: "auditorium", TheaterID: theater.ID, Name: "IMAX"}
	preset := domain.Preset{
		ID: "preset", UserID: "user", TheaterID: theater.ID, AuditoriumID: auditorium.ID,
		SeatCount: 1, SeatPreference: domain.SeatPreference{
			CandidateSeats: []string{"H10"}, Adjacency: domain.SeatAdjacencyRequired,
		},
	}
	monitor := domain.MonitorJob{
		ID: "monitor", UserID: "user", PresetID: preset.ID, MovieID: "movie", Movie: "영화",
		TargetDates: []string{"2026-08-20"}, PollInterval: time.Minute, PollIntervalMax: 2 * time.Minute,
	}
	seatMap := domain.SeatMap{
		AuditoriumID: auditorium.ID,
		Seats:        []domain.Seat{{ID: "seat", Label: "H10", Row: "H", Number: 10, X: 0.5, Y: 0.5, Type: domain.SeatTypeStandard}},
	}
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
	var closed atomic.Int32
	var factoryCalls atomic.Int32
	var factoryDone <-chan struct{}
	automation := &webPaymentAutomation{
		webProbeAutomation: &webProbeAutomation{probes: &atomic.Int32{}}, closed: &closed,
	}
	server := &Server{
		repository: store, rootContext: ctx, ids: &webAtomicIDs{}, clock: webTestClock{now},
		paymentSessions: make(map[string]*paymentSession),
		factory: func(openContext context.Context, _ bool, _ AutomationPurpose, _ string) (Automation, error) {
			factoryCalls.Add(1)
			factoryDone = openContext.Done()
			return automation, nil
		},
	}
	if err := server.runBookingSession(taskContext, monitor.ID, false); err != nil {
		t.Fatal(err)
	}
	cancelTask()
	select {
	case <-factoryDone:
		t.Fatal("payment browser inherited execution cancellation")
	default:
	}
	if !server.hasPaymentSession(monitor.ID) || closed.Load() != 0 {
		t.Fatalf("retained/closed = %t/%d", server.hasPaymentSession(monitor.ID), closed.Load())
	}
	if err := server.ExecuteAvailability(ctx, monitor.ID, domain.Showtime{}); err != nil || factoryCalls.Load() != 1 {
		t.Fatalf("duplicate execution error/calls = %v/%d", err, factoryCalls.Load())
	}
	retainedMonitor, err := store.GetMonitor(ctx, monitor.ID)
	if err != nil || retainedMonitor.Status != domain.MonitorTriggered || retainedMonitor.ReservationID == "" {
		t.Fatalf("retained monitor = %+v, %v", retainedMonitor, err)
	}
	reused, err := server.abandonPaymentSession(ctx, monitor.ID)
	if err != nil || !reused || closed.Load() != 1 {
		t.Fatalf("abandon result/closed = %t/%v/%d", reused, err, closed.Load())
	}
	reactivated, err := store.GetMonitor(ctx, monitor.ID)
	if err != nil || reactivated.Status != domain.MonitorPending || reactivated.ReservationID != "" {
		t.Fatalf("reactivated monitor = %+v, %v", reactivated, err)
	}
	reservation, err := store.GetReservation(ctx, retainedMonitor.ReservationID)
	if err != nil || reservation.Status != "abandoned" {
		t.Fatalf("abandoned reservation = %+v, %v", reservation, err)
	}
}

func TestPaymentSessionExpirationClosesBrowserAndReactivatesMonitor(t *testing.T) {
	ctx := t.Context()
	store := memoryrepo.New()
	now := time.Date(2026, 8, 12, 20, 0, 0, 0, time.UTC)
	monitor := domain.MonitorJob{
		ID: "monitor", UserID: "user", Status: domain.MonitorTriggered, ReservationID: "reservation",
	}
	reservation := domain.Reservation{ID: "reservation", UserID: "user", MonitorID: monitor.ID, Status: "prepared"}
	if err := store.PutMonitor(ctx, monitor); err != nil {
		t.Fatal(err)
	}
	if err := store.PutReservation(ctx, reservation); err != nil {
		t.Fatal(err)
	}
	var closed atomic.Int32
	server := &Server{
		repository: store, rootContext: ctx, ids: &webAtomicIDs{}, clock: webTestClock{now},
		paymentSessions: make(map[string]*paymentSession),
	}
	automation := &webPaymentAutomation{
		webProbeAutomation: &webProbeAutomation{probes: &atomic.Int32{}}, closed: &closed,
	}
	server.retainPaymentSession(monitor.ID, reservation, automation)
	server.paymentMu.Lock()
	session := server.paymentSessions[monitor.ID]
	server.paymentMu.Unlock()
	server.expirePaymentSession(monitor.ID, session)
	if server.hasPaymentSession(monitor.ID) || closed.Load() != 1 {
		t.Fatalf("active/closed = %t/%d", server.hasPaymentSession(monitor.ID), closed.Load())
	}
	updatedMonitor, _ := store.GetMonitor(ctx, monitor.ID)
	updatedReservation, _ := store.GetReservation(ctx, reservation.ID)
	if updatedMonitor.Status != domain.MonitorPaymentUnknown || updatedReservation.Status != "unknown" ||
		updatedMonitor.ReservationID != reservation.ID {
		t.Fatalf("expired state = %+v / %+v", updatedMonitor, updatedReservation)
	}
	if err := server.ExecuteAvailability(ctx, monitor.ID, domain.Showtime{}); err != nil {
		t.Fatalf("unknown payment accepted a duplicate execution: %v", err)
	}
	events, err := store.ListAppEvents(ctx, monitor.UserID, 10)
	if err != nil || len(events) != 1 || events[0].Kind != "payment.expired" || events[0].Tone != domain.EventWarning {
		t.Fatalf("expiration events = %+v, %v", events, err)
	}
}

func TestRetryRecoversPersistedPaymentAttemptAfterAppRestart(t *testing.T) {
	ctx := t.Context()
	store := memoryrepo.New()
	monitor := domain.MonitorJob{
		ID: "monitor", UserID: "user", Status: domain.MonitorTriggered, ReservationID: "reservation",
	}
	reservation := domain.Reservation{ID: "reservation", UserID: "user", MonitorID: monitor.ID, Status: "prepared"}
	if err := store.PutMonitor(ctx, monitor); err != nil {
		t.Fatal(err)
	}
	if err := store.PutReservation(ctx, reservation); err != nil {
		t.Fatal(err)
	}
	server := &Server{
		repository: store, clock: webTestClock{time.Date(2026, 8, 12, 20, 0, 0, 0, time.UTC)},
		paymentSessions: make(map[string]*paymentSession),
	}
	recovered, err := server.abandonPaymentSession(ctx, monitor.ID)
	if err != nil || !recovered {
		t.Fatalf("recover persisted payment = %t, %v", recovered, err)
	}
	updatedMonitor, _ := store.GetMonitor(ctx, monitor.ID)
	updatedReservation, _ := store.GetReservation(ctx, reservation.ID)
	if updatedMonitor.Status != domain.MonitorPending || updatedMonitor.ReservationID != "" ||
		updatedReservation.Status != "abandoned" {
		t.Fatalf("recovered state = %+v / %+v", updatedMonitor, updatedReservation)
	}
}

func TestRemovingLastPaymentSessionWakesExecutionWorker(t *testing.T) {
	server := &Server{
		paymentSessions: map[string]*paymentSession{"monitor": {}},
		executionReady:  make(chan struct{}, 1),
	}
	if session := server.removePaymentSession("monitor", nil); session == nil {
		t.Fatal("payment session was not removed")
	}
	select {
	case <-server.ExecutionAvailable():
	default:
		t.Fatal("last payment session removal did not publish an execution wake")
	}
}

func TestCanAcceptExecutionUsesReadyWarmCapacityForRetainedPayment(t *testing.T) {
	ready := false
	server := &Server{
		paymentSessions:          map[string]*paymentSession{"retained": {}},
		bookingCapacityAvailable: func() bool { return ready },
	}
	if server.CanAcceptExecution() {
		t.Fatal("execution accepted without a ready warm slot")
	}
	ready = true
	if !server.CanAcceptExecution() {
		t.Fatal("ready warm slot was blocked by retained payment")
	}
}
