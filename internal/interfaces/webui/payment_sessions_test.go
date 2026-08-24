package webui

import (
	"context"
	"fmt"
	"sync/atomic"
	"testing"
	"time"

	"github.com/cineko-org/client/internal/domain"
	"github.com/cineko-org/client/internal/testsupport/memoryrepo"
	catalogpb "github.com/cineko-org/contracts/v3/gen/go/cineko/catalog"
	clientpb "github.com/cineko-org/contracts/v3/gen/go/cineko/client"
	observationpb "github.com/cineko-org/contracts/v3/gen/go/cineko/observation"
	seatmappb "github.com/cineko-org/contracts/v3/gen/go/cineko/seatmap"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"
)

type webPaymentAutomation struct {
	*webProbeAutomation
	closed   *atomic.Int32
	retained *atomic.Int32
}

func (*webPaymentAutomation) OpenSeatSelection(
	context.Context,
	*observationpb.SeatAvailabilityTask,
	int,
) (*seatmappb.LiveSeatObservation, error) {
	snapshot := seatMapSnapshot(domain.SeatMap{AuditoriumID: "auditorium", Seats: []domain.Seat{{
		Label: "H10", Row: "H", Number: 10, X: .5, Y: .5, Type: domain.SeatTypeStandard,
	}}})
	return gatewayLiveObservationForWeb(snapshot), nil
}

func gatewayLiveObservationForWeb(snapshot *seatmappb.Snapshot) *seatmappb.LiveSeatObservation {
	snapshot = proto.CloneOf(snapshot)
	auditoriumID := snapshot.GetAuditoriumId()
	snapshotID := "snapshot-web"
	layoutHash := "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
	capacity := int32(1)
	observedAt := timestamppb.New(time.Date(2026, time.August, 23, 8, 0, 0, 0, time.UTC))
	snapshot.SetId(snapshotID)
	snapshot.SetLayoutHash(layoutHash)
	snapshot.SetCapacity(capacity)
	snapshot.SetObservedAt(observedAt)
	for index, seat := range snapshot.GetLayout().GetSeats() {
		if seat.GetId() == "" {
			seat.SetId(fmt.Sprintf("seat-%d", index+1))
		}
		seat.SetAuditoriumId(auditoriumID)
	}
	showtimeID := "showtime"
	available := make([]*seatmappb.AvailableSeat, 0, len(snapshot.GetLayout().GetSeats()))
	for _, seat := range snapshot.GetLayout().GetSeats() {
		seatID := seat.GetId()
		available = append(available, seatmappb.AvailableSeat_builder{SeatId: &seatID}.Build())
	}
	availability := seatmappb.AvailabilitySnapshot_builder{
		ShowtimeId: &showtimeID, AuditoriumId: &auditoriumID,
		LayoutHash: &layoutHash, AvailableSeats: available, ObservedAt: observedAt,
	}.Build()
	return seatmappb.LiveSeatObservation_builder{Layout: snapshot, Availability: availability}.Build()
}

func (*webPaymentAutomation) PreparePayment(
	_ context.Context,
	showtime *catalogpb.Showtime,
	labels []string,
) (*clientpb.Reservation, error) {
	price := "20,000원"
	return clientpb.Reservation_builder{Showtime: showtime, SeatLabels: labels, TotalPrice: &price}.Build(), nil
}

func (automation *webPaymentAutomation) Close() { automation.closed.Add(1) }

func (automation *webPaymentAutomation) RetainPayment() error {
	if automation.retained != nil {
		automation.retained.Add(1)
	}
	return nil
}

