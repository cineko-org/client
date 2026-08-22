package cgv

import (
	"fmt"
	"math"
	"time"

	"github.com/cineko-org/client/internal/domain"
	catalogpb "github.com/cineko-org/contracts/gen/go/cineko/catalog"
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
	if scheduleDate := value.GetScheduleDate(); scheduleDate != nil {
		result.Date = fmt.Sprintf("%04d-%02d-%02d", scheduleDate.GetYear(), scheduleDate.GetMonth(), scheduleDate.GetDay())
	}
	return result
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
