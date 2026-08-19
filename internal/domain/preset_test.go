package domain

import (
	"encoding/json"
	"testing"
)

func TestPresetValidateRejectsInvalidPreferenceData(t *testing.T) {
	t.Parallel()

	preset := Preset{
		ID: "preset-1", UserID: "user-1", Name: "중앙", TheaterID: "theater-1",
		AuditoriumID: "auditorium-1", SeatCount: 1,
		SeatPreference: SeatPreference{CandidateSeats: []string{"H10"}, Adjacency: SeatAdjacencyRequired, PreferredZones: []SeatZone{{
			Name: "bad", MinX: .8, MaxX: .2, MinY: 0, MaxY: 1,
		}}},
	}
	if err := preset.Validate(nil); err == nil {
		t.Fatal("Validate() accepted reversed zone bounds")
	}

	preset.SeatPreference.PreferredZones = nil
	preset.SeatPreference.PreferredTypes = []SeatType{"made-up"}
	if err := preset.Validate(nil); err == nil {
		t.Fatal("Validate() accepted an unknown seat type")
	}
}

func TestSeatPreferenceMigratesLegacyExplicitSeatsAndTogetherFlag(t *testing.T) {
	t.Parallel()
	var preference SeatPreference
	if err := json.Unmarshal([]byte(`{"explicitSeats":["H10","H11"],"together":false}`), &preference); err != nil {
		t.Fatalf("Unmarshal() error = %v", err)
	}
	if len(preference.CandidateSeats) != 2 || preference.CandidateSeats[0] != "H10" ||
		preference.Adjacency != SeatAdjacencyRequired {
		t.Fatalf("legacy preference = %+v", preference)
	}
}

func TestSeatPreferenceMigratesLegacyAdjacencyToRequired(t *testing.T) {
	t.Parallel()
	for _, adjacency := range []string{"preferred", "none"} {
		var preference SeatPreference
		if err := json.Unmarshal([]byte(`{"candidateSeats":["H10"],"adjacency":"`+adjacency+`"}`), &preference); err != nil {
			t.Fatal(err)
		}
		if preference.Adjacency != SeatAdjacencyRequired {
			t.Fatalf("adjacency %q migrated to %q", adjacency, preference.Adjacency)
		}
	}
}

func TestSeatPreferenceRejectsInvalidJSON(t *testing.T) {
	t.Parallel()
	var preference SeatPreference
	if err := json.Unmarshal([]byte(`{"candidateSeats":3}`), &preference); err == nil {
		t.Fatal("Unmarshal() accepted invalid JSON")
	}
}

func TestPresetValidateRequiresAUsableCandidateGroup(t *testing.T) {
	t.Parallel()
	seatMap := SeatMap{AuditoriumID: "auditorium-1", Seats: []Seat{
		{Label: "H10", Row: "H", Number: 10},
		{Label: "H11", Row: "H", Number: 11},
		{Label: "H13", Row: "H", Number: 13},
	}}
	preset := Preset{
		ID: "preset", UserID: "user", Name: "두 자리", TheaterID: "theater",
		AuditoriumID: seatMap.AuditoriumID, SeatCount: 2,
		SeatPreference: SeatPreference{
			CandidateSeats: []string{"H10", "H13"}, Adjacency: SeatAdjacencyRequired,
		},
	}
	if err := preset.Validate(&seatMap); err == nil {
		t.Fatal("Validate() accepted candidates without a consecutive pair")
	}
	preset.SeatPreference.CandidateSeats = []string{"H10", "H11"}
	if err := preset.Validate(&seatMap); err != nil {
		t.Fatalf("Validate() rejected consecutive candidates: %v", err)
	}
	preset.SeatPreference.CandidateSeats = []string{"H10"}
	if err := preset.Validate(&seatMap); err == nil {
		t.Fatal("Validate() accepted fewer candidates than requested seats")
	}
}

func TestPresetValidateAllowsAutomaticLiveSelection(t *testing.T) {
	t.Parallel()
	preset := Preset{
		ID: "preset", UserID: "user", Name: "한 자리", TheaterID: "theater",
		AuditoriumID: "auditorium", SeatCount: 1,
		SeatPreference: SeatPreference{Adjacency: SeatAdjacencyRequired},
	}
	if err := preset.Validate(nil); err != nil {
		t.Fatalf("Validate() rejected automatic live selection: %v", err)
	}
	seatMap := SeatMap{AuditoriumID: preset.AuditoriumID}
	if err := preset.Validate(&seatMap); err != nil {
		t.Fatalf("Validate() rejected automatic live selection with a known layout: %v", err)
	}
	preset.SeatPreference.CandidateSeats = []string{""}
	if err := preset.Validate(nil); err == nil {
		t.Fatal("Validate() accepted an empty candidate label")
	}
	preset.SeatPreference.CandidateSeats = []string{"H10"}
	preset.SeatPreference.Adjacency = SeatAdjacencyPreferred
	if err := preset.Validate(nil); err == nil {
		t.Fatal("Validate() accepted best-effort adjacency")
	}
}
