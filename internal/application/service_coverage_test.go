package application

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/cineko-org/client/internal/domain"
	catalogpb "github.com/cineko-org/contracts/gen/go/cineko/catalog"
	clientpb "github.com/cineko-org/contracts/gen/go/cineko/client"
	seatmappb "github.com/cineko-org/contracts/gen/go/cineko/seatmap"
)

var errInjected = errors.New("injected failure")

func putMonitor(ctx context.Context, repository *monitorRepositoryFake, value *clientpb.Monitor) error {
	return repository.PutMonitor(ctx, resourceForMonitor(value, repository.revision))
}

func cancellationRequest(userID string, commit bool) *clientpb.WebUIReservationCancellationRequest {
	reservationID := "reservation"
	reservation := clientpb.Reservation_builder{Id: &reservationID, UserId: &userID}.Build()
	return clientpb.WebUIReservationCancellationRequest_builder{Reservation: reservation, Commit: &commit}.Build()
}

func TestPresetServiceCoversSuccessAndFailurePaths(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	now := time.Date(2026, time.August, 9, 10, 0, 0, 0, time.UTC)
	request := validPresetRequest()
	preset := request.GetPreset()

	repository := newPresetRepositoryFake()
	service := NewPresetService(repository, &sequenceIDs{}, fixedClock{now})
	createdResource, err := service.Create(ctx, request)
	created := createdResource.GetPreset()
	if err != nil || created == nil || created.GetName() != "center" {
		t.Fatalf("Create() = %+v, %v", createdResource, err)
	}
	storedPreset := repository.values[created.GetId()]
	storedPreset.GetIdentity().SetRevision(1)
	repository.values[created.GetId()] = storedPreset
	updateRequest := clonePresetMutationForTest(request)
	updateRequest.GetMutation().SetExpectedRevision(1)
	updateRequest.GetPreset().SetId(created.GetId())
	updateRequest.GetPreset().SetName("updated")
	updatedResource, err := service.Update(ctx, updateRequest)
	updated := updatedResource.GetPreset()
	if err != nil || updated == nil || updated.GetName() != "updated" {
		t.Fatalf("Update() = %+v, %v", updatedResource, err)
	}
	listed, err := service.List(ctx, preset.GetUserId())
	if err != nil || len(listed) != 1 {
		t.Fatalf("List() = %+v, %v", listed, err)
	}
	if err := service.Delete(ctx, preset.GetUserId(), created.GetId()); err != nil {
		t.Fatalf("Delete() error = %v", err)
	}

	assertPresetCreateError(t, request, func(repository *presetRepositoryFake, _ *seatMapRepositoryFake) {
		repository.listErr = errInjected
	})
	assertPresetCreateError(t, request, func(repository *presetRepositoryFake, _ *seatMapRepositoryFake) {
		repository.values["duplicate"] = presetResourceFixture(presetFixtureForTest("duplicate", preset.GetUserId(), "theater", "auditorium", []string{"A1"}), 0)
	})
	assertPresetCreateError(t, &clientpb.WebUIResourceMutation{}, nil)
	assertPresetCreateError(t, request, func(repository *presetRepositoryFake, _ *seatMapRepositoryFake) {
		repository.putErr = errInjected
	})
	for name, configure := range map[string]func(*presetRepositoryFake, *seatMapRepositoryFake){
		"get": func(repository *presetRepositoryFake, _ *seatMapRepositoryFake) { repository.getErr = errInjected },
		"owner": func(repository *presetRepositoryFake, _ *seatMapRepositoryFake) {
			repository.values["preset"] = presetResourceFixture(presetFixtureForTest("preset", "other", "theater", "auditorium", []string{"A1"}), 0)
		},
		"list": func(repository *presetRepositoryFake, _ *seatMapRepositoryFake) {
			repository.values["preset"] = applicationPreset(preset.GetUserId(), preset.GetAuditoriumId(), now)
			repository.listErr = errInjected
		},
	} {
		t.Run("update_"+name, func(t *testing.T) {
			repository := newPresetRepositoryFake()
			seatMaps := newSeatMapRepositoryFake()
			configure(repository, seatMaps)
			service := NewPresetService(repository, &sequenceIDs{}, fixedClock{now})
			if _, err := service.Update(ctx, updateRequest); err == nil {
				t.Fatal("Update() succeeded")
			}
		})
	}

	repository = newPresetRepositoryFake()
	repository.values["preset"] = applicationPreset("other", preset.GetAuditoriumId(), now)
	service = NewPresetService(repository, &sequenceIDs{}, fixedClock{now})
	if err := service.Delete(ctx, preset.GetUserId(), "preset"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("Delete() ownership error = %v", err)
	}
	repository.getErr = errInjected
	if err := service.Delete(ctx, preset.GetUserId(), "preset"); !errors.Is(err, errInjected) {
		t.Fatalf("Delete() repository error = %v", err)
	}
}

