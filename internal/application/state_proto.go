package application

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/cineko-org/client/internal/domain"
	clientpb "github.com/cineko-org/contracts/v3/gen/go/cineko/client"
	commonpb "github.com/cineko-org/contracts/v3/gen/go/cineko/common"
	seatmappb "github.com/cineko-org/contracts/v3/gen/go/cineko/seatmap"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"
)

func resourceIdentity(id string, revision int64) *commonpb.ResourceIdentity {
	return commonpb.ResourceIdentity_builder{Id: &id, Revision: &revision}.Build()
}

func localTimeValue(value *commonpb.LocalTime) string {
	if value == nil {
		return ""
	}
	return fmt.Sprintf("%02d:%02d", value.GetHour(), value.GetMinute())
}

func monitorCommandID(message *clientpb.WebUIResourceMutation) string {
	if message == nil || message.GetMutation() == nil {
		return ""
	}
	return strings.TrimSpace(message.GetMutation().GetCommandId())
}

// presetMutationMessage returns the generated request message directly. The
// application boundary does not create a second request type for presets.
func presetMutationMessage(message *clientpb.WebUIResourceMutation) (*clientpb.Preset, int64, error) {
	if message == nil || message.GetPreset() == nil || message.GetMutation() == nil {
		return nil, 0, errors.New("preset mutation is required")
	}
	return message.GetPreset(), message.GetMutation().GetExpectedRevision(), nil
}

// monitorMutationMessage returns the generated request message directly. The
// monitor workflow keeps the Proto as its persisted source of truth.
func monitorMutationMessage(message *clientpb.WebUIResourceMutation) (*clientpb.Monitor, int64, error) {
	if message == nil || message.GetMonitor() == nil || message.GetMutation() == nil {
		return nil, 0, errors.New("monitor mutation is required")
	}
	return message.GetMonitor(), message.GetMutation().GetExpectedRevision(), nil
}

func presetMessage(resource *clientpb.Resource) (*clientpb.Preset, int64, error) {
	if resource == nil || resource.GetIdentity() == nil || resource.GetPreset() == nil {
		return nil, 0, errors.New("preset resource is required")
	}
	return resource.GetPreset(), resource.GetIdentity().GetRevision(), nil
}

func monitorMessage(resource *clientpb.Resource) (*clientpb.Monitor, int64, error) {
	if resource == nil || resource.GetIdentity() == nil || resource.GetMonitor() == nil {
		return nil, 0, errors.New("monitor resource is required")
	}
	return resource.GetMonitor(), resource.GetIdentity().GetRevision(), nil
}

func reservationMessage(resource *clientpb.Resource) (*clientpb.Reservation, int64, error) {
	if resource == nil || resource.GetIdentity() == nil || resource.GetReservation() == nil {
		return nil, 0, errors.New("reservation resource is required")
	}
	return resource.GetReservation(), resource.GetIdentity().GetRevision(), nil
}

func resourceForPreset(message *clientpb.Preset, revision int64) *clientpb.Resource {
	return clientpb.Resource_builder{Identity: resourceIdentity(message.GetId(), revision), Preset: message}.Build()
}

func resourceForMonitor(message *clientpb.Monitor, revision int64) *clientpb.Resource {
	return clientpb.Resource_builder{Identity: resourceIdentity(message.GetId(), revision), Monitor: message}.Build()
}

func resourceForReservation(message *clientpb.Reservation, revision int64) *clientpb.Resource {
	return clientpb.Resource_builder{Identity: resourceIdentity(message.GetId(), revision), Reservation: message}.Build()
}

func resourceForExternalOperation(message *clientpb.ExternalOperation, revisions ...int64) *clientpb.Resource {
	revision := int64(0)
	if len(revisions) > 0 {
		revision = revisions[0]
	}
	return clientpb.Resource_builder{Identity: resourceIdentity(message.GetId(), revision), ExternalOperation: message}.Build()
}

func clonePreset(value *clientpb.Preset) *clientpb.Preset {
	if value == nil {
		return nil
	}
	return proto.CloneOf(value)
}

func cloneMonitor(value *clientpb.Monitor) *clientpb.Monitor {
	if value == nil {
		return nil
	}
	return proto.CloneOf(value)
}

func cloneReservation(value *clientpb.Reservation) *clientpb.Reservation {
	if value == nil {
		return nil
	}
	return proto.CloneOf(value)
}

func cloneExternalOperation(value *clientpb.ExternalOperation) *clientpb.ExternalOperation {
	if value == nil {
		return nil
	}
	return proto.CloneOf(value)
}

