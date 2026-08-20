package application

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/cineko-org/client/internal/domain"
)

var errInjected = errors.New("injected failure")

func TestPresetServiceCoversSuccessAndFailurePaths(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	now := time.Date(2026, time.August, 9, 10, 0, 0, 0, time.UTC)
	request := validPresetRequest()

	repository := newPresetRepositoryFake()
	service := NewPresetService(repository, &sequenceIDs{}, fixedClock{now})
	created, err := service.Create(ctx, request)
	if err != nil || created.Name != "center" {
		t.Fatalf("Create() = %+v, %v", created, err)
	}
	storedPreset := repository.values[created.ID]
	storedPreset.Revision = 1
	repository.values[created.ID] = storedPreset
	request.ExpectedRevision = 1
	request.Name = "updated"
	updated, err := service.Update(ctx, request.UserID, created.ID, request)
	if err != nil || updated.Name != "updated" {
		t.Fatalf("Update() = %+v, %v", updated, err)
	}
	listed, err := service.List(ctx, request.UserID)
	if err != nil || len(listed) != 1 {
		t.Fatalf("List() = %+v, %v", listed, err)
	}
	if err := service.Delete(ctx, request.UserID, created.ID); err != nil {
		t.Fatalf("Delete() error = %v", err)
	}

	assertPresetCreateError(t, request, func(repository *presetRepositoryFake, _ *seatMapRepositoryFake) {
		repository.listErr = errInjected
	})
	assertPresetCreateError(t, request, func(repository *presetRepositoryFake, _ *seatMapRepositoryFake) {
		repository.values["duplicate"] = domain.Preset{ID: "duplicate", UserID: request.UserID, Name: request.Name}
	})
	assertPresetCreateError(t, CreatePresetRequest{}, nil)
	assertPresetCreateError(t, request, func(repository *presetRepositoryFake, _ *seatMapRepositoryFake) {
		repository.putErr = errInjected
	})
	for name, configure := range map[string]func(*presetRepositoryFake, *seatMapRepositoryFake){
		"get": func(repository *presetRepositoryFake, _ *seatMapRepositoryFake) { repository.getErr = errInjected },
		"owner": func(repository *presetRepositoryFake, _ *seatMapRepositoryFake) {
			repository.values["preset"] = domain.Preset{ID: "preset", UserID: "other"}
		},
		"list": func(repository *presetRepositoryFake, _ *seatMapRepositoryFake) {
			repository.values["preset"] = applicationPreset(request.UserID, request.AuditoriumID, now)
			repository.listErr = errInjected
		},
	} {
		t.Run("update_"+name, func(t *testing.T) {
			repository := newPresetRepositoryFake()
			seatMaps := newSeatMapRepositoryFake()
			configure(repository, seatMaps)
			service := NewPresetService(repository, &sequenceIDs{}, fixedClock{now})
			if _, err := service.Update(ctx, request.UserID, "preset", request); err == nil {
				t.Fatal("Update() succeeded")
			}
		})
	}

	repository = newPresetRepositoryFake()
	repository.values["preset"] = applicationPreset("other", request.AuditoriumID, now)
	service = NewPresetService(repository, &sequenceIDs{}, fixedClock{now})
	if err := service.Delete(ctx, request.UserID, "preset"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("Delete() ownership error = %v", err)
	}
	repository.getErr = errInjected
	if err := service.Delete(ctx, request.UserID, "preset"); !errors.Is(err, errInjected) {
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
	request := CreateMonitorRequest{
		UserID: "user", PresetID: "preset", MovieID: "movie_1", Movie: " Movie ", TargetWeekdays: []int{1},
	}
	created, err := service.Create(ctx, request)
	if err != nil || created.Mode != domain.MonitorModeOpening ||
		created.PollInterval != 3*time.Minute || created.PollIntervalMax != 8*time.Minute ||
		created.SearchHorizonDays != domain.DefaultSearchHorizonDays {
		t.Fatalf("Create() = %+v, %v", created, err)
	}
	monitors.job.Revision = 1
	update := request
	update.ExpectedRevision = 1
	update.Movie = "Updated"
	updatedMonitor, err := service.Update(ctx, UpdateMonitorRequest{ID: created.ID, CreateMonitorRequest: update})
	if err != nil || updatedMonitor.Movie != "Updated" || updatedMonitor.ID != created.ID {
		t.Fatalf("Update() = %+v, %v", updatedMonitor, err)
	}
	checkedAt := now.Add(-time.Minute)
	updatedMonitor.Status = domain.MonitorBooked
	updatedMonitor.LastCheckedAt = &checkedAt
	updatedMonitor.LastError = "old failure"
	updatedMonitor.ReservationID = "old-reservation"
	if err := monitors.PutMonitor(ctx, updatedMonitor); err != nil {
		t.Fatal(err)
	}
	updatedMonitor, err = service.Update(ctx, UpdateMonitorRequest{ID: created.ID, CreateMonitorRequest: update})
	if err != nil || updatedMonitor.Status != domain.MonitorPending || updatedMonitor.LastCheckedAt != nil ||
		updatedMonitor.LastError != "" || updatedMonitor.ReservationID != "" {
		t.Fatalf("Update() retained stale execution state: %+v, %v", updatedMonitor, err)
	}
	monitors.getErr = ErrNotFound
	if _, err := service.Update(ctx, UpdateMonitorRequest{ID: "missing", CreateMonitorRequest: CreateMonitorRequest{UserID: "user"}}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("Update(missing) error = %v", err)
	}
	monitors.getErr = nil
	updatedMonitor.Status = domain.MonitorRunning
	if err := monitors.PutMonitor(ctx, updatedMonitor); err != nil {
		t.Fatal(err)
	}
	if _, err := service.Update(ctx, UpdateMonitorRequest{ID: created.ID, CreateMonitorRequest: update}); !errors.Is(err, ErrConflict) {
		t.Fatalf("Update(running) error = %v", err)
	}
	updatedMonitor.Status = domain.MonitorTriggered
	if err := monitors.PutMonitor(ctx, updatedMonitor); err != nil {
		t.Fatal(err)
	}
	if _, err := service.Update(ctx, UpdateMonitorRequest{ID: created.ID, CreateMonitorRequest: update}); !errors.Is(err, ErrConflict) {
		t.Fatalf("Update(awaiting payment) error = %v", err)
	}
	updatedMonitor.Status = domain.MonitorPaymentUnknown
	if err := monitors.PutMonitor(ctx, updatedMonitor); err != nil {
		t.Fatal(err)
	}
	if _, err := service.Update(ctx, UpdateMonitorRequest{ID: created.ID, CreateMonitorRequest: update}); !errors.Is(err, ErrConflict) {
		t.Fatalf("Update(unknown payment) error = %v", err)
	}
	updatedMonitor.Status = domain.MonitorPending
	if err := monitors.PutMonitor(ctx, updatedMonitor); err != nil {
		t.Fatal(err)
	}
	invalidUpdate := update
	invalidUpdate.MovieID = ""
	if _, err := service.Update(ctx, UpdateMonitorRequest{ID: created.ID, CreateMonitorRequest: invalidUpdate}); err == nil {
		t.Fatal("Update accepted invalid monitor")
	}
	presets.getErr = errInjected
	if _, err := service.Update(ctx, UpdateMonitorRequest{ID: created.ID, CreateMonitorRequest: update}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("Update preset error = %v", err)
	}
	presets.getErr = nil
	expiredUpdate := update
	expiredUpdate.TargetWeekdays = nil
	expiredUpdate.TargetDates = []string{"2026-08-08"}
	if _, err := service.Update(ctx, UpdateMonitorRequest{ID: created.ID, CreateMonitorRequest: expiredUpdate}); !errors.Is(err, ErrMonitorExpired) {
		t.Fatalf("Update expired error = %v", err)
	}
	monitors.putErr = errInjected
	if _, err := service.Update(ctx, UpdateMonitorRequest{ID: created.ID, CreateMonitorRequest: update}); !errors.Is(err, errInjected) {
		t.Fatalf("Update put error = %v", err)
	}
	monitors.putErr = nil

	explicit := request
	explicit.Mode = domain.MonitorModeCancellation
	explicit.TargetWeekdays = nil
	explicit.TargetDates = []string{"2026-08-10"}
	explicit.PollInterval = 7 * time.Second
	explicit.PollIntervalMax = 11 * time.Second
	explicit.SearchHorizonDays = 9
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
	invalid := request
	invalid.MovieID = ""
	if _, err := service.Create(ctx, invalid); err == nil {
		t.Fatal("Create() accepted invalid monitor")
	}
	expired := explicit
	expired.TargetDates = []string{"2026-08-08"}
	if _, err := service.Create(ctx, expired); !errors.Is(err, ErrMonitorExpired) {
		t.Fatalf("Create() expiration error = %v", err)
	}
	monitors.putErr = errInjected
	if _, err := service.Create(ctx, explicit); !errors.Is(err, errInjected) {
		t.Fatalf("Create() put error = %v", err)
	}

	monitors.putErr = nil
	monitors.job = created
	if err := service.Delete(ctx, "other", created.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("Delete() ownership error = %v", err)
	}
	monitors.job.UserID = "user"
	monitors.job.Status = domain.MonitorRunning
	if err := service.Delete(ctx, "user", created.ID); !errors.Is(err, ErrConflict) {
		t.Fatalf("Delete() running error = %v", err)
	}
	monitors.job.Status = domain.MonitorStopped
	if err := service.Delete(ctx, "user", created.ID); err != nil {
		t.Fatalf("Delete() error = %v", err)
	}
	monitors.getErr = errInjected
	if err := service.Delete(ctx, "user", created.ID); !errors.Is(err, errInjected) {
		t.Fatalf("Delete() get error = %v", err)
	}
}

func TestCancellationServiceCoversReviewCommitAndFailures(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	now := time.Date(2026, time.August, 9, 10, 0, 0, 0, time.UTC)
	repository := &reservationRepositoryFake{reservation: domain.Reservation{ID: "reservation", UserID: "user"}}
	booking := &bookingGatewayFake{draft: domain.CancellationDraft{ReservationID: "reservation", RefundAmount: "10000"}}
	service := NewCancellationService(repository, booking, fixedClock{now})

	draft, err := service.Cancel(ctx, "user", "reservation", false)
	if err != nil || draft.RefundAmount != "10000" {
		t.Fatalf("Cancel(review) = %+v, %v", draft, err)
	}
	draft, err = service.Cancel(ctx, "user", "reservation", true)
	if err != nil || repository.reservation.Status != "cancelled" || repository.reservation.CancelledAt == nil {
		t.Fatalf("Cancel(commit) = %+v, %+v, %v", draft, repository.reservation, err)
	}
	if _, err := service.Cancel(ctx, "other", "reservation", false); !errors.Is(err, ErrNotFound) {
		t.Fatalf("Cancel() ownership error = %v", err)
	}
	repository.getErr = errInjected
	if _, err := service.Cancel(ctx, "user", "reservation", false); !errors.Is(err, ErrNotFound) {
		t.Fatalf("Cancel() get error = %v", err)
	}
	repository.getErr = nil
	booking.prepareCancellationErr = errInjected
	if _, err := service.Cancel(ctx, "user", "reservation", true); !errors.Is(err, errInjected) {
		t.Fatalf("Cancel() prepare error = %v", err)
	}
	booking.prepareCancellationErr = nil
	booking.commitCancellationErr = errInjected
	if _, err := service.Cancel(ctx, "user", "reservation", true); !errors.Is(err, errInjected) {
		t.Fatalf("Cancel() commit error = %v", err)
	}
	booking.commitCancellationErr = nil
	repository.putErr = errInjected
	if _, err := service.Cancel(ctx, "user", "reservation", true); !errors.Is(err, errInjected) {
		t.Fatalf("Cancel() put error = %v", err)
	}
}

func TestMonitorServiceCreateIdempotentPaths(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	now := time.Date(2026, time.August, 9, 10, 0, 0, 0, time.UTC)
	request := CreateMonitorRequest{
		UserID: "user", PresetID: "preset", MovieID: "movie_1", Movie: "Movie", TargetDates: []string{"2026-08-10"},
		PollInterval: 5 * time.Second, PollIntervalMax: 8 * time.Second,
	}
	presets := newPresetRepositoryFake()
	presets.values["preset"] = applicationPreset("user", "auditorium", now)

	monitors := &monitorRepositoryFake{getErr: ErrNotFound}
	service := NewMonitorService(monitors, presets, &sequenceIDs{}, fixedClock{now})
	if _, err := service.CreateIdempotent(ctx, "", request); err == nil {
		t.Fatal("CreateIdempotent accepted an empty key")
	}
	created, err := service.CreateIdempotent(ctx, "command", request)
	if err != nil || created.ID != "command" {
		t.Fatalf("CreateIdempotent(create) = %+v, %v", created, err)
	}
	monitors.getErr = nil
	existing, err := service.CreateIdempotent(ctx, "command", request)
	if err != nil || existing.ID != "command" {
		t.Fatalf("CreateIdempotent(existing) = %+v, %v", existing, err)
	}
	monitors.job.UserID = "other"
	if _, err := service.CreateIdempotent(ctx, "command", request); !errors.Is(err, ErrNotFound) {
		t.Fatalf("CreateIdempotent(owner) = %v", err)
	}
	monitors.getErr = errInjected
	if _, err := service.CreateIdempotent(ctx, "command", request); !errors.Is(err, errInjected) {
		t.Fatalf("CreateIdempotent(get) = %v", err)
	}
	monitors.getErr = ErrNotFound
	presets.getErr = errInjected
	if _, err := service.CreateIdempotent(ctx, "command", request); !errors.Is(err, ErrNotFound) {
		t.Fatalf("CreateIdempotent(preset) = %v", err)
	}
	presets.getErr = nil
	invalid := request
	invalid.Movie = ""
	invalid.MovieID = ""
	if _, err := service.CreateIdempotent(ctx, "command", invalid); err == nil {
		t.Fatal("CreateIdempotent accepted an invalid request")
	}
	expired := request
	expired.TargetDates = []string{"2026-08-08"}
	if _, err := service.CreateIdempotent(ctx, "command", expired); !errors.Is(err, ErrMonitorExpired) {
		t.Fatalf("CreateIdempotent(expired) = %v", err)
	}
	monitors.putErr = errInjected
	if _, err := service.CreateIdempotent(ctx, "command", request); !errors.Is(err, errInjected) {
		t.Fatalf("CreateIdempotent(put) = %v", err)
	}
}

func TestCancellationOperationLedgerPaths(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	now := time.Date(2026, time.August, 9, 10, 0, 0, 0, time.UTC)
	newHarness := func() (*reservationSequenceRepository, *bookingGatewayFake, *operationRepositoryFake, *CancellationService) {
		reservations := &reservationSequenceRepository{reservation: domain.Reservation{ID: "reservation", UserID: "user"}}
		booking := &bookingGatewayFake{draft: domain.CancellationDraft{ReservationID: "reservation", RefundAmount: "10000"}}
		operations := &operationRepositoryFake{}
		return reservations, booking, operations, NewCancellationService(reservations, booking, fixedClock{now}, operations)
	}

	reservations, _, operations, service := newHarness()
	if _, err := service.Cancel(ctx, "user", "reservation", true); err != nil ||
		reservations.reservation.Status != "cancelled" || operations.last.Status != domain.ExternalOperationReconciled {
		t.Fatalf("Cancel(ledger success) = %+v, %+v, %v", reservations.reservation, operations.last, err)
	}

	_, _, operations, service = newHarness()
	operations.failAt = 1
	if _, err := service.Cancel(ctx, "user", "reservation", true); !errors.Is(err, errInjected) {
		t.Fatalf("Cancel(prepare ledger failure) = %v", err)
	}

	reservations, booking, operations, service := newHarness()
	booking.commitCancellationErr = errInjected
	if _, err := service.Cancel(ctx, "user", "reservation", true); !errors.Is(err, errInjected) ||
		reservations.reservation.Status != "cancellation_unknown" || operations.last.Status != domain.ExternalOperationUnknown {
		t.Fatalf("Cancel(unknown) = %+v, %+v, %v", reservations.reservation, operations.last, err)
	}

	reservations, _, operations, service = newHarness()
	operations.failAt = 2
	if _, err := service.Cancel(ctx, "user", "reservation", true); !errors.Is(err, errInjected) ||
		reservations.reservation.Status != "cancellation_unknown" {
		t.Fatalf("Cancel(confirm ledger failure) = %+v, %v", reservations.reservation, err)
	}

	reservations, _, _, service = newHarness()
	reservations.failAt = 2
	if _, err := service.Cancel(ctx, "user", "reservation", true); !errors.Is(err, errInjected) {
		t.Fatalf("Cancel(final reservation failure) = %v", err)
	}
}

type operationRepositoryFake struct {
	puts   int
	failAt int
	last   domain.ExternalOperation
}

func (repository *operationRepositoryFake) PutExternalOperation(_ context.Context, value domain.ExternalOperation) error {
	repository.puts++
	if repository.puts == repository.failAt {
		return errInjected
	}
	repository.last = value
	return nil
}

type reservationSequenceRepository struct {
	reservation domain.Reservation
	puts        int
	failAt      int
}

func (repository *reservationSequenceRepository) PutReservation(_ context.Context, value domain.Reservation) error {
	repository.puts++
	if repository.puts == repository.failAt {
		return errInjected
	}
	repository.reservation = value
	return nil
}

func (repository *reservationSequenceRepository) GetReservation(context.Context, string) (domain.Reservation, error) {
	return repository.reservation, nil
}

func (repository *reservationSequenceRepository) ListReservationsByUser(context.Context, string) ([]domain.Reservation, error) {
	return []domain.Reservation{repository.reservation}, nil
}

func assertPresetCreateError(
	t *testing.T,
	request CreatePresetRequest,
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

func validPresetRequest() CreatePresetRequest {
	return CreatePresetRequest{
		UserID: "user", Name: " center ", TheaterID: "theater", AuditoriumID: "auditorium",
		SeatCount: 1, SeatPreference: domain.SeatPreference{CandidateSeats: []string{"A1"}, PreferredTypes: []domain.SeatType{domain.SeatTypeStandard}, Adjacency: domain.SeatAdjacencyRequired},
	}
}

func applicationPreset(userID, auditoriumID string, now time.Time) domain.Preset {
	return domain.Preset{
		ID: "preset", UserID: userID, Name: "center", TheaterID: "theater", AuditoriumID: auditoriumID,
		SeatCount: 1, SeatPreference: domain.SeatPreference{CandidateSeats: []string{"A1"}, Adjacency: domain.SeatAdjacencyRequired},
		CreatedAt: now, UpdatedAt: now,
	}
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
	values    map[string]domain.Preset
	listErr   error
	getErr    error
	putErr    error
	deleteErr error
}

func newPresetRepositoryFake() *presetRepositoryFake {
	return &presetRepositoryFake{values: make(map[string]domain.Preset)}
}

func (repository *presetRepositoryFake) PutPreset(_ context.Context, value domain.Preset) error {
	if repository.putErr != nil {
		return repository.putErr
	}
	repository.values[value.ID] = value
	return nil
}

func (repository *presetRepositoryFake) GetPreset(_ context.Context, id string) (domain.Preset, error) {
	if repository.getErr != nil {
		return domain.Preset{}, repository.getErr
	}
	value, found := repository.values[id]
	if !found {
		return domain.Preset{}, ErrNotFound
	}
	return value, nil
}

func (repository *presetRepositoryFake) ListPresetsByUser(_ context.Context, userID string) ([]domain.Preset, error) {
	if repository.listErr != nil {
		return nil, repository.listErr
	}
	values := make([]domain.Preset, 0, len(repository.values))
	for _, value := range repository.values {
		if value.UserID == userID {
			values = append(values, value)
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
	values map[string]domain.SeatMap
	getErr error
	putErr error
}

func newSeatMapRepositoryFake() *seatMapRepositoryFake {
	return &seatMapRepositoryFake{values: make(map[string]domain.SeatMap)}
}

func (repository *seatMapRepositoryFake) PutSeatMap(_ context.Context, value domain.SeatMap) error {
	if repository.putErr != nil {
		return repository.putErr
	}
	repository.values[value.AuditoriumID] = value
	return nil
}

func (repository *seatMapRepositoryFake) GetSeatMap(_ context.Context, id string) (domain.SeatMap, error) {
	if repository.getErr != nil {
		return domain.SeatMap{}, repository.getErr
	}
	value, found := repository.values[id]
	if !found {
		return domain.SeatMap{}, ErrNotFound
	}
	return value, nil
}

type monitorRepositoryFake struct {
	job        domain.MonitorJob
	getErr     error
	putErr     error
	listErr    error
	deleteErr  error
	renewErr   error
	releaseErr error
}

func (repository *monitorRepositoryFake) PutMonitor(_ context.Context, value domain.MonitorJob) error {
	if repository.putErr != nil {
		return repository.putErr
	}
	repository.job = value
	return nil
}

func (repository *monitorRepositoryFake) GetMonitor(context.Context, string) (domain.MonitorJob, error) {
	if repository.getErr != nil {
		return domain.MonitorJob{}, repository.getErr
	}
	return repository.job, nil
}

func (repository *monitorRepositoryFake) ListMonitorsByUser(context.Context, string) ([]domain.MonitorJob, error) {
	if repository.listErr != nil {
		return nil, repository.listErr
	}
	return []domain.MonitorJob{repository.job}, nil
}

func (repository *monitorRepositoryFake) DeleteMonitor(context.Context, string) error {
	return repository.deleteErr
}

func (repository *monitorRepositoryFake) AcquireMonitor(
	context.Context, string, string, time.Time, time.Duration,
) (domain.MonitorJob, error) {
	if repository.getErr != nil {
		return domain.MonitorJob{}, repository.getErr
	}
	return repository.job, nil
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
	reservation domain.Reservation
	getErr      error
	putErr      error
}

func (repository *reservationRepositoryFake) PutReservation(_ context.Context, value domain.Reservation) error {
	if repository.putErr != nil {
		return repository.putErr
	}
	repository.reservation = value
	return nil
}

func (repository *reservationRepositoryFake) GetReservation(context.Context, string) (domain.Reservation, error) {
	return repository.reservation, repository.getErr
}

func (repository *reservationRepositoryFake) ListReservationsByUser(
	context.Context,
	string,
) ([]domain.Reservation, error) {
	return []domain.Reservation{repository.reservation}, nil
}

type bookingGatewayFake struct {
	draft                  domain.CancellationDraft
	prepareCancellationErr error
	commitCancellationErr  error
}

func (*bookingGatewayFake) OpenSeatSelection(
	context.Context,
	domain.Showtime,
	int,
) (domain.SeatSelection, error) {
	return domain.SeatSelection{}, nil
}

func (*bookingGatewayFake) PreparePayment(
	context.Context,
	domain.Showtime,
	[]string,
) (domain.BookingDraft, error) {
	return domain.BookingDraft{}, nil
}

func (gateway *bookingGatewayFake) PrepareCancellation(
	context.Context,
	domain.Reservation,
) (domain.CancellationDraft, error) {
	return gateway.draft, gateway.prepareCancellationErr
}

func (gateway *bookingGatewayFake) CommitCancellation(context.Context) error {
	return gateway.commitCancellationErr
}
