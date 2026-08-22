package cgv

import (
	"crypto/sha256"
	"encoding/hex"
	"slices"
	"time"

	"github.com/cineko-org/client/internal/domain"
	seatmappb "github.com/cineko-org/contracts/v3/gen/go/cineko/seatmap"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"
)

func seatSnapshotProto(value parsedSeatSnapshot, auditoriumID string) *seatmappb.Snapshot {
	id := seatID(auditoriumID, value.Hash)
	layoutHash := value.Hash
	capacity := boundedInt32(len(value.Seats))
	if value.Captured.IsZero() {
		value.Captured = time.Now()
	}
	layout := canonicalLayoutProto(value)
	return seatmappb.Snapshot_builder{
		Id: &id, AuditoriumId: &auditoriumID, LayoutHash: &layoutHash, Capacity: &capacity,
		Layout: layout, ObservedAt: timestamppb.New(value.Captured),
	}.Build()
}

// canonicalLayoutProto orders every geometry collection before hashing or
// transport so provider response ordering and live availability cannot create
// a false layout revision.
func canonicalLayoutProto(value parsedSeatSnapshot) *seatmappb.Layout {
	seats := slices.Clone(value.Seats)
	for index := range seats {
		seats[index].Features = slices.Clone(seats[index].Features)
		slices.Sort(seats[index].Features)
		seats[index].SourceClasses = slices.Clone(seats[index].SourceClasses)
		slices.Sort(seats[index].SourceClasses)
	}
	slices.SortFunc(seats, func(left, right domain.Seat) int {
		return compareStrings(left.ID, right.ID, left.Label, right.Label)
	})
	zones := slices.Clone(value.Zones)
	slices.SortFunc(zones, func(left, right domain.LayoutZone) int {
		return compareStrings(left.Code, right.Code, left.Name, right.Name)
	})
	blocks := slices.Clone(value.Blocks)
	slices.SortFunc(blocks, func(left, right domain.LayoutBlock) int {
		return compareStrings(left.Code, right.Code, left.Name, right.Name)
	})
	layout := seatmappb.Layout_builder{}
	for _, seat := range seats {
		layout.Seats = append(layout.Seats, seatProto(seat))
	}
	for _, zone := range zones {
		code, name, kindCode, kindName := zone.Code, zone.Name, zone.KindCode, zone.KindName
		minX, maxX, minY, maxY := zone.MinX, zone.MaxX, zone.MinY, zone.MaxY
		zoneCapacity := boundedInt32(zone.Capacity)
		layout.Zones = append(layout.Zones, seatmappb.LayoutZone_builder{
			Code: &code, Name: &name, KindCode: &kindCode, KindName: &kindName,
			MinX: &minX, MaxX: &maxX, MinY: &minY, MaxY: &maxY, Capacity: &zoneCapacity,
		}.Build())
	}
	for _, block := range blocks {
		code, name, kindCode, kindName := block.Code, block.Name, block.KindCode, block.KindName
		minX, maxX, minY, maxY := block.MinX, block.MaxX, block.MinY, block.MaxY
		layout.Blocks = append(layout.Blocks, seatmappb.LayoutBlock_builder{
			Code: &code, Name: &name, KindCode: &kindCode, KindName: &kindName,
			MinX: &minX, MaxX: &maxX, MinY: &minY, MaxY: &maxY,
		}.Build())
	}
	return layout.Build()
}

func canonicalLayoutHash(value parsedSeatSnapshot) (string, error) {
	encoded, err := proto.MarshalOptions{Deterministic: true}.Marshal(canonicalLayoutProto(value))
	if err != nil {
		return "", err
	}
	digest := sha256.Sum256(encoded)
	return hex.EncodeToString(digest[:]), nil
}

func compareStrings(leftPrimary, rightPrimary, leftSecondary, rightSecondary string) int {
	if leftPrimary < rightPrimary {
		return -1
	}
	if leftPrimary > rightPrimary {
		return 1
	}
	if leftSecondary < rightSecondary {
		return -1
	}
	if leftSecondary > rightSecondary {
		return 1
	}
	return 0
}

func seatProto(value domain.Seat) *seatmappb.Seat {
	id, auditoriumID, label, row := value.ID, value.AuditoriumID, value.Label, value.Row
	number := boundedInt32(value.Number)
	x, y := value.X, value.Y
	seatType, zoneName, zoneKind := string(value.Type), value.ZoneName, value.ZoneKind
	saleFormCode, saleFormName := value.SaleFormCode, value.SaleFormName
	leftAisle, rightAisle := value.LeftAisle, value.RightAisle
	sourceLabel, sourceKindCode, sourceKindName := value.SourceLabel, value.SourceSeatKindCode, value.SourceSeatKindName
	return seatmappb.Seat_builder{
		Id: &id, AuditoriumId: &auditoriumID, Label: &label, Row: &row, Number: &number,
		X: &x, Y: &y, Type: &seatType, ZoneName: &zoneName, ZoneKind: &zoneKind,
		SaleFormCode: &saleFormCode, SaleFormName: &saleFormName,
		LeftAisle: &leftAisle, RightAisle: &rightAisle,
		Features: append([]string(nil), value.Features...), SourceLabel: &sourceLabel,
		SourceSeatKindCode: &sourceKindCode, SourceSeatKindName: &sourceKindName,
		SourceClasses: append([]string(nil), value.SourceClasses...),
	}.Build()
}

func liveSeatObservationProto(
	snapshot *seatmappb.Snapshot,
	showtimeID string,
	live []domain.LiveSeat,
) *seatmappb.LiveSeatObservation {
	if snapshot == nil || snapshot.GetLayout() == nil {
		return seatmappb.LiveSeatObservation_builder{}.Build()
	}
	availableLabels := make(map[string]struct{}, len(live))
	for _, value := range live {
		if value.Available {
			availableLabels[value.Label] = struct{}{}
		}
	}
	available := make([]*seatmappb.AvailableSeat, 0, len(availableLabels))
	for _, seat := range snapshot.GetLayout().GetSeats() {
		if seat == nil {
			continue
		}
		if _, ok := availableLabels[seat.GetLabel()]; ok {
			seatID := seat.GetId()
			available = append(available, seatmappb.AvailableSeat_builder{SeatId: &seatID}.Build())
		}
	}
	auditoriumID, layoutHash := snapshot.GetAuditoriumId(), snapshot.GetLayoutHash()
	availability := seatmappb.AvailabilitySnapshot_builder{
		ShowtimeId: &showtimeID, AuditoriumId: &auditoriumID, LayoutHash: &layoutHash,
		AvailableSeats: available, ObservedAt: snapshot.GetObservedAt(),
	}.Build()
	return seatmappb.LiveSeatObservation_builder{Layout: snapshot, Availability: availability}.Build()
}
