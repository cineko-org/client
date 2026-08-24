package application

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/cineko-org/client/internal/domain"
	catalogpb "github.com/cineko-org/contracts/v3/gen/go/cineko/catalog"
	clientpb "github.com/cineko-org/contracts/v3/gen/go/cineko/client"
	observationpb "github.com/cineko-org/contracts/v3/gen/go/cineko/observation"
	seatmappb "github.com/cineko-org/contracts/v3/gen/go/cineko/seatmap"
	"google.golang.org/protobuf/types/known/timestamppb"
)

func TestMonitorServiceKeepsIndefiniteWeekdayRule(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, time.August, 9, 12, 0, 0, 0, time.UTC)
	repository := &workerRepository{preset: presetFixtureForTest("preset-1", "user-1", "theater-1", "auditorium-1", []string{"A1"})}
	service := NewMonitorService(repository, repository, &sequenceIDs{}, fixedClock{now: now})

	resource, err := service.Create(context.Background(), monitorMutationForTest(0, "", "", "user-1", "preset-1", "movie_1", "오디세이", nil, []int{int(time.Saturday)}, 0, "", ""))
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	job := resource.GetMonitor()
	if len(job.GetTargetWeekdays()) != 1 || job.GetTargetWeekdays()[0] != int32(time.Saturday) {
		t.Fatalf("TargetWeekdays = %v, want Saturday", job.GetTargetWeekdays())
	}
}

func TestMonitorServiceTurnsMonitoringOffAndBackOn(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, time.August, 23, 12, 0, 0, 0, time.UTC)
	repository := &workerRepository{job: monitorFixtureForTest("오디세이", []string{"2026-08-24"})}
	service := NewMonitorService(repository, repository, &sequenceIDs{}, fixedClock{now: now})

	stoppedResource, err := service.SetEnabled(context.Background(), "user", "monitor", false)
	if err != nil {
		t.Fatalf("SetEnabled(false) error = %v", err)
	}
	stopped := stoppedResource.GetMonitor().GetState().GetStopped()
	if stopped == nil || stopped.GetReason() != "user_disabled" {
		t.Fatalf("SetEnabled(false) state = %v, want stopped(user_disabled)", stoppedResource.GetMonitor().GetState())
	}

	resumedResource, err := service.SetEnabled(context.Background(), "user", "monitor", true)
	if err != nil {
		t.Fatalf("SetEnabled(true) error = %v", err)
	}
	if resumedResource.GetMonitor().GetState().GetPending() == nil {
		t.Fatalf("SetEnabled(true) state = %v, want pending", resumedResource.GetMonitor().GetState())
	}
}

type workerRepository struct {
	job         *clientpb.Monitor
	preset      *clientpb.Preset
	theater     domain.Theater
	auditorium  domain.Auditorium
	seatMap     domain.SeatMap
	reservation *clientpb.Reservation
}

