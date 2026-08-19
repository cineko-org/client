package domain

import (
	"slices"
	"testing"
)

func TestSeatRankerPrefersExplicitContiguousGroup(t *testing.T) {
	t.Parallel()

	seatMap := SeatMap{Seats: []Seat{
		{Label: "H9", Row: "H", Number: 9, X: .42, Y: .55, Type: SeatTypeStandard},
		{Label: "H10", Row: "H", Number: 10, X: .48, Y: .55, Type: SeatTypeStandard},
		{Label: "H11", Row: "H", Number: 11, X: .52, Y: .55, Type: SeatTypeStandard},
		{Label: "H12", Row: "H", Number: 12, X: .58, Y: .55, Type: SeatTypeStandard},
	}}
	live := []LiveSeat{
		{Label: "H9", Available: true}, {Label: "H10", Available: true},
		{Label: "H11", Available: true}, {Label: "H12", Available: true},
	}
	groups, err := (SeatRanker{}).Rank(seatMap, live, 2, SeatPreference{
		CandidateSeats: []string{"H11", "H12"}, Adjacency: SeatAdjacencyRequired,
	})
	if err != nil {
		t.Fatalf("Rank() error = %v", err)
	}
	if groups[0].Seats[0].Label != "H11" || groups[0].Seats[1].Label != "H12" {
		t.Fatalf("best group = %+v", groups[0].Seats)
	}
}

func TestSeatRankerRejectsGapForTogetherPreference(t *testing.T) {
	t.Parallel()

	seatMap := SeatMap{Seats: []Seat{
		{Label: "A1", Row: "A", Number: 1, X: .4, Y: .2},
		{Label: "A3", Row: "A", Number: 3, X: .6, Y: .2},
	}}
	live := []LiveSeat{{Label: "A1", Available: true}, {Label: "A3", Available: true}}
	groups, err := (SeatRanker{}).Rank(seatMap, live, 2, SeatPreference{
		CandidateSeats: []string{"A1", "A3"}, Adjacency: SeatAdjacencyRequired,
	})
	if err != nil {
		t.Fatalf("Rank() error = %v", err)
	}
	if len(groups) != 0 {
		t.Fatalf("groups = %+v, want none", groups)
	}
}

func TestSeatRankerDoesNotJoinSeatsAcrossAisle(t *testing.T) {
	t.Parallel()

	seatMap := SeatMap{Seats: []Seat{
		{Label: "J10", Row: "J", Number: 10, X: .45, Y: .6, RightAisle: true},
		{Label: "J11", Row: "J", Number: 11, X: .55, Y: .6, LeftAisle: true},
	}}
	live := []LiveSeat{{Label: "J10", Available: true}, {Label: "J11", Available: true}}
	groups, err := (SeatRanker{}).Rank(seatMap, live, 2, SeatPreference{
		CandidateSeats: []string{"J10", "J11"}, Adjacency: SeatAdjacencyRequired,
	})
	if err != nil {
		t.Fatalf("Rank() error = %v", err)
	}
	if len(groups) != 0 {
		t.Fatalf("groups = %+v, want none across an aisle", groups)
	}
}

func TestSeatRankerRestrictsSelectionToCandidateSeats(t *testing.T) {
	t.Parallel()
	seatMap := SeatMap{Seats: []Seat{
		{Label: "H10", Row: "H", Number: 10},
		{Label: "H11", Row: "H", Number: 11},
	}}
	live := []LiveSeat{{Label: "H10", Available: false}, {Label: "H11", Available: true}}
	groups, err := (SeatRanker{}).Rank(seatMap, live, 1, SeatPreference{
		CandidateSeats: []string{"H10"}, Adjacency: SeatAdjacencyRequired,
	})
	if err != nil {
		t.Fatalf("Rank() error = %v", err)
	}
	if len(groups) != 0 {
		t.Fatalf("groups = %+v, want no fallback outside candidate seats", groups)
	}
}

func TestSeatRankerNeverFallsBackToSplitCandidates(t *testing.T) {
	t.Parallel()
	seatMap := SeatMap{Seats: []Seat{
		{Label: "H10", Row: "H", Number: 10, X: .45},
		{Label: "H11", Row: "H", Number: 11, X: .5},
		{Label: "H12", Row: "H", Number: 12, X: .55},
	}}
	preference := SeatPreference{
		CandidateSeats: []string{"H10", "H12", "H11"}, Adjacency: SeatAdjacencyRequired,
	}
	allAvailable := []LiveSeat{
		{Label: "H10", Available: true}, {Label: "H11", Available: true}, {Label: "H12", Available: true},
	}
	groups, err := (SeatRanker{}).Rank(seatMap, allAvailable, 2, preference)
	if err != nil || len(groups) == 0 || !consecutive(groups[0].Seats) {
		t.Fatalf("preferred consecutive groups = %+v, %v", groups, err)
	}
	separatedAvailable := []LiveSeat{{Label: "H10", Available: true}, {Label: "H12", Available: true}}
	groups, err = (SeatRanker{}).Rank(seatMap, separatedAvailable, 2, preference)
	if err != nil || len(groups) != 0 {
		t.Fatalf("split fallback groups = %+v, %v", groups, err)
	}
}

func TestSeatRankerUsesAllLiveSeatsWithoutCandidatesAndRejectsSplitPolicies(t *testing.T) {
	t.Parallel()
	seatMap := SeatMap{Seats: []Seat{{Label: "H10", Row: "H", Number: 10}}}
	groups, err := (SeatRanker{}).Rank(
		seatMap,
		[]LiveSeat{{Label: "H10", Available: true}},
		1,
		SeatPreference{Adjacency: SeatAdjacencyRequired},
	)
	if err != nil || len(groups) != 1 || groups[0].Seats[0].Label != "H10" {
		t.Fatalf("Rank() without candidates = %+v, %v", groups, err)
	}
	if _, err := (SeatRanker{}).Rank(SeatMap{}, nil, 1, SeatPreference{
		CandidateSeats: []string{"H10"}, Adjacency: SeatAdjacencyNone,
	}); err == nil {
		t.Fatal("Rank() accepted split-seat policy")
	}
}

func TestSortGroupsBreaksEqualScoresBySeatLabel(t *testing.T) {
	t.Parallel()

	groups := []SeatGroup{
		{Seats: []Seat{{Label: "B2"}}, Score: 100},
		{Seats: []Seat{{Label: "A1"}}, Score: 100},
		{Seats: []Seat{{Label: "C3"}}, Score: 200},
	}

	sortGroups(groups)

	got := []string{
		groups[0].Seats[0].Label,
		groups[1].Seats[0].Label,
		groups[2].Seats[0].Label,
	}
	want := []string{"C3", "A1", "B2"}
	if !slices.Equal(got, want) {
		t.Fatalf("sorted labels = %v, want %v", got, want)
	}
}
