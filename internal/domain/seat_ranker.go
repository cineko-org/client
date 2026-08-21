package domain

import (
	"errors"
	"math"
	"slices"
	"sort"
)

type SeatGroup struct {
	Seats []Seat
	Score float64
}

type SeatRanker struct{}

func (SeatRanker) Rank(
	seatMap SeatMap,
	liveSeats []LiveSeat,
	count int,
	preference SeatPreference,
) ([]SeatGroup, error) {
	if count < 1 {
		return nil, errors.New("seat count must be positive")
	}
	if !preference.Adjacency.Valid() {
		return nil, errors.New("seat adjacency policy must require adjacent seats")
	}
	available := make(map[string]bool, len(liveSeats))
	for _, live := range liveSeats {
		available[live.Label] = live.Available
	}
	candidates := make(map[string]struct{}, len(preference.CandidateSeats))
	for _, label := range preference.CandidateSeats {
		candidates[label] = struct{}{}
	}
	rows := make(map[string][]Seat)
	for _, seat := range seatMap.Seats {
		_, candidate := candidates[seat.Label]
		if available[seat.Label] && (len(candidates) == 0 || candidate) {
			rows[seat.Row] = append(rows[seat.Row], seat)
		}
	}

	groups := consecutiveGroups(rows, count, preference)
	sortGroups(groups)
	return groups, nil
}

func consecutiveGroups(rows map[string][]Seat, count int, preference SeatPreference) []SeatGroup {
	var groups []SeatGroup
	for _, row := range rows {
		sort.Slice(row, func(i, j int) bool { return row[i].Number < row[j].Number })
		for start := 0; start+count <= len(row); start++ {
			candidate := row[start : start+count]
			if !consecutive(candidate) {
				continue
			}
			groups = append(groups, SeatGroup{
				Seats: append([]Seat(nil), candidate...),
				Score: scoreGroup(candidate, preference),
			})
		}
	}
	return groups
}

func sortGroups(groups []SeatGroup) {
	sort.Slice(groups, func(i, j int) bool {
		if groups[i].Score == groups[j].Score {
			return groups[i].Seats[0].Label < groups[j].Seats[0].Label
		}
		return groups[i].Score > groups[j].Score
	})
}

func consecutive(seats []Seat) bool {
	for index := 1; index < len(seats); index++ {
		previous, current := seats[index-1], seats[index]
		if current.Number != previous.Number+1 || previous.RightAisle || current.LeftAisle {
			return false
		}
	}
	return true
}

func scoreGroup(seats []Seat, preference SeatPreference) float64 {
	score := 0.0
	for _, seat := range seats {
		score += scoreSeat(seat, preference)
	}
	return score / float64(len(seats))
}

func scoreSeat(seat Seat, preference SeatPreference) float64 {
	score := 100.0
	if candidateRank := slices.Index(preference.CandidateSeats, seat.Label); candidateRank >= 0 {
		score += 10_000 - float64(candidateRank*100)
	}
	if rowRank := slices.Index(preference.PreferredRows, seat.Row); rowRank >= 0 {
		score += 2_000 - float64(rowRank*100)
	}
	for _, zone := range preference.PreferredZones {
		if zone.Contains(seat.X, seat.Y) {
			score += float64(zone.Weight) * 10
		}
	}
	if typeRank := slices.Index(preference.PreferredTypes, seat.Type); typeRank >= 0 {
		score += 500 - float64(typeRank*25)
	}
	score -= math.Hypot(seat.X-0.5, seat.Y-0.55) * 100
	if preference.AvoidEdges && (seat.X < 0.08 || seat.X > 0.92) {
		score -= 500
	}
	return score
}