func (repository *workerRepository) PutMonitor(_ context.Context, resource *clientpb.Resource) error {
	job, _, err := monitorMessage(resource)
	if err != nil {
		return err
	}
	repository.job = cloneMonitor(job)
	return nil
}
func (repository *workerRepository) GetMonitor(context.Context, string) (*clientpb.Resource, error) {
	return resourceForMonitor(cloneMonitor(repository.job), 0), nil
}
func (repository *workerRepository) ListMonitorsByUser(context.Context, string) ([]*clientpb.Resource, error) {
	return []*clientpb.Resource{resourceForMonitor(cloneMonitor(repository.job), 0)}, nil
}
func (repository *workerRepository) DeleteMonitor(context.Context, string) error { return nil }
func (repository *workerRepository) GetPreset(context.Context, string) (*clientpb.Resource, error) {
	return resourceForPreset(clonePreset(repository.preset), 0), nil
}
func (repository *workerRepository) PutPreset(context.Context, *clientpb.Resource) error { return nil }
func (repository *workerRepository) ListPresetsByUser(context.Context, string) ([]*clientpb.Resource, error) {
	return []*clientpb.Resource{resourceForPreset(clonePreset(repository.preset), 0)}, nil
}
func (repository *workerRepository) DeletePreset(context.Context, string) error { return nil }
func (repository *workerRepository) GetTheater(context.Context, string) (*catalogpb.Theater, error) {
	id, providerID := repository.theater.ID, repository.theater.ProviderID
	region, name := repository.theater.Region, repository.theater.Name
	return catalogpb.Theater_builder{
		Id: &id, ProviderId: &providerID, Identity: catalogTestTheaterIdentity(repository.theater.SourceKey),
		Region: &region, Name: &name,
	}.Build(), nil
}
func (repository *workerRepository) PutTheater(context.Context, *catalogpb.Theater) error { return nil }
func (repository *workerRepository) ListTheaters(ctx context.Context) ([]*catalogpb.Theater, error) {
	value, err := repository.GetTheater(ctx, repository.theater.ID)
	return []*catalogpb.Theater{value}, err
}
func (repository *workerRepository) GetAuditorium(context.Context, string) (*catalogpb.Auditorium, error) {
	id, theaterID, name := repository.auditorium.ID, repository.auditorium.TheaterID, repository.auditorium.Name
	capacity, _ := int32Checked(repository.auditorium.Capacity, "auditorium capacity")
	parts := strings.Split(repository.auditorium.SourceKey, "/")
	siteNo, screenNo := "56", "7"
	if len(parts) >= 2 {
		siteNo, screenNo = parts[0], parts[len(parts)-1]
	}
	return catalogpb.Auditorium_builder{Id: &id, TheaterId: &theaterID,
		Identity: catalogTestAuditoriumIdentity(siteNo, screenNo), Name: &name,
		ScreenTypes: append([]string(nil), repository.auditorium.ScreenTypes...), Capacity: &capacity}.Build(), nil
}
func (repository *workerRepository) PutAuditorium(context.Context, *catalogpb.Auditorium) error {
	return nil
}
func (repository *workerRepository) ListAuditoriumsByTheater(ctx context.Context, _ string) ([]*catalogpb.Auditorium, error) {
	value, err := repository.GetAuditorium(ctx, repository.auditorium.ID)
	return []*catalogpb.Auditorium{value}, err
}
func (repository *workerRepository) GetSeatMap(context.Context, string) (*seatmappb.Snapshot, error) {
	auditoriumID, layoutHash := repository.seatMap.AuditoriumID, repository.seatMap.Version
	return seatmappb.Snapshot_builder{AuditoriumId: &auditoriumID, LayoutHash: &layoutHash}.Build(), nil
}
func (repository *workerRepository) PutSeatMap(context.Context, *seatmappb.Snapshot) error {
	return nil
}
func (repository *workerRepository) PutReservation(_ context.Context, resource *clientpb.Resource) error {
	value, _, err := reservationMessage(resource)
	if err != nil {
		return err
	}
	repository.reservation = cloneReservation(value)
	return nil
}
func (repository *workerRepository) GetReservation(context.Context, string) (*clientpb.Resource, error) {
	return resourceForReservation(cloneReservation(repository.reservation), 0), nil
}
func (repository *workerRepository) ListReservationsByUser(context.Context, string) ([]*clientpb.Resource, error) {
	return []*clientpb.Resource{resourceForReservation(cloneReservation(repository.reservation), 0)}, nil
}

type workerGateway struct {
	live []domain.LiveSeat
}

func (gateway *workerGateway) OpenSeatSelection(
	context.Context,
	*observationpb.SeatAvailabilityTask,
	int,
) (*seatmappb.LiveSeatObservation, error) {
	snapshot := gatewaySeatSnapshot()
	return gatewayLiveObservation(snapshot, gateway.live), nil
}

