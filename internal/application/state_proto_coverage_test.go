package application

import (
	"errors"
	"reflect"
	"testing"
	"time"

	clientpb "github.com/cineko-org/contracts/gen/go/cineko/client"
	commonpb "github.com/cineko-org/contracts/gen/go/cineko/common"
	seatmappb "github.com/cineko-org/contracts/gen/go/cineko/seatmap"
	"google.golang.org/protobuf/types/known/durationpb"
	"google.golang.org/protobuf/types/known/timestamppb"
)

func TestStateProtoPrimitiveResourceAndCloneHelpers(t *testing.T) {
	t.Parallel()

	if durationValue(nil) != 0 || localTimeValue(nil) != "" || monitorCommandID(nil) != "" || monitorCommandID(&clientpb.WebUIResourceMutation{}) != "" {
		t.Fatal("nil Proto helper returned a non-zero value")
	}
	if durationValue(durationpb.New(time.Second)) != time.Second {
		t.Fatal("duration Proto helper lost the duration")
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
		func(value *clientpb.Preset) { value.SetSeatCount(0) },
		func(value *clientpb.Preset) { value.SetSeatCount(9) },
		func(value *clientpb.Preset) { value.SetSeatPreference(nil) },
		func(value *clientpb.Preset) {
			value.SetSeatPreference(clientpb.SeatPreference_builder{Together: boolPointer(false)}.Build())
		},
		func(value *clientpb.Preset) {
			value.SetSeatCount(2)
			value.SetSeatPreference(clientpb.SeatPreference_builder{Together: boolPointer(true), ExplicitSeats: []string{"A1"}}.Build())
		},
		func(value *clientpb.Preset) {
			value.SetSeatPreference(clientpb.SeatPreference_builder{Together: boolPointer(true), ExplicitSeats: []string{" "}}.Build())
		},
		func(value *clientpb.Preset) {
			value.SetSeatPreference(clientpb.SeatPreference_builder{Together: boolPointer(true), ExplicitSeats: []string{"A1", "A1"}}.Build())
		},
		func(value *clientpb.Preset) {
			value.SetSeatPreference(clientpb.SeatPreference_builder{Together: boolPointer(true), PreferredZones: []*clientpb.SeatZone{nil}}.Build())
		},
		func(value *clientpb.Preset) {
			value.SetSeatPreference(clientpb.SeatPreference_builder{Together: boolPointer(true), PreferredZones: []*clientpb.SeatZone{stateSeatZoneForTest(" ", 0, 1, 0, 1)}}.Build())
		},
		func(value *clientpb.Preset) {
			value.SetSeatPreference(clientpb.SeatPreference_builder{Together: boolPointer(true), PreferredZones: []*clientpb.SeatZone{stateSeatZoneForTest("bad", -1, 1, 0, 1)}}.Build())
		},
		func(value *clientpb.Preset) {
			value.SetSeatPreference(clientpb.SeatPreference_builder{Together: boolPointer(true), PreferredZones: []*clientpb.SeatZone{stateSeatZoneForTest("bad", 0, 2, 0, 1)}}.Build())
		},
		func(value *clientpb.Preset) {
			value.SetSeatPreference(clientpb.SeatPreference_builder{Together: boolPointer(true), PreferredZones: []*clientpb.SeatZone{stateSeatZoneForTest("bad", 0, 1, -1, 1)}}.Build())
		},
		func(value *clientpb.Preset) {
			value.SetSeatPreference(clientpb.SeatPreference_builder{Together: boolPointer(true), PreferredZones: []*clientpb.SeatZone{stateSeatZoneForTest("bad", 0, 1, 0, 2)}}.Build())
		},
		func(value *clientpb.Preset) {
			value.SetSeatPreference(clientpb.SeatPreference_builder{Together: boolPointer(true), PreferredZones: []*clientpb.SeatZone{stateSeatZoneForTest("bad", 1, 0, 0, 1)}}.Build())
		},
		func(value *clientpb.Preset) {
			value.SetSeatPreference(clientpb.SeatPreference_builder{Together: boolPointer(true), PreferredZones: []*clientpb.SeatZone{stateSeatZoneForTest("bad", 0, 1, 1, 0)}}.Build())
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
		func(value *clientpb.Monitor) { value.SetTargetDates(nil) },
		func(value *clientpb.Monitor) { value.SetMode(nil) },
		func(value *clientpb.Monitor) { value.SetMode(&clientpb.MonitorMode{}) },
		func(value *clientpb.Monitor) {
			value.SetMode(clientpb.MonitorMode_builder{Cancellation: clientpb.CancellationMonitor_builder{}.Build()}.Build())
			value.SetTargetWeekdays([]int32{1})
		},
		func(value *clientpb.Monitor) { value.SetPollInterval(durationpb.New(time.Second)) },
		func(value *clientpb.Monitor) { value.SetMaximumPollInterval(durationpb.New(2 * time.Second)) },
		func(value *clientpb.Monitor) { value.SetTargetDates([]*commonpb.LocalDate{nil}) },
		func(value *clientpb.Monitor) {
			value.SetTargetDates([]*commonpb.LocalDate{stateDateForTest(2, 30)})
		},
		func(value *clientpb.Monitor) {
			value.SetTargetDates([]*commonpb.LocalDate{stateDateForTest(8, 10), stateDateForTest(8, 10)})
		},
		func(value *clientpb.Monitor) {
			value.SetTargetDates(nil)
			value.SetTargetWeekdays([]int32{-1})
		},
		func(value *clientpb.Monitor) {
			value.SetTargetDates(nil)
			value.SetTargetWeekdays([]int32{7})
		},
		func(value *clientpb.Monitor) {
			value.SetTargetDates(nil)
			value.SetTargetWeekdays([]int32{1, 1})
		},
		func(value *clientpb.Monitor) {
			value.SetTargetDates(nil)
			value.SetTargetWeekdays([]int32{1})
			value.SetSearchHorizonDays(0)
		},
		func(value *clientpb.Monitor) {
			value.SetTargetDates(nil)
			value.SetTargetWeekdays([]int32{1})
			value.SetSearchHorizonDays(366)
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

	if monitorModeIsCancellation(nil) || monitorModeIsCancellation(&clientpb.Monitor{}) {
		t.Fatal("empty monitor reported cancellation mode")
	}
	cancellation := validStateMonitorForTest()
	cancellation.SetMode(clientpb.MonitorMode_builder{Cancellation: clientpb.CancellationMonitor_builder{}.Build()}.Build())
	if !monitorModeIsCancellation(cancellation) {
		t.Fatal("cancellation monitor mode was not detected")
	}
	if monitorPollInterval(nil) != 3*time.Minute || monitorPollInterval(&clientpb.Monitor{}) != 3*time.Minute {
		t.Fatal("missing poll interval did not use the default")
	}
	if monitorPollIntervalMax(nil) != 8*time.Minute {
		t.Fatal("nil monitor maximum poll interval did not use the default")
	}
	withoutMaximum := validStateMonitorForTest()
	withoutMaximum.SetMaximumPollInterval(nil)
	if monitorPollIntervalMax(withoutMaximum) != 12*time.Second/5 {
		t.Fatalf("derived maximum poll interval = %v", monitorPollIntervalMax(withoutMaximum))
	}

	defaults := clientpb.Monitor_builder{TargetWeekdays: []int32{1}}.Build()
	applyMonitorDefaults(defaults)
	if defaults.GetMode().GetOpening() == nil || defaults.GetPollInterval().AsDuration() != 3*time.Minute ||
		defaults.GetMaximumPollInterval().AsDuration() != 8*time.Minute || defaults.GetSearchHorizonDays() != defaultSearchHorizonDays ||
		defaults.GetState().GetPending() == nil {
		t.Fatalf("monitor defaults = %+v", defaults)
	}
	applyMonitorDefaults(defaults)
}

func TestStateProtoMonitorLifecycleDatesAndRankingAdapters(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, time.August, 10, 12, 0, 0, 0, time.UTC)
	if monitorIsExpired(nil, now) || monitorIsExpired(clientpb.Monitor_builder{TargetWeekdays: []int32{1}}.Build(), now) || monitorIsExpired(&clientpb.Monitor{}, now) {
		t.Fatal("non-expiring monitor reported expired")
	}
	withNilAndFuture := clientpb.Monitor_builder{TargetDates: []*commonpb.LocalDate{nil, stateDateForTest(8, 11)}}.Build()
	if monitorIsExpired(withNilAndFuture, now) {
		t.Fatal("future monitor reported expired")
	}
	expired := clientpb.Monitor_builder{TargetDates: []*commonpb.LocalDate{stateDateForTest(8, 8)}}.Build()
	if !monitorIsExpired(expired, now) {
		t.Fatal("past monitor did not expire")
	}

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
	monitorRecordCheck(nil, now, nil)
	monitorRecordCheck(transitioned, now, nil)
	if monitorStateName(transitioned) != "running" {
		t.Fatal("successful check did not recover failed monitor")
	}
	monitorRecordCheck(transitioned, now, errors.New("boom"))
	if monitorStateName(transitioned) != "failed" || transitioned.GetState().GetFailed().GetReason() != "boom" {
		t.Fatal("failed check did not retain its cause")
	}

	if monitorResolveTargetDates(nil, now) != nil {
		t.Fatal("nil monitor resolved target dates")
	}
	resolved := monitorResolveTargetDates(clientpb.Monitor_builder{
		TargetDates:       []*commonpb.LocalDate{nil, stateDateForTest(8, 8), stateDateForTest(8, 11)},
		TargetWeekdays:    []int32{int32(time.Monday)},
		SearchHorizonDays: int32PointerForStateTest(8),
	}.Build(), now)
	if !reflect.DeepEqual(resolved, []string{"2026-08-10", "2026-08-11", "2026-08-17"}) {
		t.Fatalf("resolved target dates = %v", resolved)
	}
	if !reflect.DeepEqual(int32Values([]int32{1, 2}), []int{1, 2}) || len(int32Values(nil)) != 0 {
		t.Fatal("int32 slice conversion failed")
	}

	if got := seatPreferenceForRanking(nil); len(got.CandidateSeats) != 0 {
		t.Fatal("nil Proto preference produced ranking rules")
	}
	preference := seatPreferenceForRanking(clientpb.SeatPreference_builder{
		ExplicitSeats: []string{"A1"}, PreferredRows: []string{"A"}, PreferredTypes: []string{"standard"},
		PreferredZones: []*clientpb.SeatZone{nil, stateSeatZoneForTest("center", 0, 1, 0, 1)},
		Together:       boolPointer(true), AvoidEdges: boolPointer(true),
	}.Build())
	if len(preference.PreferredZones) != 1 || !preference.AvoidEdges || preference.Adjacency == "" {
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
	seatCount, together := int32(1), true
	return clientpb.Preset_builder{
		Id: &id, UserId: &userID, Name: &name, TheaterId: &theaterID, AuditoriumId: &auditoriumID, SeatCount: &seatCount,
		SeatPreference: clientpb.SeatPreference_builder{Together: &together, ExplicitSeats: []string{"A1"}, PreferredZones: []*clientpb.SeatZone{stateSeatZoneForTest("center", 0, 1, 0, 1)}}.Build(),
	}.Build()
}

func validStateMonitorForTest() *clientpb.Monitor {
	id, userID, presetID, movieID := "monitor", "user", "preset", "movie"
	return clientpb.Monitor_builder{
		Id: &id, UserId: &userID, PresetId: &presetID, MovieId: &movieID,
		Mode:         clientpb.MonitorMode_builder{Opening: clientpb.OpeningMonitor_builder{}.Build()}.Build(),
		TargetDates:  []*commonpb.LocalDate{stateDateForTest(8, 10)},
		PollInterval: durationpb.New(2 * time.Second), MaximumPollInterval: durationpb.New(3 * time.Second),
	}.Build()
}

func stateSeatZoneForTest(name string, minX, maxX, minY, maxY float64) *clientpb.SeatZone {
	weight := int32(2)
	return clientpb.SeatZone_builder{Name: &name, MinX: &minX, MaxX: &maxX, MinY: &minY, MaxY: &maxY, Weight: &weight}.Build()
}

func stateDateForTest(month, day int32) *commonpb.LocalDate {
	year := int32(2026)
	return commonpb.LocalDate_builder{Year: &year, Month: &month, Day: &day}.Build()
}

func stateTimeForTest(hour, minute int32) *commonpb.LocalTime {
	return commonpb.LocalTime_builder{Hour: &hour, Minute: &minute}.Build()
}

func int32PointerForStateTest(value int32) *int32 { return &value }

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
