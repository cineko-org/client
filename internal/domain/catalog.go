package domain

import (
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"
)

type SeatType string

const (
	SeatTypeStandard   SeatType = "standard"
	SeatTypeWheelchair SeatType = "wheelchair"
	SeatTypeCompanion  SeatType = "companion"
	SeatTypeCouple     SeatType = "couple"
	SeatTypeRecliner   SeatType = "recliner"
	SeatTypeMotion     SeatType = "motion"
	SeatTypePrime      SeatType = "prime"
	SeatTypePremium    SeatType = "premium"
	SeatTypeBed        SeatType = "bed"
	SeatTypeUnknown    SeatType = "unknown"
)

func (seatType SeatType) Valid() bool {
	switch seatType {
	case SeatTypeStandard, SeatTypeWheelchair, SeatTypeCompanion, SeatTypeCouple,
		SeatTypeRecliner, SeatTypeMotion, SeatTypePrime, SeatTypePremium, SeatTypeBed, SeatTypeUnknown:
		return true
	default:
		return false
	}
}

type Theater struct {
	ID         string
	ProviderID string
	Region     string
	Name       string
	SourceKey  string
	ObservedAt time.Time
}

func (theater Theater) Validate() error {
	if strings.TrimSpace(theater.ID) == "" || strings.TrimSpace(theater.ProviderID) == "" {
		return errors.New("theater id and provider id are required")
	}
	if strings.TrimSpace(theater.SourceKey) == "" {
		return errors.New("theater source key is required")
	}
	if strings.TrimSpace(theater.Region) == "" {
		return errors.New("theater region is required")
	}
	if strings.TrimSpace(theater.Name) == "" {
		return errors.New("theater name is required")
	}
	return nil
}

type Auditorium struct {
	ID             string
	TheaterID      string
	SourceKey      string
	Name           string
	ScreenTypes    []string
	Capacity       int
	SeatMapVersion string
	ObservedAt     time.Time
}

func (auditorium Auditorium) Validate() error {
	if auditorium.ID == "" || auditorium.TheaterID == "" || strings.TrimSpace(auditorium.SourceKey) == "" || strings.TrimSpace(auditorium.Name) == "" {
		return errors.New("auditorium id, theater id, source key, and name are required")
	}
	if auditorium.Capacity < 0 {
		return errors.New("auditorium capacity cannot be negative")
	}
	return nil
}

type Seat struct {
	ID                 string
	AuditoriumID       string
	Label              string
	Row                string
	Number             int
	X                  float64
	Y                  float64
	Type               SeatType
	ZoneName           string
	ZoneKind           string
	SaleFormCode       string
	SaleFormName       string
	LeftAisle          bool
	RightAisle         bool
	Features           []string
	SourceLabel        string
	SourceSeatKindCode string
	SourceSeatKindName string
	SourceClasses      []string
}

func (seat Seat) Validate() error {
	if seat.ID == "" || seat.AuditoriumID == "" || seat.Label == "" || seat.Row == "" {
		return errors.New("seat id, auditorium id, label, and row are required")
	}
	if seat.Number < 1 {
		return errors.New("seat number must be positive")
	}
	if seat.X < 0 || seat.X > 1 || seat.Y < 0 || seat.Y > 1 {
		return fmt.Errorf("seat %s position must be normalized to 0..1", seat.Label)
	}
	return nil
}

type SeatMap struct {
	AuditoriumID string
	Version      string
	Seats        []Seat
	Zones        []LayoutZone
	Blocks       []LayoutBlock
	Evidence     LayoutEvidence
	ObservedAt   time.Time
}

type LayoutZone struct {
	Code     string
	Name     string
	KindCode string
	KindName string
	MinX     float64
	MaxX     float64
	MinY     float64
	MaxY     float64
	Capacity int
}

type LayoutBlock struct {
	Code     string
	Name     string
	KindCode string
	KindName string
	MinX     float64
	MaxX     float64
	MinY     float64
	MaxY     float64
}

func (zone LayoutZone) Validate() error {
	if strings.TrimSpace(zone.Code) == "" || strings.TrimSpace(zone.Name) == "" {
		return errors.New("layout zone code and name are required")
	}
	if zone.Capacity < 0 || !normalizedBounds(zone.MinX, zone.MaxX, zone.MinY, zone.MaxY) {
		return fmt.Errorf("layout zone %s has invalid bounds or capacity", zone.Name)
	}
	return nil
}

func (block LayoutBlock) Validate() error {
	if strings.TrimSpace(block.Code) == "" || strings.TrimSpace(block.Name) == "" {
		return errors.New("layout block code and name are required")
	}
	if !normalizedBounds(block.MinX, block.MaxX, block.MinY, block.MaxY) {
		return fmt.Errorf("layout block %s has invalid bounds", block.Name)
	}
	return nil
}

