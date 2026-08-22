package application

import (
	"github.com/cineko-org/client/internal/domain"
	catalogpb "github.com/cineko-org/contracts/gen/go/cineko/catalog"
)

// These message builders are shared by the Central-claimed execution tests.
// Central owns observation and retry lifecycle coverage; Client tests only
// exercise the exact execution command path.
func coverageTheater(value domain.Theater) *catalogpb.Theater {
	id, providerID, sourceKey, region, name := value.ID, value.ProviderID, value.SourceKey, value.Region, value.Name
	return catalogpb.Theater_builder{Id: &id, ProviderId: &providerID, SourceKey: &sourceKey, Region: &region, Name: &name}.Build()
}

func coverageAuditorium(value domain.Auditorium) *catalogpb.Auditorium {
	id, theaterID, sourceKey, name := value.ID, value.TheaterID, value.SourceKey, value.Name
	capacity := mustInt32ForTest(value.Capacity)
	return catalogpb.Auditorium_builder{
		Id: &id, TheaterId: &theaterID, SourceKey: &sourceKey,
		Name: &name, ScreenTypes: append([]string(nil), value.ScreenTypes...), Capacity: &capacity,
	}.Build()
}
