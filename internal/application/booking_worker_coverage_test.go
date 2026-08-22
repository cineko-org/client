package application

import (
	"strings"

	"github.com/cineko-org/client/internal/domain"
	catalogpb "github.com/cineko-org/contracts/v3/gen/go/cineko/catalog"
)

// These message builders are shared by the Central-claimed execution tests.
// Central owns observation and retry lifecycle coverage; Client tests only
// exercise the exact execution command path.
func coverageTheater(value domain.Theater) *catalogpb.Theater {
	id, providerID, region, name := value.ID, value.ProviderID, value.Region, value.Name
	return catalogpb.Theater_builder{
		Id: &id, ProviderId: &providerID, Identity: catalogTestTheaterIdentity(value.SourceKey),
		Region: &region, Name: &name,
	}.Build()
}

func coverageAuditorium(value domain.Auditorium) *catalogpb.Auditorium {
	id, theaterID, name := value.ID, value.TheaterID, value.Name
	capacity := mustInt32ForTest(value.Capacity)
	parts := strings.Split(value.SourceKey, "/")
	siteNo, screenNo := "56", "7"
	if len(parts) >= 2 {
		siteNo, screenNo = parts[0], parts[len(parts)-1]
	}
	return catalogpb.Auditorium_builder{
		Id: &id, TheaterId: &theaterID, Identity: catalogTestAuditoriumIdentity(siteNo, screenNo),
		Name: &name, ScreenTypes: append([]string(nil), value.ScreenTypes...), Capacity: &capacity,
	}.Build()
}
