package webui

import (
	"fmt"
	"math"
	"strings"
	"time"

	"github.com/cineko-org/client/internal/domain"
	catalogpb "github.com/cineko-org/contracts/gen/go/cineko/catalog"
	clientpb "github.com/cineko-org/contracts/gen/go/cineko/client"
	commonpb "github.com/cineko-org/contracts/gen/go/cineko/common"
	seatmappb "github.com/cineko-org/contracts/gen/go/cineko/seatmap"
	"google.golang.org/protobuf/types/known/timestamppb"
)

func theaterProtoForTest(value domain.Theater) *catalogpb.Theater {
	id, providerID, sourceKey, region, name := value.ID, value.ProviderID, value.SourceKey, value.Region, value.Name
	return catalogpb.Theater_builder{
		Id: &id, ProviderId: &providerID, SourceKey: &sourceKey, Region: &region, Name: &name,
	}.Build()
}

func showtimeProtoForTest(value domain.Showtime) *catalogpb.Showtime {
	message := showtimeToProto(value)
	location := time.FixedZone("Asia/Seoul", 9*60*60)
	date := value.Date
	if startsAt, err := time.ParseInLocation(time.DateOnly+" 15:04", date+" "+value.StartsAt, location); err == nil {
		message.SetStartsAt(timestamppb.New(startsAt))
	}
	if endsAt, err := time.ParseInLocation(time.DateOnly+" 15:04", date+" "+value.EndsAt, location); err == nil {
		message.SetEndsAt(timestamppb.New(endsAt))
	}
	return message
}

func resourceFromPreset(value *clientpb.Preset) *clientpb.Resource {
	return clientpb.Resource_builder{
		Identity: resourceIdentity(value.GetId(), time.Time{}),
		Preset:   value,
	}.Build()
}

func resourceFromMonitor(value *clientpb.Monitor) *clientpb.Resource {
	return clientpb.Resource_builder{
		Identity: resourceIdentity(value.GetId(), time.Time{}),
		Monitor:  value,
	}.Build()
}

func resourceFromReservation(value *clientpb.Reservation) *clientpb.Resource {
	return clientpb.Resource_builder{
		Identity:    resourceIdentity(value.GetId(), time.Time{}),
		Reservation: value,
	}.Build()
}

func presetProtoFixture(theaterID, auditoriumID string, candidates []string) *clientpb.Preset {
	id := "preset"
	userID := "user"
	name := "seat"
	seatCount := int32(1)
	together := true
	return clientpb.Preset_builder{
		Id: &id, UserId: &userID, Name: &name, TheaterId: &theaterID, AuditoriumId: &auditoriumID,
		SeatCount: &seatCount,
		SeatPreference: clientpb.SeatPreference_builder{
			ExplicitSeats: append([]string(nil), candidates...), Together: &together,
		}.Build(),
	}.Build()
}

func monitorProtoFixture(
	presetID, movieID, movieTitle string,
	targetDates []string,
	state *clientpb.MonitorState,
	reservationID string,
) *clientpb.Monitor {
	id := "monitor"
	userID := "user"
	return clientpb.Monitor_builder{
		Id: &id, UserId: &userID, PresetId: &presetID,
		MovieId: &movieID, MovieTitle: &movieTitle, TargetDates: localDates(targetDates),
		State: state, ReservationId: &reservationID,
	}.Build()
}

func reservationProtoFixture(id, userID, monitorID string) *clientpb.Reservation {
	return clientpb.Reservation_builder{
		Id: &id, UserId: &userID, MonitorId: &monitorID,
		Prepared: clientpb.ReservationPrepared_builder{}.Build(),
	}.Build()
}

func showtimeToProto(value domain.Showtime) *catalogpb.Showtime {
	id, providerID, sourceKey := value.ID, value.ProviderID, value.SourceKey
	theaterID, movieID, movieTitle, posterURL := value.TheaterID, value.MovieID, value.Movie, value.PosterURL
	auditoriumID, auditoriumName := value.AuditoriumID, value.AuditoriumName
	availableSeats, capacity, soldOut := mustInt32(value.AvailableSeats), mustInt32(value.Capacity), value.SoldOut
	movie := catalogpb.Movie_builder{Id: &movieID, SourceKey: &movieID, Title: &movieTitle, PosterUrl: &posterURL}.Build()
	auditorium := catalogpb.Auditorium_builder{
		Id: &auditoriumID, TheaterId: &theaterID, SourceKey: &auditoriumID, Name: &auditoriumName,
		ScreenTypes: append([]string(nil), value.ScreenTypes...), Capacity: &capacity,
	}.Build()
	return catalogpb.Showtime_builder{
		Id: &id, ProviderId: &providerID, SourceKey: &sourceKey, TheaterId: &theaterID,
		Movie: movie, Auditorium: auditorium, ScheduleDate: localDate(value.Date),
		StartsAt: timestampText(value.StartsAt), EndsAt: timestampText(value.EndsAt),
		AvailableSeats: &availableSeats, Capacity: &capacity, SoldOut: &soldOut,
	}.Build()
}

