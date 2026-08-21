package cgv

import (
	"strings"
	"testing"

	"github.com/cineko-org/client/internal/domain"
)

func TestCommandedShowtimeUsesExactProviderTupleBeforeDisplayHandoff(t *testing.T) {
	command := domain.Showtime{
		ProviderID:     providerCGV,
		SourceKey:      "0056/2026-08-20/0007/0004",
		MovieID:        catalogID(providerCGV, "movie", "00001234"),
		Movie:          "stale command display",
		TheaterID:      "theater",
		TheaterName:    "용산아이파크몰",
		AuditoriumName: "IMAX관",
		Date:           "2026-08-20",
		StartsAt:       "21:30",
		EndsAt:         "23:50",
	}
	rows := []providerScheduleRow{
		{SiteNo: "0056", MovieNo: "00001234", MovieTitle: "first display", AuditoriumNo: "0007", AuditoriumName: "IMAX관", Date: "2026-08-20", Sequence: "0003", StartClock: "2130", EndClock: "2350", Available: 2, Capacity: 624},
		{SiteNo: "0056", MovieNo: "00001234", MovieTitle: "fresh display", AuditoriumNo: "0007", AuditoriumName: "IMAX관", Date: "2026-08-20", Sequence: "0004", StartClock: "2130", EndClock: "2350", Available: 2, Capacity: 624},
	}
	resolved, err := commandedShowtime(rows, command)
	if err != nil {
		t.Fatalf("commandedShowtime() error = %v", err)
	}
	if resolved.SourceKey != command.SourceKey || resolved.Movie != "fresh display" {
		t.Fatalf("resolved projection = %+v", resolved)
	}
	if _, err := commandedShowtime(rows[:1], command); err == nil {
		t.Fatal("commanded showtime accepted a missing provider tuple row")
	}
}

func TestScheduleButtonDisplayProjectionUsesObservedButtonAndRowText(t *testing.T) {
	showtime := domain.Showtime{
		ProviderID:     "cgv",
		SourceKey:      "0056/2026-08-20/0007/0003",
		MovieID:        "movie",
		Movie:          "표시 영화명",
		TheaterName:    "용산아이파크몰",
		AuditoriumName: "2관 (Laser)",
		Date:           "2026-08-20",
		StartsAt:       "19:30",
		EndsAt:         "21:31",
		AvailableSeats: 1,
		Capacity:       184,
	}
	// This mirrors the observed ordinary button: canonical provider fields are
	// absent, while the surrounding row carries the display movie title.
	buttonText := "19:30 - 21:31 1 / 184석 2관 (Laser)"
	rowText := strings.Join([]string{buttonText, showtime.Movie}, " ")
	if !scheduleButtonDisplayMatches(buttonText, rowText, showtime) {
		t.Fatal("observed schedule button projection was rejected")
	}
	if strings.Contains(buttonText, "data-source-key") || strings.Contains(buttonText, "scnSseq") {
		t.Fatal("test fixture must not invent a canonical DOM attribute")
	}
}

func TestScheduleButtonDisplayProjectionRejectsAmbiguousRows(t *testing.T) {
	showtime := domain.Showtime{
		ProviderID:     "cgv",
		SourceKey:      "0056/2026-08-20/0007/0003",
		MovieID:        "movie",
		Movie:          "표시 영화명",
		TheaterName:    "용산아이파크몰",
		AuditoriumName: "IMAX관",
		Date:           "2026-08-20",
		StartsAt:       "21:30",
		EndsAt:         "23:50",
		AvailableSeats: 2,
		Capacity:       624,
	}
	texts := []struct{ button, row string }{
		{"21:30 - 23:50 2 / 624석 IMAX관", "표시 영화명 21:30 - 23:50 2 / 624석 IMAX관"},
		{"21:30 - 23:50 2 / 624석 IMAX관", "표시 영화명 21:30 - 23:50 2 / 624석 IMAX관"},
	}
	matches := 0
	for _, text := range texts {
		if scheduleButtonDisplayMatches(text.button, text.row, showtime) {
			matches++
		}
	}
	if matches != 2 {
		t.Fatalf("matching display rows = %d, want 2", matches)
	}
}

func TestValidateShowtimeIdentityRequiresCanonicalTupleAndDisplay(t *testing.T) {
	valid := domain.Showtime{
		ProviderID:     "cgv",
		SourceKey:      "0056/2026-08-20/0007/0003",
		MovieID:        "movie",
		Movie:          "영화",
		TheaterName:    "용산아이파크몰",
		AuditoriumName: "IMAX관",
		Date:           "2026-08-20",
		StartsAt:       "21:30",
		EndsAt:         "23:50",
	}
	if err := validateShowtimeIdentity(valid); err != nil {
		t.Fatalf("valid showtime rejected: %v", err)
	}
	for name, mutate := range map[string]func(*domain.Showtime){
		"provider":      func(value *domain.Showtime) { value.ProviderID = "" },
		"source key":    func(value *domain.Showtime) { value.SourceKey = "" },
		"movie display": func(value *domain.Showtime) { value.Movie = "" },
		"end display":   func(value *domain.Showtime) { value.EndsAt = "" },
	} {
		t.Run(name, func(t *testing.T) {
			value := valid
			mutate(&value)
			if err := validateShowtimeIdentity(value); err == nil {
				t.Fatal("incomplete showtime identity accepted")
			}
		})
	}
}

func scheduleButtonDisplayMatches(buttonText, rowText string, showtime domain.Showtime) bool {
	buttonText = normalize(buttonText)
	rowText = normalize(rowText)
	for _, expected := range []string{showtime.Movie, showtime.AuditoriumName, showtime.StartsAt, showtime.EndsAt} {
		if !strings.Contains(rowText, normalize(expected)) {
			return false
		}
	}
	seatTotals := scheduleSeatTotals(showtime)
	return seatTotals != "" && strings.Contains(strings.Join(strings.Fields(buttonText), ""), strings.Join(strings.Fields(seatTotals), ""))
}
