package application

import (
	"time"

	"github.com/cineko-org/client/internal/domain"
	catalogpb "github.com/cineko-org/contracts/gen/go/cineko/catalog"
)

// showtimeDomainFromProto creates the small process-internal projection used
// by the schedule-window algorithm. Generated Showtimes remain the only
// values crossing application ports and persistence boundaries.
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