func TestPreparedPaymentKeepsBrowserAndReusesMonitorOnRetry(t *testing.T) {
	ctx, cancelRoot := context.WithCancel(t.Context())
	defer cancelRoot()
	taskContext, cancelTask := context.WithCancel(ctx)
	store := memoryrepo.New()
	now := time.Date(2026, 8, 12, 20, 0, 0, 0, time.FixedZone("KST", 9*60*60))
	theater := domain.Theater{ID: "theater", Name: "용산"}
	auditorium := domain.Auditorium{ID: "auditorium", TheaterID: theater.ID, Name: "IMAX"}
	preset := presetProtoFixture(theater.ID, auditorium.ID, []string{"H10"})
	monitor := monitorProtoFixture(
		preset.GetId(), "movie_1", "영화", []string{"2026-08-20"},
		clientpb.MonitorState_builder{Pending: clientpb.MonitorPending_builder{}.Build()}.Build(), "",
	)
	seatMap := domain.SeatMap{
		AuditoriumID: auditorium.ID,
		Seats:        []domain.Seat{{ID: "seat", Label: "H10", Row: "H", Number: 10, X: 0.5, Y: 0.5, Type: domain.SeatTypeStandard}},
	}
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
	var closed atomic.Int32
	var retained atomic.Int32
	var factoryCalls atomic.Int32
	var factoryDone <-chan struct{}
	automation := &webPaymentAutomation{
		webProbeAutomation: &webProbeAutomation{probes: &atomic.Int32{}}, closed: &closed, retained: &retained,
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
	if err := server.ExecuteAvailability(taskContext, monitor.GetId(), showtimeProtoForTest(domain.Showtime{
		ID: "showtime", ProviderID: "cgv", SourceKey: "0056/2026-08-20/0007/0003",
		MovieID: "movie_1", Movie: "영화", TheaterID: theater.ID, AuditoriumID: auditorium.ID,
		Date: "2026-08-20", StartsAt: "20:00", EndsAt: "22:00", AvailableSeats: 1, Capacity: 100,
	}), true); err != nil {
		t.Fatal(err)
	}
	cancelTask()
	select {
	case <-factoryDone:
		t.Fatal("payment browser inherited execution cancellation")
	default:
	}
	if !server.hasPaymentSession(monitor.GetId()) || retained.Load() != 1 || closed.Load() != 0 {
		t.Fatalf("active/retained/closed = %t/%d/%d", server.hasPaymentSession(monitor.GetId()), retained.Load(), closed.Load())
	}
	if err := server.ExecuteAvailability(ctx, monitor.GetId(), showtimeProtoForTest(domain.Showtime{}), true); err != nil || factoryCalls.Load() != 1 {
		t.Fatalf("duplicate execution error/calls = %v/%d", err, factoryCalls.Load())
	}
	retainedMonitor, err := store.GetMonitor(ctx, monitor.GetId())
	if err != nil || retainedMonitor.GetMonitor().GetState().GetTriggered() == nil || retainedMonitor.GetMonitor().GetReservationId() == "" {
		t.Fatalf("retained monitor = %+v, %v", retainedMonitor, err)
	}
	reused, err := server.abandonPaymentSession(ctx, monitor.GetId())
	if err != nil || !reused || closed.Load() != 1 {
		t.Fatalf("abandon result/closed = %t/%v/%d", reused, err, closed.Load())
	}
	reactivated, err := store.GetMonitor(ctx, monitor.GetId())
	if err != nil || reactivated.GetMonitor().GetState().GetPending() == nil || reactivated.GetMonitor().GetReservationId() != "" {
		t.Fatalf("reactivated monitor = %+v, %v", reactivated, err)
	}
	reservation, err := store.GetReservation(ctx, retainedMonitor.GetMonitor().GetReservationId())
	if err != nil || reservation.GetReservation().GetPrepared() == nil {
		t.Fatalf("abandoned reservation = %+v, %v", reservation, err)
	}
}

func TestPaymentSessionExpirationClosesBrowserAndReactivatesMonitor(t *testing.T) {
	ctx := t.Context()
	store := memoryrepo.New()
	now := time.Date(2026, 8, 12, 20, 0, 0, 0, time.UTC)
	monitor := monitorProtoFixture(
		"", "", "", nil,
		clientpb.MonitorState_builder{Triggered: clientpb.MonitorTriggered_builder{}.Build()}.Build(), "reservation",
	)
	reservation := reservationProtoFixture("reservation", "user", monitor.GetId())
	if err := store.PutMonitor(ctx, resourceFromMonitor(monitor)); err != nil {
		t.Fatal(err)
	}
	if err := store.PutReservation(ctx, resourceFromReservation(reservation)); err != nil {
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
	server.retainPaymentSession(monitor.GetId(), resourceFromReservation(reservation), automation)
	server.paymentMu.Lock()
	session := server.paymentSessions[monitor.GetId()]
	server.paymentMu.Unlock()
	server.expirePaymentSession(monitor.GetId(), session)
	if server.hasPaymentSession(monitor.GetId()) || closed.Load() != 1 {
		t.Fatalf("active/closed = %t/%d", server.hasPaymentSession(monitor.GetId()), closed.Load())
	}
	updatedMonitor, _ := store.GetMonitor(ctx, monitor.GetId())
	updatedReservation, _ := store.GetReservation(ctx, reservation.GetId())
	if updatedMonitor.GetMonitor().GetState().GetPaymentUnknown() == nil || updatedReservation.GetReservation().GetPrepared() == nil ||
		updatedMonitor.GetMonitor().GetReservationId() != reservation.GetId() {
		t.Fatalf("expired state = %+v / %+v", updatedMonitor, updatedReservation)
	}
	if err := server.ExecuteAvailability(ctx, monitor.GetId(), showtimeProtoForTest(domain.Showtime{}), true); err != nil {
		t.Fatalf("unknown payment accepted a duplicate execution: %v", err)
	}
	events, err := store.ListAppEvents(ctx, monitor.GetUserId(), 10)
	if err != nil || len(events) != 1 || events[0].GetAppEvent().GetKind() != "payment.expired" || events[0].GetAppEvent().GetWarning() == nil {
		t.Fatalf("expiration events = %+v, %v", events, err)
	}
}

func TestRetryRecoversPersistedPaymentAttemptAfterAppRestart(t *testing.T) {
	ctx := t.Context()
	store := memoryrepo.New()
	monitor := monitorProtoFixture(
		"", "", "", nil,
		clientpb.MonitorState_builder{Triggered: clientpb.MonitorTriggered_builder{}.Build()}.Build(), "reservation",
	)
	reservation := reservationProtoFixture("reservation", "user", monitor.GetId())
	if err := store.PutMonitor(ctx, resourceFromMonitor(monitor)); err != nil {
		t.Fatal(err)
	}
	if err := store.PutReservation(ctx, resourceFromReservation(reservation)); err != nil {
		t.Fatal(err)
	}
	server := &Server{
		repository: store, clock: webTestClock{time.Date(2026, 8, 12, 20, 0, 0, 0, time.UTC)},
		paymentSessions: make(map[string]*paymentSession),
	}
	recovered, err := server.abandonPaymentSession(ctx, monitor.GetId())
	if err != nil || !recovered {
		t.Fatalf("recover persisted payment = %t, %v", recovered, err)
	}
	updatedMonitor, _ := store.GetMonitor(ctx, monitor.GetId())
	updatedReservation, _ := store.GetReservation(ctx, reservation.GetId())
	if updatedMonitor.GetMonitor().GetState().GetPending() == nil || updatedMonitor.GetMonitor().GetReservationId() != "" ||
		updatedReservation.GetReservation().GetPrepared() == nil {
		t.Fatalf("recovered state = %+v / %+v", updatedMonitor, updatedReservation)
	}
}
