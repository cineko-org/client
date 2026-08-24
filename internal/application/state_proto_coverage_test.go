package application

import (
	"reflect"
	"testing"
	"time"

	"github.com/cineko-org/client/internal/domain"
	clientpb "github.com/cineko-org/contracts/v3/gen/go/cineko/client"
	commonpb "github.com/cineko-org/contracts/v3/gen/go/cineko/common"
	seatmappb "github.com/cineko-org/contracts/v3/gen/go/cineko/seatmap"
	"google.golang.org/protobuf/types/known/timestamppb"
)

func TestStateProtoPrimitiveResourceAndCloneHelpers(t *testing.T) {
	t.Parallel()

	if localTimeValue(nil) != "" || monitorCommandID(nil) != "" || monitorCommandID(&clientpb.WebUIResourceMutation{}) != "" {
		t.Fatal("nil Proto helper returned a non-zero value")
	}

	preset := validStatePresetForTest()
	monitor := validStateMonitorForTest()
	reservationID := "reservation"
	reservation := clientpb.Reservation_builder{Id: &reservationID}.Build()
	operationID := "operation"
	operation := clientpb.ExternalOperation_builder{Id: &operationID}.Build()
	revision := int64(3)
	mutationIdentity := commonpb.MutationIdentity_builder{CommandId: stringPointer(" command "), ExpectedRevision: &revision}.Build()
	presetMutation := clientpb.WebUIResourceMutation_builder{Mutation: mutationIdentity, Preset: preset}.Build()
	monitorMutation := clientpb.WebUIResourceMutation_builder{Mutation: mutationIdentity, Monitor: monitor}.Build()
	if monitorCommandID(monitorMutation) != "command" {
		t.Fatal("monitor command id was not normalized")
	}
	if got, gotRevision, err := presetMutationMessage(presetMutation); err != nil || got != preset || gotRevision != revision {
		t.Fatalf("presetMutationMessage() = %v, %d, %v", got, gotRevision, err)
	}
	if _, _, err := presetMutationMessage(nil); err == nil {
		t.Fatal("nil preset mutation accepted")
	}
	if _, _, err := monitorMutationMessage(&clientpb.WebUIResourceMutation{}); err == nil {
		t.Fatal("incomplete monitor mutation accepted")
	}

	resources := []*clientpb.Resource{
		resourceForPreset(preset, revision),
		resourceForMonitor(monitor, revision),
		resourceForReservation(reservation, revision),
		resourceForExternalOperation(operation),
	}
	if got, gotRevision, err := presetMessage(resources[0]); err != nil || got != preset || gotRevision != revision {
		t.Fatalf("presetMessage() = %v, %d, %v", got, gotRevision, err)
	}
	if got, gotRevision, err := monitorMessage(resources[1]); err != nil || got != monitor || gotRevision != revision {
		t.Fatalf("monitorMessage() = %v, %d, %v", got, gotRevision, err)
	}
	if got, gotRevision, err := reservationMessage(resources[2]); err != nil || got != reservation || gotRevision != revision {
		t.Fatalf("reservationMessage() = %v, %d, %v", got, gotRevision, err)
	}
	for name, read := range map[string]func() error{
		"preset":      func() error { _, _, err := presetMessage(nil); return err },
		"monitor":     func() error { _, _, err := monitorMessage(&clientpb.Resource{}); return err },
		"reservation": func() error { _, _, err := reservationMessage(&clientpb.Resource{}); return err },
	} {
		if err := read(); err == nil {
			t.Fatalf("%s resource accepted incomplete Proto", name)
		}
	}
	if resources[3].GetIdentity().GetRevision() != 0 || resources[3].GetExternalOperation() != operation {
		t.Fatal("external operation resource was not built directly from Proto")
	}

	if clonePreset(nil) != nil || cloneMonitor(nil) != nil || cloneReservation(nil) != nil || cloneExternalOperation(nil) != nil {
		t.Fatal("nil clone helper returned a message")
	}
	if clonePreset(preset) == preset || cloneMonitor(monitor) == monitor || cloneReservation(reservation) == reservation || cloneExternalOperation(operation) == operation {
		t.Fatal("Proto clone helper returned the source pointer")
	}
}