func TestMonitorServiceCoversDefaultsOwnershipAndRepositoryErrors(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	now := time.Date(2026, time.August, 9, 10, 0, 0, 0, time.UTC)
	presets := newPresetRepositoryFake()
	presets.values["preset"] = applicationPreset("user", "auditorium", now)
	monitors := &monitorRepositoryFake{}
	service := NewMonitorService(monitors, presets, &sequenceIDs{}, fixedClock{now})
	request := monitorMutationForTest(0, "", "", "user", "preset", openingMonitorModeForTest(), "movie_1", " Movie ", nil, []int{1}, 0, "", "", 0, 0)
	createdResource, err := service.Create(ctx, request)
	created := createdResource.GetMonitor()
	if err != nil || created == nil || created.GetMode().GetOpening() == nil ||
		created.GetPollInterval().AsDuration() != 3*time.Minute || created.GetMaximumPollInterval().AsDuration() != 8*time.Minute ||
		created.GetSearchHorizonDays() != defaultSearchHorizonDays {
		t.Fatalf("Create() = %+v, %v", createdResource, err)
	}
	monitors.revision = 1
	update := cloneMonitorMutationForTest(request)
	update.GetMutation().SetExpectedRevision(1)
	update.GetMonitor().SetId(created.GetId())
	update.GetMonitor().SetMovieTitle("Updated")
	updatedResource, err := service.Update(ctx, update)
	updatedMonitor := updatedResource.GetMonitor()
	if err != nil || updatedMonitor == nil || updatedMonitor.GetMovieTitle() != "Updated" || updatedMonitor.GetId() != created.GetId() {
		t.Fatalf("Update() = %+v, %v", updatedResource, err)
	}
	checkedAt := now.Add(-time.Minute)
	updatedMonitor.SetState(clientpb.MonitorState_builder{Booked: clientpb.MonitorBooked_builder{}.Build()}.Build())
	updatedMonitor.SetLastCheckedAt(domainTimestampForTest(checkedAt))
	updatedMonitor.SetReservationId("old-reservation")
	if err := putMonitor(ctx, monitors, updatedMonitor); err != nil {
		t.Fatal(err)
	}
	updatedResource, err = service.Update(ctx, update)
	updatedMonitor = updatedResource.GetMonitor()
	if err != nil || updatedMonitor == nil || updatedMonitor.GetState().GetPending() == nil || updatedMonitor.GetLastCheckedAt() != nil ||
		updatedMonitor.GetReservationId() != "" {
		t.Fatalf("Update() retained stale execution state: %+v, %v", updatedResource, err)
	}
	monitors.getErr = ErrNotFound
	missing := monitorMutationForTest(1, "", "missing", "user", "preset", openingMonitorModeForTest(), "movie_1", "Movie", []string{"2026-08-10"}, nil, 0, "", "", 5*time.Second, 8*time.Second)
	if _, err := service.Update(ctx, missing); !errors.Is(err, ErrNotFound) {
		t.Fatalf("Update(missing) error = %v", err)
	}
	monitors.getErr = nil
	setMonitorState(updatedMonitor, "running", "")
	if err := putMonitor(ctx, monitors, updatedMonitor); err != nil {
		t.Fatal(err)
	}
	if _, err := service.Update(ctx, update); !errors.Is(err, ErrConflict) {
		t.Fatalf("Update(running) error = %v", err)
	}
	setMonitorState(updatedMonitor, "triggered", "")
	if err := putMonitor(ctx, monitors, updatedMonitor); err != nil {
		t.Fatal(err)
	}
	if _, err := service.Update(ctx, update); !errors.Is(err, ErrConflict) {
		t.Fatalf("Update(awaiting payment) error = %v", err)
	}
	setMonitorState(updatedMonitor, "payment_unknown", "")
	if err := putMonitor(ctx, monitors, updatedMonitor); err != nil {
		t.Fatal(err)
	}
	if _, err := service.Update(ctx, update); !errors.Is(err, ErrConflict) {
		t.Fatalf("Update(unknown payment) error = %v", err)
	}
	setMonitorState(updatedMonitor, "pending", "")
	if err := putMonitor(ctx, monitors, updatedMonitor); err != nil {
		t.Fatal(err)
	}
	invalidUpdate := cloneMonitorMutationForTest(update)
	invalidUpdate.GetMonitor().SetMovieId("")
	if _, err := service.Update(ctx, invalidUpdate); err == nil {
		t.Fatal("Update accepted invalid monitor")
	}
	presets.getErr = errInjected
	if _, err := service.Update(ctx, update); !errors.Is(err, ErrNotFound) {
		t.Fatalf("Update preset error = %v", err)
	}
	presets.getErr = nil
	expiredUpdate := monitorMutationForTest(1, "", created.GetId(), "user", "preset", openingMonitorModeForTest(), "movie_1", "Movie", []string{"2026-08-08"}, nil, 0, "", "", 5*time.Second, 8*time.Second)
	if _, err := service.Update(ctx, expiredUpdate); !errors.Is(err, ErrMonitorExpired) {
		t.Fatalf("Update expired error = %v", err)
	}
	monitors.putErr = errInjected
	if _, err := service.Update(ctx, update); !errors.Is(err, errInjected) {
		t.Fatalf("Update put error = %v", err)
	}
	monitors.putErr = nil

	explicit := monitorMutationForTest(0, "", "", "user", "preset", cancellationMonitorModeForTest(), "movie_1", "Movie", []string{"2026-08-10"}, nil, 9, "", "", 7*time.Second, 11*time.Second)
	if _, err := service.Create(ctx, explicit); err != nil {
		t.Fatalf("Create(explicit) error = %v", err)
	}
	if _, err := service.List(ctx, "user"); err != nil {
		t.Fatalf("List() error = %v", err)
	}

	presets.getErr = errInjected
	if _, err := service.Create(ctx, request); !errors.Is(err, ErrNotFound) {
		t.Fatalf("Create() preset error = %v", err)
	}
	presets.getErr = nil
	invalid := cloneMonitorMutationForTest(request)
	invalid.GetMonitor().SetMovieId("")
	if _, err := service.Create(ctx, invalid); err == nil {
		t.Fatal("Create() accepted invalid monitor")
	}
	expired := monitorMutationForTest(0, "", "", "user", "preset", cancellationMonitorModeForTest(), "movie_1", "Movie", []string{"2026-08-08"}, nil, 9, "", "", 7*time.Second, 11*time.Second)
	if _, err := service.Create(ctx, expired); !errors.Is(err, ErrMonitorExpired) {
		t.Fatalf("Create() expiration error = %v", err)
	}
	monitors.putErr = errInjected
	if _, err := service.Create(ctx, explicit); !errors.Is(err, errInjected) {
		t.Fatalf("Create() put error = %v", err)
	}

	monitors.putErr = nil
	monitors.job = cloneMonitor(created)
	if err := service.Delete(ctx, "other", created.GetId()); !errors.Is(err, ErrNotFound) {
		t.Fatalf("Delete() ownership error = %v", err)
	}
	monitors.job.SetUserId("user")
	setMonitorState(monitors.job, "running", "")
	if err := service.Delete(ctx, "user", created.GetId()); !errors.Is(err, ErrConflict) {
		t.Fatalf("Delete() running error = %v", err)
	}
	setMonitorState(monitors.job, "stopped", "")
	if err := service.Delete(ctx, "user", created.GetId()); err != nil {
		t.Fatalf("Delete() error = %v", err)
	}
	monitors.getErr = errInjected
	if err := service.Delete(ctx, "user", created.GetId()); !errors.Is(err, errInjected) {
		t.Fatalf("Delete() get error = %v", err)
	}
}