func gatewaySeatMap() domain.SeatMap {
	return domain.SeatMap{AuditoriumID: "auditorium-1", Seats: []domain.Seat{{
		Label: "H10", Row: "H", Number: 10, X: .5, Y: .55, Type: domain.SeatTypeStandard,
	}}}
}
func (gateway *workerGateway) PreparePayment(
	_ context.Context, showtime *catalogpb.Showtime, seats []string,
) (*clientpb.Reservation, error) {
	return clientpb.Reservation_builder{Showtime: showtime, SeatLabels: append([]string(nil), seats...)}.Build(), nil
}
func (gateway *workerGateway) PrepareCancellation(context.Context, *clientpb.Reservation) (*clientpb.WebUICancellationResult, error) {
	return nil, fmt.Errorf("not used")
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

type liveObservationRepositoryFake struct {
	requests            []*seatmappb.LiveSeatObservation
	err                 error
	snapshot            *seatmappb.Snapshot
	response            *seatmappb.Snapshot
	onSubmit            func(*seatmappb.LiveSeatObservation)
	waitForCancellation bool
}

func (repository *liveObservationRepositoryFake) SubmitLiveSeatObservation(
	ctx context.Context,
	observation *seatmappb.LiveSeatObservation,
) (*seatmappb.Snapshot, error) {
	repository.requests = append(repository.requests, observation)
	if repository.onSubmit != nil {
		repository.onSubmit(observation)
	}
	if repository.waitForCancellation {
		<-ctx.Done()
		return nil, ctx.Err()
	}
	if repository.err != nil {
		return nil, repository.err
	}
	if repository.response != nil {
		return repository.response, nil
	}
	snapshot := repository.snapshot
	if snapshot == nil {
		snapshot = observation.GetLayout()
	}
	return snapshot, nil
}

func gatewaySeatSnapshot() *seatmappb.Snapshot {
	seatID, auditoriumID, label, row := "seat", "auditorium-1", "H10", "H"
	snapshotID, layoutHash := "snapshot", testLayoutHashForApplication
	capacity := int32(1)
	number := int32(10)
	seat := seatmappb.Seat_builder{Id: &seatID, AuditoriumId: &auditoriumID, Label: &label, Row: &row, Number: &number, Type: stringPointerForTest("standard")}.Build()
	return seatmappb.Snapshot_builder{
		Id: &snapshotID, AuditoriumId: &auditoriumID, LayoutHash: &layoutHash,
		Capacity: &capacity, Layout: seatmappb.Layout_builder{Seats: []*seatmappb.Seat{seat}}.Build(),
		ObservedAt: timestamppb.New(time.Date(2026, time.August, 23, 8, 0, 0, 0, time.UTC)),
	}.Build()
}

func gatewayAvailableSeats(snapshot *seatmappb.Snapshot, live []domain.LiveSeat) []*seatmappb.Seat {
	available := map[string]bool{}
	for _, seat := range live {
		available[seat.Label] = seat.Available
	}
	result := make([]*seatmappb.Seat, 0, len(snapshot.GetLayout().GetSeats()))
	for _, seat := range snapshot.GetLayout().GetSeats() {
		if available[seat.GetLabel()] {
			result = append(result, seat)
		}
	}
	return result
}

func gatewayLiveObservation(snapshot *seatmappb.Snapshot, live []domain.LiveSeat) *seatmappb.LiveSeatObservation {
	showtimeID := "showtime"
	available := gatewayAvailableSeats(snapshot, live)
	availableIDs := make([]*seatmappb.AvailableSeat, 0, len(available))
	for _, seat := range available {
		seatID := seat.GetId()
		availableIDs = append(availableIDs, seatmappb.AvailableSeat_builder{SeatId: &seatID}.Build())
	}
	auditoriumID, layoutHash := snapshot.GetAuditoriumId(), snapshot.GetLayoutHash()
	availability := seatmappb.AvailabilitySnapshot_builder{
		ShowtimeId: &showtimeID, AuditoriumId: &auditoriumID,
		LayoutHash: &layoutHash, AvailableSeats: availableIDs,
		ObservedAt: snapshot.GetObservedAt(),
	}.Build()
	return seatmappb.LiveSeatObservation_builder{Layout: snapshot, Availability: availability}.Build()
}

func stringPointerForTest(value string) *string { return &value }

const testLayoutHashForApplication = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