func TestStateProtoPresetValidationCoversEveryInvariant(t *testing.T) {
	t.Parallel()

	if err := validatePresetMessage(validStatePresetForTest()); err != nil {
		t.Fatalf("valid preset rejected: %v", err)
	}
	if validatePresetMessage(nil) == nil {
		t.Fatal("nil preset accepted")
	}

	invalid := []func(*clientpb.Preset){
		func(value *clientpb.Preset) { value.SetId("") },
		func(value *clientpb.Preset) { value.SetUserId("") },
		func(value *clientpb.Preset) { value.SetName(" ") },
		func(value *clientpb.Preset) { value.SetTheaterId("") },
		func(value *clientpb.Preset) { value.SetAuditoriumId("") },
		func(value *clientpb.Preset) {
			value.SetSeatPreference(clientpb.SeatPreference_builder{ExplicitSeats: []string{" "}}.Build())
		},
		func(value *clientpb.Preset) {
			value.SetSeatPreference(clientpb.SeatPreference_builder{ExplicitSeats: []string{"A1", "A1"}}.Build())
		},
	}
	for index, mutate := range invalid {
		value := validStatePresetForTest()
		mutate(value)
		if err := validatePresetMessage(value); err == nil {
			t.Fatalf("invalid preset case %d accepted", index)
		}
	}
}

func TestStateProtoMonitorValidationAndDefaults(t *testing.T) {
	t.Parallel()

	if err := validateMonitorMessage(validStateMonitorForTest()); err != nil {
		t.Fatalf("valid monitor rejected: %v", err)
	}
	if validateMonitorMessage(nil) == nil {
		t.Fatal("nil monitor accepted")
	}

	invalid := []func(*clientpb.Monitor){
		func(value *clientpb.Monitor) { value.SetId("") },
		func(value *clientpb.Monitor) { value.SetUserId("") },
		func(value *clientpb.Monitor) { value.SetPresetId("") },
		func(value *clientpb.Monitor) { value.SetMovieId("") },
		func(value *clientpb.Monitor) { value.SetSeatCount(0) },
		func(value *clientpb.Monitor) { value.SetSeatCount(9) },
		func(value *clientpb.Monitor) { value.SetSeatType("") },
		func(value *clientpb.Monitor) { value.SetSeatType("balcony") },
		func(value *clientpb.Monitor) { value.SetTargetWeekdays(nil) },
		func(value *clientpb.Monitor) {
			value.SetTargetWeekdays([]int32{-1})
		},
		func(value *clientpb.Monitor) {
			value.SetTargetWeekdays([]int32{7})
		},
		func(value *clientpb.Monitor) {
			value.SetTargetWeekdays([]int32{1, 1})
		},
		func(value *clientpb.Monitor) { value.SetEarliestTime(stateTimeForTest(24, 0)) },
		func(value *clientpb.Monitor) { value.SetEarliestTime(stateTimeForTest(-1, 0)) },
		func(value *clientpb.Monitor) { value.SetLatestTime(stateTimeForTest(0, 60)) },
		func(value *clientpb.Monitor) { value.SetLatestTime(stateTimeForTest(0, -1)) },
	}
	for index, mutate := range invalid {
		value := validStateMonitorForTest()
		mutate(value)
		if err := validateMonitorMessage(value); err == nil {
			t.Fatalf("invalid monitor case %d accepted", index)
		}
	}
	if validateLocalTime(nil) != nil {
		t.Fatal("nil local time rejected")
	}
	if err := validateLocalTime(stateTimeForTest(12, 30)); err != nil {
		t.Fatalf("valid local time rejected: %v", err)
	}
	defaults := clientpb.Monitor_builder{TargetWeekdays: []int32{1}}.Build()
	applyMonitorDefaults(defaults)
	if defaults.GetSeatCount() != 1 || defaults.GetSeatType() != "standard" ||
		defaults.GetState().GetPending() == nil {
		t.Fatalf("monitor defaults = %+v", defaults)
	}
	applyMonitorDefaults(defaults)
}