func TestCancellationServiceCoversReviewCommitAndFailures(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	now := time.Date(2026, time.August, 9, 10, 0, 0, 0, time.UTC)
	repository := &reservationRepositoryFake{reservation: bookedReservationFixtureForTest("reservation", "user", "monitor")}
	booking := &bookingGatewayFake{draft: cancellationResultFixtureForTest("reservation", "booking", "10000")}
	service := NewCancellationService(repository, booking, fixedClock{now})

	draft, err := service.Cancel(ctx, cancellationRequest("user", false))
	if err != nil || draft.GetRefundAmount() != "10000" {
		t.Fatalf("Cancel(review) = %+v, %v", draft, err)
	}
	draft, err = service.Cancel(ctx, cancellationRequest("user", true))
	if err != nil || repository.reservation.GetCancelled() == nil || repository.reservation.GetCancelledAt() == nil {
		t.Fatalf("Cancel(commit) = %+v, %+v, %v", draft, repository.reservation, err)
	}
	if _, err := service.Cancel(ctx, cancellationRequest("other", false)); !errors.Is(err, ErrNotFound) {
		t.Fatalf("Cancel() ownership error = %v", err)
	}
	repository.getErr = errInjected
	if _, err := service.Cancel(ctx, cancellationRequest("user", false)); !errors.Is(err, ErrNotFound) {
		t.Fatalf("Cancel() get error = %v", err)
	}
	repository.getErr = nil
	booking.prepareCancellationErr = errInjected
	if _, err := service.Cancel(ctx, cancellationRequest("user", true)); !errors.Is(err, errInjected) {
		t.Fatalf("Cancel() prepare error = %v", err)
	}
	booking.prepareCancellationErr = nil
	booking.commitCancellationErr = errInjected
	if _, err := service.Cancel(ctx, cancellationRequest("user", true)); !errors.Is(err, errInjected) {
		t.Fatalf("Cancel() commit error = %v", err)
	}
	booking.commitCancellationErr = nil
	repository.putErr = errInjected
	if _, err := service.Cancel(ctx, cancellationRequest("user", true)); !errors.Is(err, errInjected) {
		t.Fatalf("Cancel() put error = %v", err)
	}
}

