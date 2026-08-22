package webui

import (
	"fmt"
	"math"
	"strings"
	"time"

	"github.com/cineko-org/client/internal/domain"
	catalogpb "github.com/cineko-org/contracts/v3/gen/go/cineko/catalog"
	clientpb "github.com/cineko-org/contracts/v3/gen/go/cineko/client"
	commonpb "github.com/cineko-org/contracts/v3/gen/go/cineko/common"
	seatmappb "github.com/cineko-org/contracts/v3/gen/go/cineko/seatmap"
	"google.golang.org/protobuf/types/known/timestamppb"
)

func theaterProtoForTest(value domain.Theater) *catalogpb.Theater {
	id, providerID, region, name := value.ID, value.ProviderID, value.Region, value.Name
	return catalogpb.Theater_builder{
		Id: &id, ProviderId: &providerID, Identity: theaterIdentityForTest(value.SourceKey), Region: &region, Name: &name,
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
	id, providerID := value.ID, value.ProviderID
	theaterID, movieID, movieTitle, posterURL := value.TheaterID, value.MovieID, value.Movie, value.PosterURL
	auditoriumID, auditoriumName := value.AuditoriumID, value.AuditoriumName
	availableSeats, capacity, soldOut := mustInt32(value.AvailableSeats), mustInt32(value.Capacity), value.SoldOut
	identity := showtimeIdentityForTest(value.SourceKey, value.Date)
	cgvIdentity := identity.GetCgv()
	movie := catalogpb.Movie_builder{
		Id: &movieID, ProviderId: &providerID, Identity: movieIdentityForTest(movieID), Title: &movieTitle, PosterUrl: &posterURL,
	}.Build()
	auditorium := catalogpb.Auditorium_builder{
		Id: &auditoriumID, TheaterId: &theaterID,
		Identity: auditoriumIdentityForTest(cgvIdentity.GetSiteNo(), cgvIdentity.GetScreenNo()), Name: &auditoriumName,
		ScreenTypes: append([]string(nil), value.ScreenTypes...), Capacity: &capacity,
	}.Build()
	return catalogpb.Showtime_builder{
		Id: &id, ProviderId: &providerID, Identity: identity, TheaterId: &theaterID,
		Movie: movie, Auditorium: auditorium,
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
	id, theaterID, name, capacity, layoutHash := value.ID, value.TheaterID, value.Name, mustInt32(value.Capacity), value.SeatMapVersion
	parts := strings.Split(value.SourceKey, "/")
	siteNo, screenNo := "56", "7"
	if len(parts) > 0 {
		siteNo = numericIdentityPart(parts[0], siteNo)
	}
	if len(parts) > 1 {
		screenNo = numericIdentityPart(parts[len(parts)-1], screenNo)
	}
	return catalogpb.Auditorium_builder{
		Id: &id, TheaterId: &theaterID, Identity: auditoriumIdentityForTest(siteNo, screenNo), Name: &name,
		ScreenTypes: append([]string(nil), value.ScreenTypes...), Capacity: &capacity, CurrentLayoutHash: &layoutHash,
	}.Build()
}

func theaterIdentityForTest(source string) *catalogpb.TheaterIdentity {
	siteNo := numericIdentityPart(source, "56")
	return catalogpb.TheaterIdentity_builder{Cgv: catalogpb.CgvTheaterIdentity_builder{SiteNo: &siteNo}.Build()}.Build()
}

func movieIdentityForTest(source string) *catalogpb.MovieIdentity {
	movieNo := numericIdentityPart(source, "1")
	return catalogpb.MovieIdentity_builder{Cgv: catalogpb.CgvMovieIdentity_builder{MovieNo: &movieNo}.Build()}.Build()
}

func auditoriumIdentityForTest(siteNo, screenNo string) *catalogpb.AuditoriumIdentity {
	siteNo = numericIdentityPart(siteNo, "56")
	screenNo = numericIdentityPart(screenNo, "7")
	return catalogpb.AuditoriumIdentity_builder{Cgv: catalogpb.CgvAuditoriumIdentity_builder{
		SiteNo: &siteNo, ScreenNo: &screenNo,
	}.Build()}.Build()
}

func showtimeIdentityForTest(source, date string) *catalogpb.ShowtimeIdentity {
	parts := strings.Split(source, "/")
	values := []string{"56", date, "7", "3"}
	for index := range values {
		if index < len(parts) && strings.TrimSpace(parts[index]) != "" {
			values[index] = strings.TrimSpace(parts[index])
		}
	}
	values[0] = numericIdentityPart(values[0], "56")
	values[2] = numericIdentityPart(values[2], "7")
	values[3] = numericIdentityPart(values[3], "3")
	return catalogpb.ShowtimeIdentity_builder{Cgv: catalogpb.CgvShowtimeIdentity_builder{
		SiteNo: &values[0], ScheduleDate: localDate(values[1]), ScreenNo: &values[2], Sequence: &values[3],
	}.Build()}.Build()
}

func numericIdentityPart(value, fallback string) string {
	digits := strings.Map(func(character rune) rune {
		if character >= '0' && character <= '9' {
			return character
		}
		return -1
	}, value)
	if digits == "" {
		return fallback
	}
	return digits
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
