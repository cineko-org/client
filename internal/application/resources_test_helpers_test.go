package application

// These builders keep test resources in the same generated-Proto shape as the
// application ports. They are fixtures, not a second application model.

import (
	"fmt"
	"math"
	"strings"
	"time"

	"github.com/cineko-org/client/internal/domain"
	catalogpb "github.com/cineko-org/contracts/v3/gen/go/cineko/catalog"
	clientpb "github.com/cineko-org/contracts/v3/gen/go/cineko/client"
	commonpb "github.com/cineko-org/contracts/v3/gen/go/cineko/common"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"
)

func resourceRevision(resource *clientpb.Resource) int64 {
	if resource == nil || resource.GetIdentity() == nil {
		return 0
	}
	return resource.GetIdentity().GetRevision()
}

func cloneResourceFixture(value *clientpb.Resource) *clientpb.Resource {
	if value == nil {
		return nil
	}
	cloned, ok := proto.Clone(value).(*clientpb.Resource)
	if !ok {
		panic("cloned resource fixture has an unexpected Proto type")
	}
	return cloned
}

func presetResourceFixture(value *clientpb.Preset, revision int64) *clientpb.Resource {
	if value == nil {
		return nil
	}
	return clientpb.Resource_builder{Identity: resourceIdentity(value.GetId(), revision), Preset: value}.Build()
}

func showtimeProtoFromDomain(value domain.Showtime) *catalogpb.Showtime {
	if strings.TrimSpace(value.ID) == "" {
		return nil
	}
	id, providerID, theaterID := value.ID, value.ProviderID, value.TheaterID
	movieID, title, posterURL := value.MovieID, value.Movie, value.PosterURL
	auditoriumID, auditoriumName := value.AuditoriumID, value.AuditoriumName
	availableSeats, _ := int32Checked(value.AvailableSeats, "showtime available seats")
	capacity, _ := int32Checked(value.Capacity, "showtime capacity")
	soldOut := value.SoldOut
	showtimeIdentity := catalogTestShowtimeIdentity(value.SourceKey, value.Date)
	cgvIdentity := showtimeIdentity.GetCgv()
	return catalogpb.Showtime_builder{Id: &id, ProviderId: &providerID, Identity: showtimeIdentity, TheaterId: &theaterID,
		Movie: catalogpb.Movie_builder{Id: &movieID, ProviderId: &providerID,
			Identity: catalogTestMovieIdentity(movieID), Title: &title, PosterUrl: &posterURL}.Build(),
		Auditorium: catalogpb.Auditorium_builder{Id: &auditoriumID, TheaterId: &theaterID,
			Identity: catalogTestAuditoriumIdentity(cgvIdentity.GetSiteNo(), cgvIdentity.GetScreenNo()),
			Name:     &auditoriumName, ScreenTypes: append([]string(nil), value.ScreenTypes...), Capacity: &capacity}.Build(),
		StartsAt: showtimeTimestampsForTest(value), EndsAt: showtimeEndTimestampForTest(value),
		AvailableSeats: &availableSeats, Capacity: &capacity, SoldOut: &soldOut}.Build()
}

func catalogTestTheaterIdentity(sourceKey string) *catalogpb.TheaterIdentity {
	siteNo := numericTestPart(sourceKey, "56")
	return catalogpb.TheaterIdentity_builder{Cgv: catalogpb.CgvTheaterIdentity_builder{SiteNo: &siteNo}.Build()}.Build()
}

func catalogTestMovieIdentity(sourceKey string) *catalogpb.MovieIdentity {
	movieNo := numericTestPart(sourceKey, "1")
	return catalogpb.MovieIdentity_builder{Cgv: catalogpb.CgvMovieIdentity_builder{MovieNo: &movieNo}.Build()}.Build()
}

func catalogTestAuditoriumIdentity(siteNo, screenNo string) *catalogpb.AuditoriumIdentity {
	siteNo = numericTestPart(siteNo, "56")
	screenNo = numericTestPart(screenNo, "7")
	return catalogpb.AuditoriumIdentity_builder{Cgv: catalogpb.CgvAuditoriumIdentity_builder{
		SiteNo: &siteNo, ScreenNo: &screenNo,
	}.Build()}.Build()
}

func catalogTestShowtimeIdentity(sourceKey, date string) *catalogpb.ShowtimeIdentity {
	parts := strings.Split(sourceKey, "/")
	values := []string{"56", date, "7", "3"}
	for index := range values {
		if index < len(parts) && strings.TrimSpace(parts[index]) != "" {
			values[index] = strings.TrimSpace(parts[index])
		}
	}
	dateValue := showtimeScheduleDateForTest(values[1])
	values[0] = numericTestPart(values[0], "56")
	values[2] = numericTestPart(values[2], "7")
	values[3] = numericTestPart(values[3], "3")
	return catalogpb.ShowtimeIdentity_builder{Cgv: catalogpb.CgvShowtimeIdentity_builder{
		SiteNo: &values[0], ScheduleDate: dateValue, ScreenNo: &values[2], Sequence: &values[3],
	}.Build()}.Build()
}

func numericTestPart(value, fallback string) string {
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

func showtimeScheduleDateForTest(value string) *commonpb.LocalDate {
	parsed, err := time.Parse(time.DateOnly, strings.TrimSpace(value))
	if err != nil {
		return nil
	}
	year := mustInt32ForTest(parsed.Year())
	month := mustInt32ForTest(int(parsed.Month()))
	day := mustInt32ForTest(parsed.Day())
	return commonpb.LocalDate_builder{Year: &year, Month: &month, Day: &day}.Build()
}

func showtimeTimestampsForTest(value domain.Showtime) *timestamppb.Timestamp {
	if value.Date == "" || value.StartsAt == "" {
		return nil
	}
	parsed, err := time.ParseInLocation("2006-01-02 15:04", value.Date+" "+value.StartsAt, domain.KoreaLocation)
	if err != nil {
		return nil
	}
	return timestamppb.New(parsed)
}

func showtimeEndTimestampForTest(value domain.Showtime) *timestamppb.Timestamp {
	if value.Date == "" || value.EndsAt == "" {
		return nil
	}
	parsed, err := time.ParseInLocation("2006-01-02 15:04", value.Date+" "+value.EndsAt, domain.KoreaLocation)
	if err != nil {
		return nil
	}
	return timestamppb.New(parsed)
}

func int32Checked(value int, field string) (int32, error) {
	if value < math.MinInt32 || value > math.MaxInt32 {
		return 0, fmt.Errorf("%s %d is outside the int32 range", field, value)
	}
	return int32(value), nil
}

func domainTimestampForTest(value time.Time) *timestamppb.Timestamp {
	if value.IsZero() {
		return nil
	}
	return timestamppb.New(value)
}