func timestampText(value string) *timestamppb.Timestamp {
	parsed, err := time.Parse(time.RFC3339, strings.TrimSpace(value))
	if err != nil {
		return nil
	}
	return timestamppb.New(parsed)
}

func seatMapSnapshot(value domain.SeatMap) *seatmappb.Snapshot {
	auditoriumID, version, capacity := value.AuditoriumID, value.Version, mustInt32(len(value.Seats))
	layout := seatmappb.Layout_builder{}
	for _, seat := range value.Seats {
		id, seatAuditoriumID, label, row, number, seatType := seat.ID, seat.AuditoriumID, seat.Label, seat.Row, mustInt32(seat.Number), string(seat.Type)
		layout.Seats = append(layout.Seats, seatmappb.Seat_builder{
			Id: &id, AuditoriumId: &seatAuditoriumID, Label: &label, Row: &row, Number: &number,
			X: &seat.X, Y: &seat.Y, Type: &seatType, ZoneName: &seat.ZoneName, ZoneKind: &seat.ZoneKind,
			SaleFormCode: &seat.SaleFormCode, SaleFormName: &seat.SaleFormName, LeftAisle: &seat.LeftAisle, RightAisle: &seat.RightAisle,
			Features: append([]string(nil), seat.Features...), SourceLabel: &seat.SourceLabel, SourceSeatKindCode: &seat.SourceSeatKindCode,
			SourceSeatKindName: &seat.SourceSeatKindName, SourceClasses: append([]string(nil), seat.SourceClasses...),
		}.Build())
	}
	for _, zone := range value.Zones {
		code, name, kindCode, kindName, minX, maxX, minY, maxY, zoneCapacity := zone.Code, zone.Name, zone.KindCode, zone.KindName, zone.MinX, zone.MaxX, zone.MinY, zone.MaxY, mustInt32(zone.Capacity)
		layout.Zones = append(layout.Zones, seatmappb.LayoutZone_builder{
			Code: &code, Name: &name, KindCode: &kindCode, KindName: &kindName, MinX: &minX, MaxX: &maxX,
			MinY: &minY, MaxY: &maxY, Capacity: &zoneCapacity,
		}.Build())
	}
	for _, block := range value.Blocks {
		code, name, kindCode, kindName, minX, maxX, minY, maxY := block.Code, block.Name, block.KindCode, block.KindName, block.MinX, block.MaxX, block.MinY, block.MaxY
		layout.Blocks = append(layout.Blocks, seatmappb.LayoutBlock_builder{
			Code: &code, Name: &name, KindCode: &kindCode, KindName: &kindName, MinX: &minX, MaxX: &maxX,
			MinY: &minY, MaxY: &maxY,
		}.Build())
	}
	return seatmappb.Snapshot_builder{
		Id: &auditoriumID, AuditoriumId: &auditoriumID, LayoutHash: &version, Capacity: &capacity,
		Layout: layout.Build(), ObservedAt: timestamp(value.ObservedAt),
	}.Build()
}

func auditoriumToProto(value domain.Auditorium) *catalogpb.Auditorium {
	id, theaterID, sourceKey, name, capacity, layoutHash := value.ID, value.TheaterID, value.SourceKey, value.Name, mustInt32(value.Capacity), value.SeatMapVersion
	return catalogpb.Auditorium_builder{
		Id: &id, TheaterId: &theaterID, SourceKey: &sourceKey, Name: &name,
		ScreenTypes: append([]string(nil), value.ScreenTypes...), Capacity: &capacity, CurrentLayoutHash: &layoutHash,
	}.Build()
}

func localDate(value string) *commonpb.LocalDate {
	parsed, err := time.Parse(time.DateOnly, strings.TrimSpace(value))
	if err != nil {
		return nil
	}
	year, month, day := mustInt32(parsed.Year()), mustInt32(int(parsed.Month())), mustInt32(parsed.Day())
	return commonpb.LocalDate_builder{Year: &year, Month: &month, Day: &day}.Build()
}

func localDates(values []string) []*commonpb.LocalDate {
	result := make([]*commonpb.LocalDate, 0, len(values))
	for _, value := range values {
		if parsed := localDate(value); parsed != nil {
			result = append(result, parsed)
		}
	}
	return result
}

func mustInt32(value int) int32 {
	if value < math.MinInt32 || value > math.MaxInt32 {
		panic(fmt.Sprintf("internal integer %d is outside the protobuf int32 range", value))
	}
	return int32(value)
}