func TestMonitorServiceCreateIdempotentPaths(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	now := time.Date(2026, time.August, 9, 10, 0, 0, 0, time.UTC)
	request := monitorMutationForTest(0, "", "", "user", "preset", openingMonitorModeForTest(), "movie_1", "Movie", []string{"2026-08-10"}, nil, 0, "", "", 5*time.Second, 8*time.Second)
	presets := newPresetRepositoryFake()
	presets.values["preset"] = applicationPreset("user", "auditorium", now)

	monitors := &monitorRepositoryFake{getErr: ErrNotFound}
	service := NewMonitorService(monitors, presets, &sequenceIDs{}, fixedClock{now})
	if _, err := service.CreateIdempotent(ctx, request); err == nil {
		t.Fatal("CreateIdempotent accepted an empty key")
	}
	request.GetMutation().SetCommandId("command")
	created, err := service.CreateIdempotent(ctx, request)
	if err != nil || created.GetMonitor().GetId() != "command" {
		t.Fatalf("CreateIdempotent(create) = %+v, %v", created, err)
	}
	monitors.getErr = nil
	existing, err := service.CreateIdempotent(ctx, request)
	if err != nil || existing.GetMonitor().GetId() != "command" {
		t.Fatalf("CreateIdempotent(existing) = %+v, %v", existing, err)
	}
	monitors.job.SetUserId("other")
	if _, err := service.CreateIdempotent(ctx, request); !errors.Is(err, ErrNotFound) {
		t.Fatalf("CreateIdempotent(owner) = %v", err)
	}
	monitors.getErr = errInjected
	if _, err := service.CreateIdempotent(ctx, request); !errors.Is(err, errInjected) {
		t.Fatalf("CreateIdempotent(get) = %v", err)
	}
	monitors.getErr = ErrNotFound
	presets.getErr = errInjected
	if _, err := service.CreateIdempotent(ctx, request); !errors.Is(err, ErrNotFound) {
		t.Fatalf("CreateIdempotent(preset) = %v", err)
	}
	presets.getErr = nil
	invalid := cloneMonitorMutationForTest(request)
	invalid.GetMonitor().SetMovieTitle("")
	invalid.GetMonitor().SetMovieId("")
	if _, err := service.CreateIdempotent(ctx, invalid); err == nil {
		t.Fatal("CreateIdempotent accepted an invalid request")
	}
	expired := cloneMonitorMutationForTest(request)
	expired.GetMonitor().SetTargetDates(localDatesForTest([]string{"2026-08-08"}))
	if _, err := service.CreateIdempotent(ctx, expired); !errors.Is(err, ErrMonitorExpired) {
		t.Fatalf("CreateIdempotent(expired) = %v", err)
	}
	monitors.putErr = errInjected
	if _, err := service.CreateIdempotent(ctx, request); !errors.Is(err, errInjected) {
		t.Fatalf("CreateIdempotent(put) = %v", err)
	}
}

