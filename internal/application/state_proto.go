package application

import (
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/cineko-org/client/internal/domain"
	clientpb "github.com/cineko-org/contracts/gen/go/cineko/client"
	commonpb "github.com/cineko-org/contracts/gen/go/cineko/common"
	seatmappb "github.com/cineko-org/contracts/gen/go/cineko/seatmap"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/durationpb"
	"google.golang.org/protobuf/types/known/timestamppb"
)

const defaultSearchHorizonDays int32 = 28

func resourceIdentity(id string, revision int64) *commonpb.ResourceIdentity {
	return commonpb.ResourceIdentity_builder{Id: &id, Revision: &revision}.Build()
}

func durationValue(value *durationpb.Duration) time.Duration {
	if value == nil {
		return 0
	}
	return value.AsDuration()
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

func resourceForExternalOperation(message *clientpb.ExternalOperation) *clientpb.Resource {
	return clientpb.Resource_builder{Identity: resourceIdentity(message.GetId(), 0), ExternalOperation: message}.Build()
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
	if value.GetSeatCount() < 1 || value.GetSeatCount() > 8 {
		return errors.New("preset seat count must be between 1 and 8")
	}
	preference := value.GetSeatPreference()
	if preference == nil || !preference.GetTogether() {
		return errors.New("unknown seat adjacency policy")
	}
	if len(preference.GetExplicitSeats()) > 0 && len(preference.GetExplicitSeats()) < int(value.GetSeatCount()) {
		return errors.New("preset candidate seats must cover the requested seat count")
	}
	if err := validateCandidateSeats(preference.GetExplicitSeats()); err != nil {
		return err
	}
	return validatePreferredZones(preference.GetPreferredZones())
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

func validatePreferredZones(zones []*clientpb.SeatZone) error {
	for _, zone := range zones {
		if zone == nil || strings.TrimSpace(zone.GetName()) == "" {
			return errors.New("seat preference zone name is required")
		}
		if !seatZoneBoundsAreValid(zone) {
			return fmt.Errorf("seat preference zone %s bounds must be ordered within 0..1", zone.GetName())
		}
	}
	return nil
}

func seatZoneBoundsAreValid(zone *clientpb.SeatZone) bool {
	return zone.GetMinX() >= 0 && zone.GetMaxX() <= 1 &&
		zone.GetMinY() >= 0 && zone.GetMaxY() <= 1 &&
		zone.GetMinX() <= zone.GetMaxX() && zone.GetMinY() <= zone.GetMaxY()
}

func validateMonitorMessage(value *clientpb.Monitor) error {
	if err := validateMonitorIdentity(value); err != nil {
		return err
	}
	if err := validateMonitorMode(value); err != nil {
		return err
	}
	if err := validateMonitorPolling(value); err != nil {
		return err
	}
	if err := validateTargetDates(value.GetTargetDates()); err != nil {
		return err
	}
	if err := validateTargetWeekdays(value.GetTargetWeekdays()); err != nil {
		return err
	}
	if len(value.GetTargetWeekdays()) > 0 && (value.GetSearchHorizonDays() < 1 || value.GetSearchHorizonDays() > 365) {
		return errors.New("weekday search horizon must be between 1 and 365 days")
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
	if strings.TrimSpace(value.GetMovieId()) == "" || len(value.GetTargetDates())+len(value.GetTargetWeekdays()) == 0 {
		return errors.New("monitor movie id and at least one target date or weekday are required")
	}
	return nil
}

func validateMonitorMode(value *clientpb.Monitor) error {
	if value.GetMode() == nil || (value.GetMode().GetOpening() == nil && value.GetMode().GetCancellation() == nil) {
		return errors.New("invalid monitor mode")
	}
	if value.GetMode().GetCancellation() != nil && len(value.GetTargetWeekdays()) > 0 {
		return errors.New("cancellation-seat monitors require exact target dates")
	}
	return nil
}

func validateMonitorPolling(value *clientpb.Monitor) error {
	poll := durationValue(value.GetPollInterval())
	maximum := durationValue(value.GetMaximumPollInterval())
	if poll < 2*time.Second {
		return errors.New("poll interval must be at least 2 seconds")
	}
	if maximum <= poll {
		return errors.New("maximum poll interval must be greater than minimum poll interval")
	}
	return nil
}

func validateTargetDates(dates []*commonpb.LocalDate) error {
	seen := make(map[string]struct{}, len(dates))
	for _, date := range dates {
		if date == nil {
			return errors.New("invalid target date")
		}
		parsed := time.Date(int(date.GetYear()), time.Month(date.GetMonth()), int(date.GetDay()), 0, 0, 0, 0, time.UTC)
		if parsed.Year() != int(date.GetYear()) || parsed.Month() != time.Month(date.GetMonth()) || parsed.Day() != int(date.GetDay()) {
			return fmt.Errorf("invalid target date %04d-%02d-%02d", date.GetYear(), date.GetMonth(), date.GetDay())
		}
		key := fmt.Sprintf("%04d-%02d-%02d", date.GetYear(), date.GetMonth(), date.GetDay())
		if _, exists := seen[key]; exists {
			return fmt.Errorf("duplicate target date %s", key)
		}
		seen[key] = struct{}{}
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

func monitorModeIsCancellation(value *clientpb.Monitor) bool {
	return value != nil && value.GetMode() != nil && value.GetMode().GetCancellation() != nil
}

func monitorPollInterval(value *clientpb.Monitor) time.Duration {
	if value == nil || value.GetPollInterval() == nil {
		return 3 * time.Minute
	}
	return value.GetPollInterval().AsDuration()
}

func monitorPollIntervalMax(value *clientpb.Monitor) time.Duration {
	if value == nil {
		return 8 * time.Minute
	}
	if maximum := durationValue(value.GetMaximumPollInterval()); maximum > 0 {
		return maximum
	}
	interval := monitorPollInterval(value)
	return interval + interval/5
}

func applyMonitorDefaults(value *clientpb.Monitor) {
	if value.GetMode() == nil {
		value.SetMode(clientpb.MonitorMode_builder{Opening: clientpb.OpeningMonitor_builder{}.Build()}.Build())
	}
	if value.GetPollInterval() == nil || value.GetPollInterval().AsDuration() <= 0 {
		value.SetPollInterval(durationpb.New(3 * time.Minute))
	}
	if value.GetMaximumPollInterval() == nil || value.GetMaximumPollInterval().AsDuration() <= 0 {
		value.SetMaximumPollInterval(durationpb.New(8 * time.Minute))
	}
	if len(value.GetTargetWeekdays()) > 0 && value.GetSearchHorizonDays() == 0 {
		value.SetSearchHorizonDays(defaultSearchHorizonDays)
	}
	if value.GetState() == nil {
		value.SetState(clientpb.MonitorState_builder{Pending: clientpb.MonitorPending_builder{}.Build()}.Build())
	}
}

func monitorIsExpired(value *clientpb.Monitor, now time.Time) bool {
	if value == nil || len(value.GetTargetWeekdays()) > 0 || len(value.GetTargetDates()) == 0 {
		return false
	}
	localNow := now.In(time.FixedZone("Asia/Seoul", 9*60*60))
	today := time.Date(localNow.Year(), localNow.Month(), localNow.Day(), 0, 0, 0, 0, localNow.Location())
	for _, date := range value.GetTargetDates() {
		if date == nil {
			continue
		}
		candidate := time.Date(int(date.GetYear()), time.Month(date.GetMonth()), int(date.GetDay()), 0, 0, 0, 0, today.Location())
		if !candidate.Before(today) {
			return false
		}
	}
	return true
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
		stateValue.Stopped = clientpb.MonitorStopped_builder{}.Build()
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

func monitorRecordCheck(value *clientpb.Monitor, now time.Time, cause error) {
	if value == nil {
		return
	}
	value.SetLastCheckedAt(timestamppb.New(now))
	value.SetUpdatedAt(timestamppb.New(now))
	if cause == nil {
		if value.GetState() != nil && value.GetState().GetFailed() != nil {
			setMonitorState(value, "running", "")
		}
		return
	}
	setMonitorFailure(value, cause.Error())
}

func monitorResolveTargetDates(value *clientpb.Monitor, now time.Time) []string {
	if value == nil {
		return nil
	}
	location := time.FixedZone("Asia/Seoul", 9*60*60)
	localNow := now.In(location)
	today := time.Date(localNow.Year(), localNow.Month(), localNow.Day(), 0, 0, 0, 0, location)
	seen := make(map[string]struct{}, len(value.GetTargetDates())+int(value.GetSearchHorizonDays()))
	for _, date := range value.GetTargetDates() {
		if date == nil {
			continue
		}
		candidate := time.Date(int(date.GetYear()), time.Month(date.GetMonth()), int(date.GetDay()), 0, 0, 0, 0, location)
		if !candidate.Before(today) {
			seen[fmt.Sprintf("%04d-%02d-%02d", date.GetYear(), date.GetMonth(), date.GetDay())] = struct{}{}
		}
	}
	for offset := int32(0); offset < value.GetSearchHorizonDays(); offset++ {
		candidate := today.AddDate(0, 0, int(offset))
		for _, weekday := range value.GetTargetWeekdays() {
			if int(candidate.Weekday()) == int(weekday) {
				seen[candidate.Format(time.DateOnly)] = struct{}{}
				break
			}
		}
	}
	result := make([]string, 0, len(seen))
	for date := range seen {
		result = append(result, date)
	}
	sort.Strings(result)
	return result
}

func int32Values(values []int32) []int {
	result := make([]int, len(values))
	for index, value := range values {
		result[index] = int(value)
	}
	return result
}

func seatPreferenceForRanking(value *clientpb.SeatPreference) domain.SeatPreference {
	if value == nil {
		return domain.SeatPreference{}
	}
	preference := domain.SeatPreference{
		CandidateSeats: append([]string(nil), value.GetExplicitSeats()...),
		PreferredRows:  append([]string(nil), value.GetPreferredRows()...),
		AvoidEdges:     value.GetAvoidEdges(),
	}
	if value.GetTogether() {
		preference.Adjacency = domain.SeatAdjacencyRequired
	}
	for _, seatType := range value.GetPreferredTypes() {
		preference.PreferredTypes = append(preference.PreferredTypes, domain.SeatType(seatType))
	}
	for _, zone := range value.GetPreferredZones() {
		if zone == nil {
			continue
		}
		preference.PreferredZones = append(preference.PreferredZones, domain.SeatZone{
			Name: zone.GetName(), MinX: zone.GetMinX(), MaxX: zone.GetMaxX(),
			MinY: zone.GetMinY(), MaxY: zone.GetMaxY(), Weight: int(zone.GetWeight()),
		})
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
