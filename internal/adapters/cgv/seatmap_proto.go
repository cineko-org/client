package cgv

import (
	"time"

	"github.com/cineko-org/client/internal/domain"
	seatmappb "github.com/cineko-org/contracts/v3/gen/go/cineko/seatmap"
	"google.golang.org/protobuf/types/known/timestamppb"
)

func seatSnapshotProto(value parsedSeatSnapshot, auditoriumID string) *seatmappb.Snapshot {
	id := seatID(auditoriumID, value.Hash)
	layoutHash := value.Hash
	capacity := boundedInt32(len(value.Seats))
	if value.Captured.IsZero() {
		value.Captured = time.Now()
	}
	layout := seatmappb.Layout_builder{}
	for _, seat := range value.Seats {
		layout.Seats = append(layout.Seats, seatProto(seat))
	}
	for _, zone := range value.Zones {
		code, name, kindCode, kindName := zone.Code, zone.Name, zone.KindCode, zone.KindName
		minX, maxX, minY, maxY := zone.MinX, zone.MaxX, zone.MinY, zone.MaxY
		zoneCapacity := boundedInt32(zone.Capacity)
		layout.Zones = append(layout.Zones, seatmappb.LayoutZone_builder{
			Code: &code, Name: &name, KindCode: &kindCode, KindName: &kindName,
			MinX: &minX, MaxX: &maxX, MinY: &minY, MaxY: &maxY, Capacity: &zoneCapacity,
		}.Build())
	}
	for _, block := range value.Blocks {
		code, name, kindCode, kindName := block.Code, block.Name, block.KindCode, block.KindName
		minX, maxX, minY, maxY := block.MinX, block.MaxX, block.MinY, block.MaxY
		layout.Blocks = append(layout.Blocks, seatmappb.LayoutBlock_builder{
			Code: &code, Name: &name, KindCode: &kindCode, KindName: &kindName,
			MinX: &minX, MaxX: &maxX, MinY: &minY, MaxY: &maxY,
		}.Build())
	}
	return seatmappb.Snapshot_builder{
		Id: &id, AuditoriumId: &auditoriumID, LayoutHash: &layoutHash, Capacity: &capacity,
		Layout: layout.Build(), ObservedAt: timestamppb.New(value.Captured),
	}.Build()
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

func availableSeatsProto(snapshot *seatmappb.Snapshot, live []domain.LiveSeat) []*seatmappb.Seat {
	if snapshot == nil || snapshot.GetLayout() == nil {
		return nil
	}
	available := make(map[string]struct{}, len(live))
	for _, value := range live {
		if value.Available {
			available[value.Label] = struct{}{}
		}
	}
	result := make([]*seatmappb.Seat, 0, len(available))
	for _, seat := range snapshot.GetLayout().GetSeats() {
		if seat == nil {
			continue
		}
		if _, ok := available[seat.GetLabel()]; ok {
			result = append(result, seat)
		}
	}
	return result
}
