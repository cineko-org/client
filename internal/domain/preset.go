package domain

import (
	"errors"
	"fmt"
	"slices"
	"strings"
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
	Name   string
	MinX   float64
	MaxX   float64
	MinY   float64
	MaxY   float64
	Weight int
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
	CandidateSeats []string
	PreferredRows  []string
	PreferredZones []SeatZone
	PreferredTypes []SeatType
	Adjacency      SeatAdjacency
	AvoidEdges     bool
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

func (preference SeatPreference) TypeRank(seatType SeatType) int {
	index := slices.Index(preference.PreferredTypes, seatType)
	if index < 0 {
		return len(preference.PreferredTypes) + 1
	}
	return index
}
