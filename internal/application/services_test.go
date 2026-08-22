package application

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/cineko-org/client/internal/domain"
	catalogpb "github.com/cineko-org/contracts/gen/go/cineko/catalog"
	clientpb "github.com/cineko-org/contracts/gen/go/cineko/client"
	seatmappb "github.com/cineko-org/contracts/gen/go/cineko/seatmap"
)

func TestMonitorServiceDefaultsRollingWeekdayHorizon(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, time.August, 9, 12, 0, 0, 0, time.UTC)
	repository := &workerRepository{preset: presetFixtureForTest("preset-1", "user-1", "theater-1", "auditorium-1", []string{"A1"})}
	service := NewMonitorService(repository, repository, &sequenceIDs{}, fixedClock{now: now})

	resource, err := service.Create(context.Background(), monitorMutationForTest(0, "", "", "user-1", "preset-1", "movie_1", "오디세이", nil, []int{int(time.Saturday)}, 0, "", ""))
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	job := resource.GetMonitor()
	if job.GetSearchHorizonDays() != defaultSearchHorizonDays {
		t.Fatalf("SearchHorizonDays = %d, want %d", job.GetSearchHorizonDays(), defaultSearchHorizonDays)
	}
}

func TestMonitorServiceRejectsExpiredExactDates(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, time.August, 9, 12, 0, 0, 0, time.UTC)
	repository := &workerRepository{preset: presetFixtureForTest("preset-1", "user-1", "theater-1", "auditorium-1", []string{"A1"})}
	service := NewMonitorService(repository, repository, &sequenceIDs{}, fixedClock{now: now})

	_, err := service.Create(context.Background(), monitorMutationForTest(0, "", "", "user-1", "preset-1", "movie_1", "오디세이", []string{"2026-08-08"}, nil, 0, "", ""))
	if !errors.Is(err, ErrMonitorExpired) {
		t.Fatalf("Create() error = %v, want %v", err, ErrMonitorExpired)
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
	id, providerID, sourceKey := repository.theater.ID, repository.theater.ProviderID, repository.theater.SourceKey
	region, name := repository.theater.Region, repository.theater.Name
	return catalogpb.Theater_builder{Id: &id, ProviderId: &providerID, SourceKey: &sourceKey, Region: &region, Name: &name}.Build(), nil
}
func (repository *workerRepository) PutTheater(context.Context, *catalogpb.Theater) error { return nil }
func (repository *workerRepository) ListTheaters(ctx context.Context) ([]*catalogpb.Theater, error) {
	value, err := repository.GetTheater(ctx, repository.theater.ID)
	return []*catalogpb.Theater{value}, err
}
func (repository *workerRepository) GetAuditorium(context.Context, string) (*catalogpb.Auditorium, error) {
	id, theaterID, sourceKey, name := repository.auditorium.ID, repository.auditorium.TheaterID, repository.auditorium.SourceKey, repository.auditorium.Name
	capacity, _ := int32Checked(repository.auditorium.Capacity, "auditorium capacity")
	return catalogpb.Auditorium_builder{Id: &id, TheaterId: &theaterID, SourceKey: &sourceKey, Name: &name,
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
	*catalogpb.Showtime,
	int,
) (*seatmappb.Snapshot, []*seatmappb.Seat, error) {
	snapshot := gatewaySeatSnapshot()
	return snapshot, gatewayAvailableSeats(snapshot, gateway.live), nil
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

func gatewaySeatSnapshot() *seatmappb.Snapshot {
	seatID, auditoriumID, label, row := "seat", "auditorium-1", "H10", "H"
	number := int32(10)
	seat := seatmappb.Seat_builder{Id: &seatID, AuditoriumId: &auditoriumID, Label: &label, Row: &row, Number: &number, Type: stringPointerForTest("standard")}.Build()
	return seatmappb.Snapshot_builder{AuditoriumId: &auditoriumID, Layout: seatmappb.Layout_builder{Seats: []*seatmappb.Seat{seat}}.Build()}.Build()
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

func stringPointerForTest(value string) *string { return &value }