func TestStateProtoMonitorLifecycleAndRankingAdapters(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, time.August, 10, 12, 0, 0, 0, time.UTC)
	setMonitorState(nil, "running", "")
	states := []string{"running", "triggered", "booked", "payment_unknown", "failed", "stopped", "pending"}
	for _, state := range states {
		monitor := &clientpb.Monitor{}
		setMonitorState(monitor, state, "reason")
		want := state
		if state == "pending" {
			want = "pending"
		}
		if got := monitorStateName(monitor); got != want {
			t.Fatalf("monitorStateName(%q) = %q", state, got)
		}
		if state == "failed" && monitor.GetState().GetFailed().GetReason() != "reason" {
			t.Fatal("failed state lost its reason")
		}
	}
	if monitorStateName(nil) != "pending" || monitorStateName(&clientpb.Monitor{}) != "pending" {
		t.Fatal("empty monitor state was not pending")
	}
	transitioned := &clientpb.Monitor{}
	monitorTransition(transitioned, "booked", now)
	if monitorStateName(transitioned) != "booked" || !transitioned.GetUpdatedAt().AsTime().Equal(now) {
		t.Fatal("monitor transition did not update state and time")
	}
	setMonitorFailure(transitioned, "failed")
	if monitorStateName(transitioned) != "failed" {
		t.Fatal("monitor failure was not recorded")
	}
	monitorRecordCheck(nil, now)
	monitorRecordCheck(transitioned, now)
	if monitorStateName(transitioned) != "failed" || !transitioned.GetLastCheckedAt().AsTime().Equal(now) {
		t.Fatal("recorded check changed terminal state or timestamp")
	}

	if !reflect.DeepEqual(int32Values([]int32{1, 2}), []int{1, 2}) || len(int32Values(nil)) != 0 {
		t.Fatal("int32 slice conversion failed")
	}

	if got := seatPreferenceForRanking(nil, "standard"); len(got.CandidateSeats) != 0 || len(got.PreferredTypes) != 1 || got.Adjacency != domain.SeatAdjacencyRequired {
		t.Fatal("nil Proto preference produced ranking rules")
	}
	preference := seatPreferenceForRanking(clientpb.SeatPreference_builder{
		ExplicitSeats: []string{"A1"}, PreferredRows: []string{"A"}, PreferredTypes: []string{"standard"},
		PreferredZones: []*clientpb.SeatZone{nil, stateSeatZoneForTest("center", 0, 1, 0, 1)},
		Together:       boolPointer(true), AvoidEdges: boolPointer(true),
	}.Build(), "recliner")
	if len(preference.CandidateSeats) != 1 || len(preference.PreferredRows) != 0 || len(preference.PreferredZones) != 0 || preference.AvoidEdges ||
		preference.Adjacency != domain.SeatAdjacencyRequired || !reflect.DeepEqual(preference.PreferredTypes, []domain.SeatType{domain.SeatTypeRecliner}) {
		t.Fatalf("ranking preference = %+v", preference)
	}

	if got := seatSelectionForRanking(nil, nil); len(got.SeatMap.Seats) != 0 {
		t.Fatal("nil snapshot produced seats")
	}
	if got := seatSelectionForRanking(&seatmappb.Snapshot{}, nil); len(got.SeatMap.Seats) != 0 {
		t.Fatal("snapshot without layout produced seats")
	}
	selection := seatSelectionForRanking(fullStateSeatSnapshotForTest(now), []*seatmappb.Seat{nil, fullStateSeatForTest()})
	if len(selection.SeatMap.Seats) != 1 || len(selection.SeatMap.Zones) != 1 || len(selection.SeatMap.Blocks) != 1 ||
		len(selection.LiveSeats) != 1 || selection.LiveSeats[0].Source != "booking-adapter" {
		t.Fatalf("seat selection conversion = %+v", selection)
	}
}