func normalizedBounds(minX, maxX, minY, maxY float64) bool {
	return minX >= 0 && maxX <= 1 && minY >= 0 && maxY <= 1 && minX <= maxX && minY <= maxY
}

type LayoutEvidence struct {
	ScreenshotPath    string
	ScreenshotSHA256  string
	SnapshotSHA256    string
	SourceShowtimeID  string
	DOMSeatCount      int
	SnapshotSeatCount int
	CaptureTrigger    string
	CapturedAt        time.Time
}

func (seatMap SeatMap) Validate() error {
	if seatMap.AuditoriumID == "" || seatMap.Version == "" {
		return errors.New("seat map auditorium id and version are required")
	}
	if len(seatMap.Seats) == 0 {
		return errors.New("seat map must contain seats")
	}
	if err := seatMap.Evidence.Validate(len(seatMap.Seats)); err != nil {
		return err
	}
	if err := seatMap.validateSeats(); err != nil {
		return err
	}
	return seatMap.validateLayout()
}

func (evidence LayoutEvidence) Validate(seatCount int) error {
	required := []string{
		evidence.ScreenshotPath,
		evidence.ScreenshotSHA256,
		evidence.SnapshotSHA256,
		evidence.SourceShowtimeID,
		evidence.CaptureTrigger,
	}
	for _, value := range required {
		if strings.TrimSpace(value) == "" {
			return errors.New("seat map requires screenshot and seat snapshot evidence")
		}
	}
	if evidence.CapturedAt.IsZero() {
		return errors.New("seat map requires screenshot and seat snapshot evidence")
	}
	if evidence.DOMSeatCount != seatCount || evidence.SnapshotSeatCount != seatCount {
		return errors.New("seat map evidence counts must match persisted seats")
	}
	return nil
}

func (seatMap SeatMap) validateSeats() error {
	seen := make(map[string]struct{}, len(seatMap.Seats))
	for _, seat := range seatMap.Seats {
		if err := seat.Validate(); err != nil {
			return err
		}
		if seat.AuditoriumID != seatMap.AuditoriumID {
			return fmt.Errorf("seat %s belongs to another auditorium", seat.Label)
		}
		if _, exists := seen[seat.Label]; exists {
			return fmt.Errorf("duplicate seat label: %s", seat.Label)
		}
		seen[seat.Label] = struct{}{}
	}
	return nil
}

func (seatMap SeatMap) validateLayout() error {
	for _, zone := range seatMap.Zones {
		if err := zone.Validate(); err != nil {
			return err
		}
	}
	for _, block := range seatMap.Blocks {
		if err := block.Validate(); err != nil {
			return err
		}
	}
	return nil
}

func (seatMap SeatMap) SortedSeats() []Seat {
	seats := append([]Seat(nil), seatMap.Seats...)
	sort.Slice(seats, func(i, j int) bool {
		if seats[i].Row == seats[j].Row {
			return seats[i].Number < seats[j].Number
		}
		return seats[i].Row < seats[j].Row
	})
	return seats
}

type AuditoriumAnalysis struct {
	AuditoriumID string
	Capacity     int
	Rows         int
	SeatTypes    map[SeatType]int
	Zones        map[string]int
	SaleForms    map[string]int
	MinX         float64
	MaxX         float64
	MinY         float64
	MaxY         float64
}

func AnalyzeSeatMap(seatMap SeatMap) AuditoriumAnalysis {
	analysis := AuditoriumAnalysis{
		AuditoriumID: seatMap.AuditoriumID,
		Capacity:     len(seatMap.Seats),
		SeatTypes:    make(map[SeatType]int),
		Zones:        make(map[string]int),
		SaleForms:    make(map[string]int),
		MinX:         1,
		MinY:         1,
	}
	rows := make(map[string]struct{})
	for _, seat := range seatMap.Seats {
		rows[seat.Row] = struct{}{}
		analysis.SeatTypes[seat.Type]++
		analysis.Zones[seat.ZoneName]++
		analysis.SaleForms[seat.SaleFormName]++
		analysis.MinX = min(analysis.MinX, seat.X)
		analysis.MaxX = max(analysis.MaxX, seat.X)
		analysis.MinY = min(analysis.MinY, seat.Y)
		analysis.MaxY = max(analysis.MaxY, seat.Y)
	}
	analysis.Rows = len(rows)
	if len(seatMap.Seats) == 0 {
		analysis.MinX, analysis.MinY = 0, 0
	}
	return analysis
}