func validatePresetMessage(value *clientpb.Preset) error {
	if value == nil || strings.TrimSpace(value.GetId()) == "" || strings.TrimSpace(value.GetUserId()) == "" {
		return errors.New("preset id and user id are required")
	}
	if strings.TrimSpace(value.GetName()) == "" {
		return errors.New("preset name is required")
	}
	if strings.TrimSpace(value.GetTheaterId()) == "" || strings.TrimSpace(value.GetAuditoriumId()) == "" {
		return errors.New("preset theater and auditorium are required")
	}
	preference := value.GetSeatPreference()
	if preference == nil {
		return nil
	}
	return validateCandidateSeats(preference.GetExplicitSeats())
}

func validateCandidateSeats(labels []string) error {
	seen := make(map[string]struct{}, len(labels))
	for _, label := range labels {
		if strings.TrimSpace(label) == "" {
			return errors.New("candidate seat label is required")
		}
		if _, exists := seen[label]; exists {
			return fmt.Errorf("duplicate candidate seat %s", label)
		}
		seen[label] = struct{}{}
	}
	return nil
}

func validateMonitorMessage(value *clientpb.Monitor) error {
	if err := validateMonitorIdentity(value); err != nil {
		return err
	}
	if value.GetSeatCount() < 1 || value.GetSeatCount() > 8 {
		return errors.New("monitor seat count must be between 1 and 8")
	}
	if !domain.SeatType(value.GetSeatType()).Valid() {
		return fmt.Errorf("unknown monitor seat type %q", value.GetSeatType())
	}
	if err := validateTargetWeekdays(value.GetTargetWeekdays()); err != nil {
		return err
	}
	if err := validateLocalTime(value.GetEarliestTime()); err != nil {
		return err
	}
	return validateLocalTime(value.GetLatestTime())
}

func validateMonitorIdentity(value *clientpb.Monitor) error {
	if value == nil || strings.TrimSpace(value.GetId()) == "" || strings.TrimSpace(value.GetUserId()) == "" || strings.TrimSpace(value.GetPresetId()) == "" {
		return errors.New("monitor id, user id, and preset id are required")
	}
	if strings.TrimSpace(value.GetMovieId()) == "" || len(value.GetTargetWeekdays()) == 0 {
		return errors.New("monitor movie id and at least one target weekday are required")
	}
	return nil
}

func validateTargetWeekdays(weekdays []int32) error {
	seen := make(map[int32]struct{}, len(weekdays))
	for _, weekday := range weekdays {
		if weekday < int32(time.Sunday) || weekday > int32(time.Saturday) {
			return fmt.Errorf("invalid target weekday %d", weekday)
		}
		if _, exists := seen[weekday]; exists {
			return fmt.Errorf("duplicate target weekday %d", weekday)
		}
		seen[weekday] = struct{}{}
	}
	return nil
}

func validateLocalTime(value *commonpb.LocalTime) error {
	if value == nil {
		return nil
	}
	if value.GetHour() < 0 || value.GetHour() > 23 || value.GetMinute() < 0 || value.GetMinute() > 59 {
		return fmt.Errorf("invalid local time %02d:%02d", value.GetHour(), value.GetMinute())
	}
	return nil
}

func applyMonitorDefaults(value *clientpb.Monitor) {
	if value.GetSeatCount() == 0 {
		value.SetSeatCount(1)
	}
	if strings.TrimSpace(value.GetSeatType()) == "" {
		value.SetSeatType(string(domain.SeatTypeStandard))
	}
	if value.GetState() == nil {
		value.SetState(clientpb.MonitorState_builder{Pending: clientpb.MonitorPending_builder{}.Build()}.Build())
	}
}

func setMonitorState(value *clientpb.Monitor, state string, reason string) {
	if value == nil {
		return
	}
	stateValue := clientpb.MonitorState_builder{}
	switch state {
	case "running":
		stateValue.Running = clientpb.MonitorRunning_builder{}.Build()
	case "triggered":
		stateValue.Triggered = clientpb.MonitorTriggered_builder{}.Build()
	case "booked":
		stateValue.Booked = clientpb.MonitorBooked_builder{}.Build()
	case "payment_unknown":
		stateValue.PaymentUnknown = clientpb.MonitorPaymentUnknown_builder{}.Build()
	case "failed":
		stateValue.Failed = clientpb.MonitorFailed_builder{Reason: &reason}.Build()
	case "stopped":
		stateValue.Stopped = clientpb.MonitorStopped_builder{Reason: &reason}.Build()
	default:
		stateValue.Pending = clientpb.MonitorPending_builder{}.Build()
	}
	value.SetState(stateValue.Build())
}

func monitorStateName(value *clientpb.Monitor) string {
	if value == nil || value.GetState() == nil {
		return "pending"
	}
	switch {
	case value.GetState().GetRunning() != nil:
		return "running"
	case value.GetState().GetTriggered() != nil:
		return "triggered"
	case value.GetState().GetBooked() != nil:
		return "booked"
	case value.GetState().GetPaymentUnknown() != nil:
		return "payment_unknown"
	case value.GetState().GetFailed() != nil:
		return "failed"
	case value.GetState().GetStopped() != nil:
		return "stopped"
	default:
		return "pending"
	}
}