func TestCancellationOperationLedgerPaths(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	now := time.Date(2026, time.August, 9, 10, 0, 0, 0, time.UTC)
	newHarness := func() (*reservationSequenceRepository, *bookingGatewayFake, *operationRepositoryFake, *CancellationService) {
		reservations := &reservationSequenceRepository{reservation: bookedReservationFixtureForTest("reservation", "user", "monitor")}
		booking := &bookingGatewayFake{draft: cancellationResultFixtureForTest("reservation", "booking", "10000")}
		operations := &operationRepositoryFake{}
		return reservations, booking, operations, NewCancellationService(reservations, booking, fixedClock{now}, operations)
	}

	reservations, _, operations, service := newHarness()
	if _, err := service.Cancel(ctx, cancellationRequest("user", true)); err != nil ||
		reservations.reservation.GetCancelled() == nil || operations.last.GetReconciled() == nil {
		t.Fatalf("Cancel(ledger success) = %+v, %+v, %v", reservations.reservation, operations.last, err)
	}

	_, _, operations, service = newHarness()
	operations.failAt = 1
	if _, err := service.Cancel(ctx, cancellationRequest("user", true)); !errors.Is(err, errInjected) {
		t.Fatalf("Cancel(prepare ledger failure) = %v", err)
	}

	reservations, booking, operations, service := newHarness()
	booking.commitCancellationErr = errInjected
	if _, err := service.Cancel(ctx, cancellationRequest("user", true)); !errors.Is(err, errInjected) ||
		reservations.reservation.GetCancellationUnknown() == nil || operations.last.GetUnknown() == nil {
		t.Fatalf("Cancel(unknown) = %+v, %+v, %v", reservations.reservation, operations.last, err)
	}

	reservations, _, operations, service = newHarness()
	operations.failAt = 2
	if _, err := service.Cancel(ctx, cancellationRequest("user", true)); !errors.Is(err, errInjected) ||
		reservations.reservation.GetCancellationUnknown() == nil {
		t.Fatalf("Cancel(confirm ledger failure) = %+v, %v", reservations.reservation, err)
	}

	reservations, _, _, service = newHarness()
	reservations.failAt = 2
	if _, err := service.Cancel(ctx, cancellationRequest("user", true)); !errors.Is(err, errInjected) {
		t.Fatalf("Cancel(final reservation failure) = %v", err)
	}
}

type operationRepositoryFake struct {
	puts   int
	failAt int
	last   *clientpb.ExternalOperation
}

func (repository *operationRepositoryFake) PutExternalOperation(_ context.Context, resource *clientpb.Resource) error {
	repository.puts++
	if repository.puts == repository.failAt {
		return errInjected
	}
	message := resource.GetExternalOperation()
	if message == nil {
		return errors.New("external operation resource is required")
	}
	repository.last = cloneExternalOperation(message)
	return nil
}

type reservationSequenceRepository struct {
	reservation *clientpb.Reservation
	puts        int
	failAt      int
}

func (repository *reservationSequenceRepository) PutReservation(_ context.Context, resource *clientpb.Resource) error {
	repository.puts++
	if repository.puts == repository.failAt {
		return errInjected
	}
	value, _, err := reservationMessage(resource)
	if err != nil {
		return err
	}
	repository.reservation = cloneReservation(value)
	return nil
}

func (repository *reservationSequenceRepository) GetReservation(context.Context, string) (*clientpb.Resource, error) {
	return resourceForReservation(cloneReservation(repository.reservation), 0), nil
}

func (repository *reservationSequenceRepository) ListReservationsByUser(context.Context, string) ([]*clientpb.Resource, error) {
	return []*clientpb.Resource{resourceForReservation(cloneReservation(repository.reservation), 0)}, nil
}

