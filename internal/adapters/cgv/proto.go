package cgv

import (
	"fmt"
	"math"
	"strings"
	"time"

	"github.com/cineko-org/client/internal/domain"
	catalogpb "github.com/cineko-org/contracts/gen/go/cineko/catalog"
	commonpb "github.com/cineko-org/contracts/gen/go/cineko/common"
	"google.golang.org/protobuf/types/known/timestamppb"
)

func theaterDomainFromProto(value *catalogpb.Theater) domain.Theater {
	if value == nil {
		return domain.Theater{}
	}
	return domain.Theater{
		ID: value.GetId(), ProviderID: value.GetProviderId(), SourceKey: value.GetSourceKey(),
		Region: value.GetRegion(), Name: value.GetName(),
	}
}

// showtimeDomainFromProto is an adapter-internal projection for the CGV DOM
// workflow. The application boundary remains catalogpb.Showtime; this value
// only carries the provider-specific display fields required by navigation.
func showtimeDomainFromProto(value *catalogpb.Showtime) domain.Showtime {
	if value == nil {
		return domain.Showtime{}
	}
	result := domain.Showtime{
		ID: value.GetId(), ProviderID: value.GetProviderId(), SourceKey: value.GetSourceKey(),
		TheaterID: value.GetTheaterId(), AvailableSeats: int(value.GetAvailableSeats()),
		Capacity: int(value.GetCapacity()), SoldOut: value.GetSoldOut(),
	}
	if startsAt := value.GetStartsAt(); startsAt != nil && startsAt.IsValid() {
		localStart := startsAt.AsTime().In(domain.KoreaLocation)
		result.StartsAt = localStart.Format("15:04")
		result.CivilDate = localStart.Format(time.DateOnly)
	}
	if endsAt := value.GetEndsAt(); endsAt != nil && endsAt.IsValid() {
		result.EndsAt = endsAt.AsTime().In(domain.KoreaLocation).Format("15:04")
	}
	if movie := value.GetMovie(); movie != nil {
		result.MovieID, result.Movie, result.PosterURL = movie.GetId(), movie.GetTitle(), movie.GetPosterUrl()
	}
	if auditorium := value.GetAuditorium(); auditorium != nil {
		result.AuditoriumID, result.AuditoriumName = auditorium.GetId(), auditorium.GetName()
		result.ScreenTypes = append([]string(nil), auditorium.GetScreenTypes()...)
	}
	if date, err := domain.ScheduleDateFromShowtimeSourceKey(result.SourceKey); err == nil {
		result.Date = date
	}
	return result
}

func showtimeProtoFromDomain(value domain.Showtime) *catalogpb.Showtime {
	if strings.TrimSpace(value.ID) == "" {
		return nil
	}
	id, providerID, sourceKey, theaterID := value.ID, value.ProviderID, value.SourceKey, value.TheaterID
	movieID, title, posterURL := value.MovieID, value.Movie, value.PosterURL
	auditoriumID, auditoriumName := value.AuditoriumID, value.AuditoriumName
	emptySource := ""
	availableSeats, capacity := boundedInt32(value.AvailableSeats), boundedInt32(value.Capacity)
	soldOut := value.SoldOut
	return catalogpb.Showtime_builder{
		Id: &id, ProviderId: &providerID, SourceKey: &sourceKey, TheaterId: &theaterID,
		Movie: catalogpb.Movie_builder{
			Id: &movieID, ProviderId: &providerID, SourceKey: &emptySource,
			Title: &title, PosterUrl: &posterURL,
		}.Build(),
		Auditorium: catalogpb.Auditorium_builder{
			Id: &auditoriumID, TheaterId: &theaterID, SourceKey: &emptySource,
			Name: &auditoriumName, ScreenTypes: append([]string(nil), value.ScreenTypes...), Capacity: &capacity,
		}.Build(),
		StartsAt: showtimeTimestamp(value.Date, value.StartsAt), EndsAt: showtimeTimestamp(value.Date, value.EndsAt),
		AvailableSeats: &availableSeats, Capacity: &capacity, SoldOut: &soldOut,
	}.Build()
}

// boundedInt32 preserves provider counts at the Proto boundary without an
// unchecked machine-width conversion. CGV counts are non-negative; malformed
// negative values become zero and impossible oversized values are capped.
func boundedInt32(value int) int32 {
	switch {
	case value < 0:
		return 0
	case value > math.MaxInt32:
		return math.MaxInt32
	default:
		return int32(value)
	}
}

func showtimeTimestamp(date, clock string) *timestamppb.Timestamp {
	date, clock = strings.TrimSpace(date), strings.TrimSpace(clock)
	if date == "" || clock == "" {
		return nil
	}
	parsed, err := time.ParseInLocation(time.DateTime, date+" "+clock, domain.KoreaLocation)
	if err != nil {
		return nil
	}
	return timestamppb.New(parsed)
}

func int32ValuesToInt(values []int32) []int {
	result := make([]int, len(values))
	for index, value := range values {
		result[index] = int(value)
	}
	return result
}

func localTimeToString(value *commonpb.LocalTime) string {
	if value == nil {
		return ""
	}
	return fmt.Sprintf("%02d:%02d", value.GetHour(), value.GetMinute())
}