func monitorTransition(value *clientpb.Monitor, state string, now time.Time) {
	setMonitorState(value, state, "")
	value.SetUpdatedAt(timestamppb.New(now))
}

func setMonitorFailure(value *clientpb.Monitor, reason string) {
	setMonitorState(value, "failed", reason)
}

func monitorRecordCheck(value *clientpb.Monitor, now time.Time) {
	if value == nil {
		return
	}
	value.SetLastCheckedAt(timestamppb.New(now))
	value.SetUpdatedAt(timestamppb.New(now))
}

func int32Values(values []int32) []int {
	result := make([]int, len(values))
	for index, value := range values {
		result[index] = int(value)
	}
	return result
}

func seatPreferenceForRanking(value *clientpb.SeatPreference, seatType string) domain.SeatPreference {
	preference := domain.SeatPreference{
		Adjacency: domain.SeatAdjacencyRequired,
	}
	if value != nil {
		preference.CandidateSeats = append([]string(nil), value.GetExplicitSeats()...)
	}
	if selectedType := domain.SeatType(seatType); selectedType.Valid() {
		preference.PreferredTypes = []domain.SeatType{selectedType}
	}
	return preference
}

// seatSelectionForRanking creates the short-lived algorithm input from the
// generated seat-map messages returned by the booking adapter. The Proto
// snapshot and available seats remain the only port-level representation.
func seatSelectionForRanking(snapshot *seatmappb.Snapshot, available []*seatmappb.Seat) domain.SeatSelection {
	selection := domain.SeatSelection{}
	if snapshot == nil || snapshot.GetLayout() == nil {
		return selection
	}
	selection.SeatMap = domain.SeatMap{
		AuditoriumID: snapshot.GetAuditoriumId(),
		Version:      snapshot.GetLayoutHash(),
	}
	if observedAt := snapshot.GetObservedAt(); observedAt != nil {
		selection.SeatMap.ObservedAt = observedAt.AsTime()
	}
	for _, value := range snapshot.GetLayout().GetSeats() {
		if value == nil {
			continue
		}
		selection.SeatMap.Seats = append(selection.SeatMap.Seats, domain.Seat{
			ID: value.GetId(), AuditoriumID: value.GetAuditoriumId(), Label: value.GetLabel(),
			Row: value.GetRow(), Number: int(value.GetNumber()), X: value.GetX(), Y: value.GetY(),
			Type: domain.SeatType(value.GetType()), ZoneName: value.GetZoneName(), ZoneKind: value.GetZoneKind(),
			SaleFormCode: value.GetSaleFormCode(), SaleFormName: value.GetSaleFormName(),
			LeftAisle: value.GetLeftAisle(), RightAisle: value.GetRightAisle(),
			Features: append([]string(nil), value.GetFeatures()...), SourceLabel: value.GetSourceLabel(),
			SourceSeatKindCode: value.GetSourceSeatKindCode(), SourceSeatKindName: value.GetSourceSeatKindName(),
			SourceClasses: append([]string(nil), value.GetSourceClasses()...),
		})
	}
	for _, value := range snapshot.GetLayout().GetZones() {
		if value == nil {
			continue
		}
		selection.SeatMap.Zones = append(selection.SeatMap.Zones, domain.LayoutZone{
			Code: value.GetCode(), Name: value.GetName(), KindCode: value.GetKindCode(), KindName: value.GetKindName(),
			MinX: value.GetMinX(), MaxX: value.GetMaxX(), MinY: value.GetMinY(), MaxY: value.GetMaxY(),
			Capacity: int(value.GetCapacity()),
		})
	}
	for _, value := range snapshot.GetLayout().GetBlocks() {
		if value == nil {
			continue
		}
		selection.SeatMap.Blocks = append(selection.SeatMap.Blocks, domain.LayoutBlock{
			Code: value.GetCode(), Name: value.GetName(), KindCode: value.GetKindCode(), KindName: value.GetKindName(),
			MinX: value.GetMinX(), MaxX: value.GetMaxX(), MinY: value.GetMinY(), MaxY: value.GetMaxY(),
		})
	}
	for _, value := range available {
		if value == nil {
			continue
		}
		selection.LiveSeats = append(selection.LiveSeats, domain.LiveSeat{
			Label: value.GetLabel(), Available: true, SaleFormCode: value.GetSaleFormCode(),
			ObservedAt: selection.SeatMap.ObservedAt, Source: "booking-adapter",
		})
	}
	return selection
}

func stringPointer(value string) *string { return &value }