func assertPresetCreateError(
	t *testing.T,
	request *clientpb.WebUIResourceMutation,
	configure func(*presetRepositoryFake, *seatMapRepositoryFake),
) {
	t.Helper()
	repository := newPresetRepositoryFake()
	seatMaps := newSeatMapRepositoryFake()
	if configure != nil {
		configure(repository, seatMaps)
	}
	service := NewPresetService(repository, &sequenceIDs{}, fixedClock{time.Now()})
	if _, err := service.Create(context.Background(), request); err == nil {
		t.Fatal("Create() succeeded")
	}
}

func validPresetRequest() *clientpb.WebUIResourceMutation {
	return presetMutationForTest(0, "", "user", " center ", "theater", "auditorium", 1, clientpb.SeatPreference_builder{
		ExplicitSeats: []string{"A1"}, PreferredTypes: []string{string(domain.SeatTypeStandard)}, Together: boolPointer(true),
	}.Build())
}

func applicationPreset(userID, auditoriumID string, now time.Time) *clientpb.Resource {
	preset := presetFixtureForTest("preset", userID, "theater", auditoriumID, []string{"A1"})
	preset.SetCreatedAt(domainTimestampForTest(now))
	preset.SetUpdatedAt(domainTimestampForTest(now))
	return presetResourceFixture(preset, 0)
}

func validApplicationSeatMap(auditoriumID string, now time.Time) domain.SeatMap {
	return domain.SeatMap{
		AuditoriumID: auditoriumID, Version: "version", ObservedAt: now,
		Seats: []domain.Seat{{
			ID: "seat", AuditoriumID: auditoriumID, Label: "A1", Row: "A", Number: 1,
			X: 0.5, Y: 0.5, Type: domain.SeatTypeStandard,
		}},
		Evidence: domain.LayoutEvidence{
			ScreenshotPath: "layout.png", ScreenshotSHA256: "screen", SnapshotSHA256: "snapshot",
			SourceShowtimeID: "showtime", DOMSeatCount: 1, SnapshotSeatCount: 1,
			CaptureTrigger: "refresh", CapturedAt: now,
		},
	}
}

type presetRepositoryFake struct {
	values    map[string]*clientpb.Resource
	listErr   error
	getErr    error
	putErr    error
	deleteErr error
}

func newPresetRepositoryFake() *presetRepositoryFake {
	return &presetRepositoryFake{values: make(map[string]*clientpb.Resource)}
}

func (repository *presetRepositoryFake) PutPreset(_ context.Context, resource *clientpb.Resource) error {
	if repository.putErr != nil {
		return repository.putErr
	}
	value, _, err := presetMessage(resource)
	if err != nil {
		return err
	}
	repository.values[value.GetId()] = presetResourceFixture(clonePreset(value), resourceRevision(resource))
	return nil
}

func (repository *presetRepositoryFake) GetPreset(_ context.Context, id string) (*clientpb.Resource, error) {
	if repository.getErr != nil {
		return nil, repository.getErr
	}
	value, found := repository.values[id]
	if !found {
		return nil, ErrNotFound
	}
	return cloneResourceFixture(value), nil
}

func (repository *presetRepositoryFake) ListPresetsByUser(_ context.Context, userID string) ([]*clientpb.Resource, error) {
	if repository.listErr != nil {
		return nil, repository.listErr
	}
	values := make([]*clientpb.Resource, 0, len(repository.values))
	for _, value := range repository.values {
		if value.GetPreset().GetUserId() == userID {
			values = append(values, cloneResourceFixture(value))
		}
	}
	return values, nil
}

func (repository *presetRepositoryFake) DeletePreset(_ context.Context, id string) error {
	if repository.deleteErr != nil {
		return repository.deleteErr
	}
	delete(repository.values, id)
	return nil
}

type seatMapRepositoryFake struct {
	values map[string]*seatmappb.Snapshot
	getErr error
	putErr error
}

func newSeatMapRepositoryFake() *seatMapRepositoryFake {
	return &seatMapRepositoryFake{values: make(map[string]*seatmappb.Snapshot)}
}

func (repository *seatMapRepositoryFake) PutSeatMap(_ context.Context, value *seatmappb.Snapshot) error {
	if repository.putErr != nil {
		return repository.putErr
	}
	repository.values[value.GetAuditoriumId()] = value
	return nil
}

func (repository *seatMapRepositoryFake) GetSeatMap(_ context.Context, id string) (*seatmappb.Snapshot, error) {
	if repository.getErr != nil {
		return nil, repository.getErr
	}
	value, found := repository.values[id]
	if !found {
		return nil, ErrNotFound
	}
	return value, nil
}

