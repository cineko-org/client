package cgv

import (
	"testing"
	"time"

	"github.com/cineko-org/client/internal/domain"
)

func TestParseSeatSnapshotUsesSaleYNAndStatusForAvailability(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, time.August, 9, 20, 40, 0, 0, time.FixedZone("KST", 9*60*60))
	snapshot, err := parseSeatSnapshot([]byte(seatSnapshotFixture), "auditorium-1", now)
	if err != nil {
		t.Fatalf("parseSeatSnapshot() error = %v", err)
	}
	if len(snapshot.Seats) != 3 || len(snapshot.Live) != 3 {
		t.Fatalf("seat counts = %d/%d, want 3/3", len(snapshot.Seats), len(snapshot.Live))
	}

	available := liveSeatByLabel(snapshot.Live, "A19")
	if !available.Available {
		t.Fatal("A19 should be available when seatSaleYn=Y and seatStusCd=00")
	}
	held := liveSeatByLabel(snapshot.Live, "A22")
	if held.Available || held.StatusCode != "04" {
		t.Fatalf("A22 = %+v, want a held and unavailable seat", held)
	}
	sold := liveSeatByLabel(snapshot.Live, "B1")
	if sold.Available {
		t.Fatal("B1 should not be available")
	}
}

func TestParseSeatSnapshotPreservesLayoutSemantics(t *testing.T) {
	t.Parallel()

	snapshot, err := parseSeatSnapshot([]byte(seatSnapshotFixture), "auditorium-1", time.Now())
	if err != nil {
		t.Fatalf("parseSeatSnapshot() error = %v", err)
	}
	seat := seatByLabel(snapshot.Seats, "A19")
	if seat.Type != domain.SeatTypeWheelchair {
		t.Fatalf("A19 type = %q, want wheelchair", seat.Type)
	}
	if seat.ZoneName != "Light존" || seat.SaleFormName != "이동식" || !seat.RightAisle {
		t.Fatalf("A19 layout metadata = %+v", seat)
	}
	if seat.X <= 0.40 || seat.X >= 0.45 || seat.Y <= 0 || seat.Y >= 0.1 {
		t.Fatalf("A19 normalized position = %.4f,%.4f", seat.X, seat.Y)
	}
	if len(snapshot.Zones) != 2 || snapshot.Zones[1].Capacity != 2 {
		t.Fatalf("zones = %+v", snapshot.Zones)
	}
	if len(snapshot.Blocks) != 1 || snapshot.Blocks[0].KindName != "입구" {
		t.Fatalf("blocks = %+v", snapshot.Blocks)
	}
	if snapshot.Hash == "" {
		t.Fatal("snapshot hash should be recorded")
	}
}

func TestIntersectAvailabilityRequiresSnapshotAndEnabledButton(t *testing.T) {
	t.Parallel()

	live := []domain.LiveSeat{
		{Label: "A19", Available: true},
		{Label: "A22", Available: true},
	}
	raw := []rawSeat{
		{Label: "A19", Disabled: false},
		{Label: "A22", Disabled: true},
	}
	result := intersectAvailability(live, raw)
	if !result[0].Available || result[1].Available {
		t.Fatalf("intersection = %+v", result)
	}
	if result[0].Source != "cgv-seat-snapshot+enabled-button" {
		t.Fatalf("source = %q", result[0].Source)
	}
}

func liveSeatByLabel(seats []domain.LiveSeat, label string) domain.LiveSeat {
	for _, seat := range seats {
		if seat.Label == label {
			return seat
		}
	}
	return domain.LiveSeat{}
}

func seatByLabel(seats []domain.Seat, label string) domain.Seat {
	for _, seat := range seats {
		if seat.Label == label {
			return seat
		}
	}
	return domain.Seat{}
}

const seatSnapshotFixture = `{
  "statusCode": 0,
  "resultMsg": "Success",
  "data": {
    "items": [{
      "sbord": {"xcoordStartVal":"0000","ycoordStartVal":"0000","xcoordEndVal":"0101","ycoordEndVal":"0035","stcnt":3},
      "salfrms": [
        {"seatSalfrmCd":"01","seatSalfrmNm":"일반"},
        {"seatSalfrmCd":"04","seatSalfrmNm":"이동식"}
      ],
      "szones": [
        {"szoneNo":"01001","szoneNm":"일반존","szoneKindCd":"01","szoneKindNm":"일반","xcoordStartVal":"0001","ycoordStartVal":"0005","xcoordEndVal":"0099","ycoordEndVal":"0033","maxNopsn":"1"},
        {"szoneNo":"02001","szoneNm":"Light존","szoneKindCd":"02","szoneKindNm":"Light존","xcoordStartVal":"0007","ycoordStartVal":"0001","xcoordEndVal":"0093","ycoordEndVal":"0005","maxNopsn":"2"}
      ],
      "sblcks": [
        {"sblckNo":"01001","sblckNm":"입구","sblckKindCd":"01","sblckKindNm":"입구","xcoordStartVal":"0067","ycoordStartVal":"0000","xcoordEndVal":"0068","ycoordEndVal":"0001"}
      ],
      "seats": [
        {"seatLocNo":"1","seatRowNm":"A","seatNo":"19","stkndCd":"01","stkndNm":"일반석","szoneNm":"Light존","szoneKindNm":"Light존","seatSalfrmCd":"04","seatStusCd":"00","seatStusNm":"미정","seatSaleYn":"Y","xcoordStartVal":"0041","ycoordStartVal":"0001","xcoordEndVal":"0043","ycoordEndVal":"0003","leftPwayYn":"N","rghtPwayYn":"Y"},
        {"seatLocNo":"2","seatRowNm":"A","seatNo":"22","stkndCd":"01","stkndNm":"일반석","szoneNm":"Light존","szoneKindNm":"Light존","seatSalfrmCd":"04","seatStusCd":"04","seatStusNm":"진행","seatSaleYn":"N","xcoordStartVal":"0047","ycoordStartVal":"0001","xcoordEndVal":"0049","ycoordEndVal":"0003","leftPwayYn":"Y","rghtPwayYn":"N"},
        {"seatLocNo":"3","seatRowNm":"B","seatNo":"1","stkndCd":"01","stkndNm":"일반석","szoneNm":"일반존","szoneKindNm":"일반","seatSalfrmCd":"01","seatStusCd":"01","seatStusNm":"판매","seatSaleYn":"N","xcoordStartVal":"0001","ycoordStartVal":"0005","xcoordEndVal":"0003","ycoordEndVal":"0007","leftPwayYn":"Y","rghtPwayYn":"N"}
      ]
    }]
  }
}`