func validStatePresetForTest() *clientpb.Preset {
	id, userID, name, theaterID, auditoriumID := "preset", "user", "center", "theater", "auditorium"
	together := true
	return clientpb.Preset_builder{
		Id: &id, UserId: &userID, Name: &name, TheaterId: &theaterID, AuditoriumId: &auditoriumID,
		SeatPreference: clientpb.SeatPreference_builder{Together: &together, ExplicitSeats: []string{"A1"}, PreferredZones: []*clientpb.SeatZone{stateSeatZoneForTest("center", 0, 1, 0, 1)}}.Build(),
	}.Build()
}

func validStateMonitorForTest() *clientpb.Monitor {
	id, userID, presetID, movieID := "monitor", "user", "preset", "movie"
	seatCount, seatType := int32(1), "standard"
	return clientpb.Monitor_builder{
		Id: &id, UserId: &userID, PresetId: &presetID, MovieId: &movieID,
		SeatCount: &seatCount, SeatType: &seatType,
		TargetWeekdays: []int32{int32(time.Monday)},
	}.Build()
}

func stateSeatZoneForTest(name string, minX, maxX, minY, maxY float64) *clientpb.SeatZone {
	weight := int32(2)
	return clientpb.SeatZone_builder{Name: &name, MinX: &minX, MaxX: &maxX, MinY: &minY, MaxY: &maxY, Weight: &weight}.Build()
}

func stateTimeForTest(hour, minute int32) *commonpb.LocalTime {
	return commonpb.LocalTime_builder{Hour: &hour, Minute: &minute}.Build()
}

func fullStateSeatForTest() *seatmappb.Seat {
	id, auditoriumID, label, row, seatType := "seat", "auditorium", "A1", "A", "standard"
	number, x, y := int32(1), 0.5, 0.5
	zoneName, zoneKind, saleFormCode, saleFormName := "center", "prime", "normal", "일반"
	leftAisle, rightAisle := true, false
	sourceLabel, sourceKindCode, sourceKindName := "source", "kind", "일반"
	return seatmappb.Seat_builder{
		Id: &id, AuditoriumId: &auditoriumID, Label: &label, Row: &row, Number: &number, X: &x, Y: &y, Type: &seatType,
		ZoneName: &zoneName, ZoneKind: &zoneKind, SaleFormCode: &saleFormCode, SaleFormName: &saleFormName,
		LeftAisle: &leftAisle, RightAisle: &rightAisle, Features: []string{"feature"}, SourceLabel: &sourceLabel,
		SourceSeatKindCode: &sourceKindCode, SourceSeatKindName: &sourceKindName, SourceClasses: []string{"class"},
	}.Build()
}

func fullStateSeatSnapshotForTest(observedAt time.Time) *seatmappb.Snapshot {
	auditoriumID, layoutHash := "auditorium", "hash"
	code, name, kindCode, kindName := "center", "중앙", "prime", "프라임"
	minX, maxX, minY, maxY, capacity := 0.0, 1.0, 0.0, 1.0, int32(1)
	zone := seatmappb.LayoutZone_builder{Code: &code, Name: &name, KindCode: &kindCode, KindName: &kindName, MinX: &minX, MaxX: &maxX, MinY: &minY, MaxY: &maxY, Capacity: &capacity}.Build()
	block := seatmappb.LayoutBlock_builder{Code: &code, Name: &name, KindCode: &kindCode, KindName: &kindName, MinX: &minX, MaxX: &maxX, MinY: &minY, MaxY: &maxY}.Build()
	return seatmappb.Snapshot_builder{
		AuditoriumId: &auditoriumID, LayoutHash: &layoutHash, ObservedAt: timestamppb.New(observedAt),
		Layout: seatmappb.Layout_builder{Seats: []*seatmappb.Seat{nil, fullStateSeatForTest()}, Zones: []*seatmappb.LayoutZone{nil, zone}, Blocks: []*seatmappb.LayoutBlock{nil, block}}.Build(),
	}.Build()
}
