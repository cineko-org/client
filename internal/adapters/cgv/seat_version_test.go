package cgv

import (
	"testing"

	"github.com/cineko-org/client/internal/domain"
)

func TestSeatMapVersionChangesForEveryPersistedLayoutValue(t *testing.T) {
	baseSeat := domain.Seat{
		Label: "A1", Row: "A", Number: 1, X: 0.25, Y: 0.5, Type: domain.SeatTypeStandard,
		ZoneName: "Light존", ZoneKind: "일반", SaleFormCode: "01", SaleFormName: "일반",
		LeftAisle: true, Features: []string{"zone:Light존"}, SourceLabel: "A1",
		SourceSeatKindCode: "01", SourceSeatKindName: "일반석", SourceClasses: []string{"seatNormal"},
	}
	baseZone := domain.LayoutZone{Code: "zone", Name: "Light존", KindCode: "01", KindName: "일반", MaxX: 1, MaxY: 1, Capacity: 1}
	baseBlock := domain.LayoutBlock{Code: "block", Name: "중앙", KindCode: "01", KindName: "일반", MaxX: 1, MaxY: 1}
	baseline := seatMapVersion([]domain.Seat{baseSeat}, []domain.LayoutZone{baseZone}, []domain.LayoutBlock{baseBlock})

	tests := []struct {
		name   string
		mutate func(*domain.Seat, *domain.LayoutZone, *domain.LayoutBlock)
	}{
		{name: "coordinate", mutate: func(seat *domain.Seat, _ *domain.LayoutZone, _ *domain.LayoutBlock) { seat.X = 0.3 }},
		{name: "seat type", mutate: func(seat *domain.Seat, _ *domain.LayoutZone, _ *domain.LayoutBlock) {
			seat.Type = domain.SeatTypeRecliner
		}},
		{name: "zone kind", mutate: func(seat *domain.Seat, _ *domain.LayoutZone, _ *domain.LayoutBlock) { seat.ZoneKind = "프리미엄" }},
		{name: "sale form name", mutate: func(seat *domain.Seat, _ *domain.LayoutZone, _ *domain.LayoutBlock) {
			seat.SaleFormName = "리클라이너"
		}},
		{name: "aisle", mutate: func(seat *domain.Seat, _ *domain.LayoutZone, _ *domain.LayoutBlock) { seat.RightAisle = true }},
		{name: "source kind", mutate: func(seat *domain.Seat, _ *domain.LayoutZone, _ *domain.LayoutBlock) {
			seat.SourceSeatKindName = "모션"
		}},
		{name: "features", mutate: func(seat *domain.Seat, _ *domain.LayoutZone, _ *domain.LayoutBlock) {
			seat.Features = append(seat.Features, "motion")
		}},
		{name: "zone kind name", mutate: func(_ *domain.Seat, zone *domain.LayoutZone, _ *domain.LayoutBlock) { zone.KindName = "프리미엄" }},
		{name: "block kind name", mutate: func(_ *domain.Seat, _ *domain.LayoutZone, block *domain.LayoutBlock) { block.KindName = "출입구" }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			seat, zone, block := baseSeat, baseZone, baseBlock
			seat.Features = append([]string(nil), baseSeat.Features...)
			seat.SourceClasses = append([]string(nil), baseSeat.SourceClasses...)
			test.mutate(&seat, &zone, &block)
			version := seatMapVersion([]domain.Seat{seat}, []domain.LayoutZone{zone}, []domain.LayoutBlock{block})
			if version == baseline {
				t.Fatalf("seatMapVersion() did not change after %s changed", test.name)
			}
		})
	}
}

func TestSeatMapVersionIgnoresFeatureOrder(t *testing.T) {
	left := domain.Seat{Label: "A1", Row: "A", Number: 1, Type: domain.SeatTypeStandard, Features: []string{"a", "b"}}
	right := left
	right.Features = []string{"b", "a"}
	if seatMapVersion([]domain.Seat{left}, nil, nil) != seatMapVersion([]domain.Seat{right}, nil, nil) {
		t.Fatal("seatMapVersion() changed for equivalent feature order")
	}
}

func TestInferSeatTypePreservesCGVCommercialSeatKinds(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		source string
		want   domain.SeatType
	}{
		{source: "C)리클라이너", want: domain.SeatTypeRecliner},
		{source: "4DX PRIME", want: domain.SeatTypePrime},
		{source: "PREMIUM", want: domain.SeatTypePremium},
		{source: "seatMap_seatPrimium", want: domain.SeatTypePremium},
		{source: "이동식 휠체어", want: domain.SeatTypeWheelchair},
	} {
		if got := inferSeatType(test.source, nil); got != test.want {
			t.Fatalf("inferSeatType(%q) = %q, want %q", test.source, got, test.want)
		}
	}
}