type monitorRepositoryFake struct {
	job        *clientpb.Monitor
	revision   int64
	getErr     error
	putErr     error
	listErr    error
	deleteErr  error
	renewErr   error
	releaseErr error
}

func (repository *monitorRepositoryFake) PutMonitor(_ context.Context, resource *clientpb.Resource) error {
	if repository.putErr != nil {
		return repository.putErr
	}
	value, revision, err := monitorMessage(resource)
	if err != nil {
		return err
	}
	repository.job = cloneMonitor(value)
	repository.revision = revision
	return nil
}

func (repository *monitorRepositoryFake) GetMonitor(context.Context, string) (*clientpb.Resource, error) {
	if repository.getErr != nil {
		return nil, repository.getErr
	}
	return resourceForMonitor(cloneMonitor(repository.job), repository.revision), nil
}

func (repository *monitorRepositoryFake) ListMonitorsByUser(context.Context, string) ([]*clientpb.Resource, error) {
	if repository.listErr != nil {
		return nil, repository.listErr
	}
	return []*clientpb.Resource{resourceForMonitor(cloneMonitor(repository.job), repository.revision)}, nil
}

func (repository *monitorRepositoryFake) DeleteMonitor(context.Context, string) error {
	return repository.deleteErr
}

func (repository *monitorRepositoryFake) AcquireMonitor(
	context.Context, string, string, time.Time, time.Duration,
) (*clientpb.Resource, error) {
	if repository.getErr != nil {
		return nil, repository.getErr
	}
	return resourceForMonitor(cloneMonitor(repository.job), repository.revision), nil
}

func (repository *monitorRepositoryFake) RenewMonitor(
	context.Context, string, string, time.Time, time.Duration,
) error {
	return repository.renewErr
}

func (repository *monitorRepositoryFake) ReleaseMonitor(context.Context, string, string) error {
	return repository.releaseErr
}

type reservationRepositoryFake struct {
	reservation *clientpb.Reservation
	getErr      error
	putErr      error
}

func (repository *reservationRepositoryFake) PutReservation(_ context.Context, resource *clientpb.Resource) error {
	if repository.putErr != nil {
		return repository.putErr
	}
	value, _, err := reservationMessage(resource)
	if err != nil {
		return err
	}
	repository.reservation = cloneReservation(value)
	return nil
}

func (repository *reservationRepositoryFake) GetReservation(context.Context, string) (*clientpb.Resource, error) {
	if repository.getErr != nil {
		return nil, repository.getErr
	}
	return resourceForReservation(cloneReservation(repository.reservation), 0), nil
}

func (repository *reservationRepositoryFake) ListReservationsByUser(
	context.Context,
	string,
) ([]*clientpb.Resource, error) {
	return []*clientpb.Resource{resourceForReservation(cloneReservation(repository.reservation), 0)}, nil
}

type bookingGatewayFake struct {
	draft                  *clientpb.WebUICancellationResult
	prepareCancellationErr error
	commitCancellationErr  error
}

func (*bookingGatewayFake) OpenSeatSelection(
	context.Context,
	*catalogpb.Showtime,
	int,
) (*seatmappb.Snapshot, []*seatmappb.Seat, error) {
	return nil, nil, nil
}

func (*bookingGatewayFake) PreparePayment(
	context.Context,
	*catalogpb.Showtime,
	[]string,
) (*clientpb.Reservation, error) {
	return nil, nil
}

func (gateway *bookingGatewayFake) PrepareCancellation(
	_ context.Context,
	reservation *clientpb.Reservation,
) (*clientpb.WebUICancellationResult, error) {
	if gateway.prepareCancellationErr != nil {
		return nil, gateway.prepareCancellationErr
	}
	reservationID, bookingNumber, refundAmount := gateway.draft.GetReservationId(), gateway.draft.GetBookingNumber(), gateway.draft.GetRefundAmount()
	if reservationID == "" && reservation != nil {
		reservationID = reservation.GetId()
	}
	return clientpb.WebUICancellationResult_builder{ReservationId: &reservationID, BookingNumber: &bookingNumber, RefundAmount: &refundAmount}.Build(), nil
}

func (gateway *bookingGatewayFake) CommitCancellation(context.Context) error {
	return gateway.commitCancellationErr
}
