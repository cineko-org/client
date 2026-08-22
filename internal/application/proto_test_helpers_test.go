package application

import (
	"fmt"
	"math"
	"strings"
	"time"

	clientpb "github.com/cineko-org/contracts/gen/go/cineko/client"
	commonpb "github.com/cineko-org/contracts/gen/go/cineko/common"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"
)

func presetFixtureForTest(id, userID, theaterID, auditoriumID string, explicitSeats []string) *clientpb.Preset {
	name := "center"
	seatCount := 1
	together := true
	seatCountValue := mustInt32ForTest(seatCount)
	return clientpb.Preset_builder{
		Id: &id, UserId: &userID, Name: &name, TheaterId: &theaterID, AuditoriumId: &auditoriumID,
		SeatCount: &seatCountValue,
		SeatPreference: clientpb.SeatPreference_builder{
			ExplicitSeats: append([]string(nil), explicitSeats...), Together: &together,
		}.Build(),
	}.Build()
}

func monitorFixtureForTest(presetID, title string, targetDates []string) *clientpb.Monitor {
	id, userID := "monitor", "user"
	movieID := "movie_1"
	horizon := int32(0)
	return clientpb.Monitor_builder{
		Id: &id, UserId: &userID, PresetId: &presetID, MovieId: &movieID, MovieTitle: &title,
		TargetDates: localDatesForTest(targetDates), SearchHorizonDays: &horizon,
		State:     clientpb.MonitorState_builder{Pending: clientpb.MonitorPending_builder{}.Build()}.Build(),
		CreatedAt: timestamppb.Now(), UpdatedAt: timestamppb.Now(),
	}.Build()
}

func bookedReservationFixtureForTest(monitorID string) *clientpb.Reservation {
	id, userID := "reservation", "user"
	return clientpb.Reservation_builder{
		Id: &id, UserId: &userID, MonitorId: &monitorID,
		Booked: clientpb.ReservationBooked_builder{}.Build(),
	}.Build()
}

func cancellationResultFixtureForTest(refundAmount string) *clientpb.WebUICancellationResult {
	reservationID, bookingNumber := "reservation", "booking"
	return clientpb.WebUICancellationResult_builder{ReservationId: &reservationID, BookingNumber: &bookingNumber, RefundAmount: &refundAmount}.Build()
}

func presetMutationForTest(revision int64, id, userID, name, theaterID, auditoriumID string, seatCount int, preference *clientpb.SeatPreference) *clientpb.WebUIResourceMutation {
	seatCountValue := mustInt32ForTest(seatCount)
	return clientpb.WebUIResourceMutation_builder{
		Mutation: commonpb.MutationIdentity_builder{ExpectedRevision: &revision}.Build(),
		Preset: clientpb.Preset_builder{
			Id: &id, UserId: &userID, Name: &name, TheaterId: &theaterID, AuditoriumId: &auditoriumID,
			SeatCount: &seatCountValue, SeatPreference: preference,
		}.Build(),
	}.Build()
}

//nolint:unparam // commandID mirrors MutationIdentity even though current fixtures intentionally omit it.
func monitorMutationForTest(revision int64, commandID, id, userID, presetID, movieID, movie string, targetDates []string, targetWeekdays []int, horizon int, earliest, latest string) *clientpb.WebUIResourceMutation {
	horizonValue := mustInt32ForTest(horizon)
	monitor := clientpb.Monitor_builder{
		Id: &id, UserId: &userID, PresetId: &presetID, MovieId: &movieID, MovieTitle: &movie,
		TargetDates: localDatesForTest(targetDates), TargetWeekdays: int32sForTest(targetWeekdays), SearchHorizonDays: &horizonValue,
		EarliestTime: localTimeForTest(earliest), LatestTime: localTimeForTest(latest),
		State: clientpb.MonitorState_builder{Pending: clientpb.MonitorPending_builder{}.Build()}.Build(),
	}.Build()
	command := strings.TrimSpace(commandID)
	return clientpb.WebUIResourceMutation_builder{
		Mutation: commonpb.MutationIdentity_builder{CommandId: &command, ExpectedRevision: &revision}.Build(), Monitor: monitor,
	}.Build()
}

func clonePresetMutationForTest(value *clientpb.WebUIResourceMutation) *clientpb.WebUIResourceMutation {
	return cloneResourceMutationForTest(value)
}

func cloneMonitorMutationForTest(value *clientpb.WebUIResourceMutation) *clientpb.WebUIResourceMutation {
	return cloneResourceMutationForTest(value)
}

func cloneResourceMutationForTest(value *clientpb.WebUIResourceMutation) *clientpb.WebUIResourceMutation {
	cloned, ok := proto.Clone(value).(*clientpb.WebUIResourceMutation)
	if !ok {
		panic("cloned resource mutation has an unexpected Proto type")
	}
	return cloned
}

func boolPointer(value bool) *bool { return &value }

func localDatesForTest(values []string) []*commonpb.LocalDate {
	result := make([]*commonpb.LocalDate, 0, len(values))
	for _, value := range values {
		parsed, err := time.Parse(time.DateOnly, value)
		if err != nil {
			continue
		}
		year, month, day := mustInt32ForTest(parsed.Year()), mustInt32ForTest(int(parsed.Month())), mustInt32ForTest(parsed.Day())
		result = append(result, commonpb.LocalDate_builder{Year: &year, Month: &month, Day: &day}.Build())
	}
	return result
}

func localTimeForTest(value string) *commonpb.LocalTime {
	if value == "" {
		return nil
	}
	parsed, err := time.Parse("15:04", value)
	if err != nil {
		return nil
	}
	h, m := mustInt32ForTest(parsed.Hour()), mustInt32ForTest(parsed.Minute())
	return commonpb.LocalTime_builder{Hour: &h, Minute: &m}.Build()
}

func int32sForTest(values []int) []int32 {
	result := make([]int32, len(values))
	for index, value := range values {
		result[index] = mustInt32ForTest(value)
	}
	return result
}

func mustInt32ForTest(value int) int32 {
	if value < math.MinInt32 || value > math.MaxInt32 {
		panic(fmt.Sprintf("test integer %d is outside the protobuf int32 range", value))
	}
	return int32(value)
}
