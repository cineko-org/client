package domain

import (
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"sort"
	"strings"
	"time"
)

type SeatAdjacency string

const (
	SeatAdjacencyRequired  SeatAdjacency = "required"
	SeatAdjacencyPreferred SeatAdjacency = "preferred"
	SeatAdjacencyNone      SeatAdjacency = "none"
)

func (adjacency SeatAdjacency) Valid() bool {
	return adjacency == SeatAdjacencyRequired
}

type SeatZone struct {
	Name   string  `json:"name"`
	MinX   float64 `json:"minX"`
	MaxX   float64 `json:"maxX"`
	MinY   float64 `json:"minY"`
	MaxY   float64 `json:"maxY"`
	Weight int     `json:"weight"`
}

func (zone SeatZone) Contains(x, y float64) bool {
	return x >= zone.MinX && x <= zone.MaxX && y >= zone.MinY && y <= zone.MaxY
}

func (zone SeatZone) Validate() error {
	if strings.TrimSpace(zone.Name) == "" {
		return errors.New("seat preference zone name is required")
	}
	if zone.MinX < 0 || zone.MaxX > 1 || zone.MinY < 0 || zone.MaxY > 1 ||
		zone.MinX > zone.MaxX || zone.MinY > zone.MaxY {
		return fmt.Errorf("seat preference zone %s bounds must be ordered within 0..1", zone.Name)
	}
	return nil
}

type SeatPreference struct {
	CandidateSeats []string      `json:"candidateSeats"`
	PreferredRows  []string      `json:"preferredRows"`
	PreferredZones []SeatZone    `json:"preferredZones"`
	PreferredTypes []SeatType    `json:"preferredTypes"`
	Adjacency      SeatAdjacency `json:"adjacency"`
	AvoidEdges     bool          `json:"avoidEdges"`
}

func (preference *SeatPreference) UnmarshalJSON(data []byte) error {
	type current SeatPreference
	var wire struct {
		current
		ExplicitSeats []string `json:"explicitSeats"`
		Together      *bool    `json:"together"`
	}
	if err := json.Unmarshal(data, &wire); err != nil {
		return err
	}
	*preference = SeatPreference(wire.current)
	if len(preference.CandidateSeats) == 0 && len(wire.ExplicitSeats) > 0 {
		preference.CandidateSeats = append([]string(nil), wire.ExplicitSeats...)
	}
	if preference.Adjacency == "" || preference.Adjacency == SeatAdjacencyPreferred ||
		preference.Adjacency == SeatAdjacencyNone {
		preference.Adjacency = SeatAdjacencyRequired
	}
	return nil
}

type Preset struct {
	Revision       int64          `json:"revision,omitempty"`
	ID             string         `json:"id"`
	UserID         string         `json:"userId"`
	Name           string         `json:"name"`
	TheaterID      string         `json:"theaterId"`
	AuditoriumID   string         `json:"auditoriumId"`
	SeatCount      int            `json:"seatCount"`
	SeatPreference SeatPreference `json:"seatPreference"`
	CreatedAt      time.Time      `json:"createdAt"`
	UpdatedAt      time.Time      `json:"updatedAt"`
}

func (preset Preset) Validate(seatMap *SeatMap) error {
	if err := preset.validateIdentity(); err != nil {
		return err
	}
	if err := preset.SeatPreference.Validate(); err != nil {
		return err
	}
	if seatMap == nil {
		return nil
	}
	return preset.validateSeatMap(*seatMap)
}

func (preset Preset) validateIdentity() error {
	if preset.ID == "" || preset.UserID == "" {
		return errors.New("preset id and user id are required")
	}
	if strings.TrimSpace(preset.Name) == "" {
		return errors.New("preset name is required")
	}
	if preset.TheaterID == "" || preset.AuditoriumID == "" {
		return errors.New("preset theater and auditorium are required")
	}
	if preset.SeatCount < 1 || preset.SeatCount > 8 {
		return errors.New("preset seat count must be between 1 and 8")
	}
	if len(preset.SeatPreference.CandidateSeats) > 0 &&
		len(preset.SeatPreference.CandidateSeats) < preset.SeatCount {
		return errors.New("preset candidate seats must cover the requested seat count")
	}
	return nil
}

func (preference SeatPreference) Validate() error {
	if !preference.Adjacency.Valid() {
		return fmt.Errorf("unknown seat adjacency policy %q", preference.Adjacency)
	}
	seenCandidates := make(map[string]struct{}, len(preference.CandidateSeats))
	for _, label := range preference.CandidateSeats {
		if strings.TrimSpace(label) == "" {
			return errors.New("candidate seat label is required")
		}
		if _, duplicate := seenCandidates[label]; duplicate {
			return fmt.Errorf("duplicate candidate seat %s", label)
		}
		seenCandidates[label] = struct{}{}
	}
	for _, zone := range preference.PreferredZones {
		if err := zone.Validate(); err != nil {
			return err
		}
	}
	for _, seatType := range preference.PreferredTypes {
		if !seatType.Valid() {
			return fmt.Errorf("unknown preferred seat type %q", seatType)
		}
	}
	return nil
}

func (preset Preset) validateSeatMap(seatMap SeatMap) error {
	if seatMap.AuditoriumID != preset.AuditoriumID {
		return errors.New("preset auditorium does not match the seat map")
	}
	if len(preset.SeatPreference.CandidateSeats) == 0 {
		return nil
	}
	seatsByLabel := make(map[string]Seat, len(seatMap.Seats))
	for _, seat := range seatMap.Seats {
		seatsByLabel[seat.Label] = seat
	}
	candidates := make([]Seat, 0, len(preset.SeatPreference.CandidateSeats))
	for _, label := range preset.SeatPreference.CandidateSeats {
		seat, exists := seatsByLabel[label]
		if !exists {
			return fmt.Errorf("candidate seat %s does not exist in auditorium", label)
		}
		candidates = append(candidates, seat)
	}
	if preset.SeatCount > 1 &&
		!hasConsecutiveCandidateGroup(candidates, preset.SeatCount) {
		return errors.New("candidate seats do not contain the required consecutive group")
	}
	return nil
}

func hasConsecutiveCandidateGroup(seats []Seat, count int) bool {
	rows := make(map[string][]Seat)
	for _, seat := range seats {
		rows[seat.Row] = append(rows[seat.Row], seat)
	}
	for _, row := range rows {
		sort.Slice(row, func(i, j int) bool { return row[i].Number < row[j].Number })
		for start := 0; start+count <= len(row); start++ {
			if consecutive(row[start : start+count]) {
				return true
			}
		}
	}
	return false
}

func (preset Preset) Owns(userID string) bool {
	return preset.UserID == userID
}

func (preference SeatPreference) TypeRank(seatType SeatType) int {
	index := slices.Index(preference.PreferredTypes, seatType)
	if index < 0 {
		return len(preference.PreferredTypes) + 1
	}
	return index
}
