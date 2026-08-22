package domain

import "testing"

func TestSeatPreferenceValidateCoversEveryInvariant(t *testing.T) {
	t.Parallel()

	valid := SeatPreference{
		CandidateSeats: []string{"A1"},
		PreferredZones: []SeatZone{{Name: "center", MinX: 0, MaxX: 1, MinY: 0, MaxY: 1}},
		PreferredTypes: []SeatType{SeatTypeStandard},
		Adjacency:      SeatAdjacencyRequired,
	}
	if err := valid.Validate(); err != nil {
		t.Fatalf("valid preference rejected: %v", err)
	}

	tests := []struct {
		name   string
		mutate func(*SeatPreference)
	}{
		{name: "adjacency", mutate: func(value *SeatPreference) { value.Adjacency = SeatAdjacencyPreferred }},
		{name: "blank candidate", mutate: func(value *SeatPreference) { value.CandidateSeats = []string{" "} }},
		{name: "duplicate candidate", mutate: func(value *SeatPreference) { value.CandidateSeats = []string{"A1", "A1"} }},
		{name: "invalid zone", mutate: func(value *SeatPreference) { value.PreferredZones[0].Name = "" }},
		{name: "invalid seat type", mutate: func(value *SeatPreference) { value.PreferredTypes = []SeatType{"invalid"} }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			candidate := valid
			candidate.CandidateSeats = append([]string(nil), valid.CandidateSeats...)
			candidate.PreferredZones = append([]SeatZone(nil), valid.PreferredZones...)
			candidate.PreferredTypes = append([]SeatType(nil), valid.PreferredTypes...)
			test.mutate(&candidate)
			if candidate.Validate() == nil {
				t.Fatalf("invalid preference accepted: %+v", candidate)
			}
		})
	}
}
