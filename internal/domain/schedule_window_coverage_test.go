package domain

import (
	"testing"
	"time"
)

func TestScheduleWindowRejectsInvalidScheduleStarts(t *testing.T) {
	t.Parallel()

	window := ScheduleWindow{}
	if window.Matches("not-a-date", "12:00") {
		t.Fatal("invalid schedule date matched")
	}
	if window.Matches("2026-08-15", "not-a-clock") {
		t.Fatal("invalid schedule clock matched")
	}
	if (ScheduleWindow{}).MatchesShowtime(Showtime{Date: "2026-08-15", StartsAt: "not-a-clock"}) {
		t.Fatal("invalid showtime clock matched")
	}
}

func TestScheduleWindowMatchesOpenBoundsAndNormalRange(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		window ScheduleWindow
		start  string
		want   bool
	}{
		{name: "latest only before bound", window: ScheduleWindow{Latest: "06:00"}, start: "05:59", want: true},
		{name: "latest only at bound", window: ScheduleWindow{Latest: "06:00"}, start: "06:00", want: false},
		{name: "earliest only at bound", window: ScheduleWindow{Earliest: "18:00"}, start: "18:00", want: true},
		{name: "earliest only before bound", window: ScheduleWindow{Earliest: "18:00"}, start: "17:59", want: false},
		{name: "normal range inside", window: ScheduleWindow{Earliest: "18:00", Latest: "21:00"}, start: "19:00", want: true},
		{name: "equal bounds are empty", window: ScheduleWindow{Earliest: "18:00", Latest: "18:00"}, start: "18:00", want: false},
		{name: "invalid latest only", window: ScheduleWindow{Latest: "invalid"}, start: "05:00", want: false},
		{name: "invalid earliest only", window: ScheduleWindow{Earliest: "invalid"}, start: "18:00", want: false},
		{name: "invalid earliest in range", window: ScheduleWindow{Earliest: "invalid", Latest: "21:00"}, start: "19:00", want: false},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := test.window.Matches("2026-08-15", test.start); got != test.want {
				t.Fatalf("Matches(%q) = %t, want %t", test.start, got, test.want)
			}
		})
	}
}

func TestScheduleWindowMatchesShowtimeFallsBackToProviderDate(t *testing.T) {
	t.Parallel()

	showtime := Showtime{Date: "2026-08-15", StartsAt: "01:00"}
	if !(ScheduleWindow{Earliest: "00:00", Latest: "06:00"}).MatchesShowtime(showtime) {
		t.Fatal("showtime provider date fallback was rejected")
	}
	if (ScheduleWindow{}).MatchesShowtime(Showtime{Date: "not-a-date", StartsAt: "01:00"}) {
		t.Fatal("invalid showtime date matched")
	}
}

func TestScheduleWindowClockParsers(t *testing.T) {
	t.Parallel()

	if got, err := parseClockMinutes("25:30"); err != nil || got != 90 {
		t.Fatalf("parseClockMinutes(25:30) = %d, %v; want 90", got, err)
	}
	if _, err := parseClockMinutes("not-a-clock"); err == nil {
		t.Fatal("parseClockMinutes accepted invalid clock")
	}

	start, err := parseScheduleStart("2026-08-14", "25:30")
	if err != nil {
		t.Fatalf("parseScheduleStart() error: %v", err)
	}
	want := time.Date(2026, time.August, 15, 1, 30, 0, 0, KoreaLocation)
	if !start.Equal(want) {
		t.Fatalf("parseScheduleStart() = %s, want %s", start, want)
	}
	if _, err := parseScheduleStart("not-a-date", "01:00"); err == nil {
		t.Fatal("parseScheduleStart accepted invalid date")
	}
	if _, err := parseScheduleStart("2026-08-14", "not-a-clock"); err == nil {
		t.Fatal("parseScheduleStart accepted invalid clock")
	}

	for _, test := range []struct {
		value       string
		wantHour    int
		wantMinute  int
		wantFailure bool
	}{
		{value: "01:02", wantHour: 1, wantMinute: 2},
		{value: "25:30", wantHour: 25, wantMinute: 30},
		{value: "47:59", wantHour: 47, wantMinute: 59},
		{value: "48:00", wantFailure: true},
		{value: "12:60", wantFailure: true},
		{value: "12-30", wantFailure: true},
		{value: "aa:bb", wantFailure: true},
	} {
		t.Run(test.value, func(t *testing.T) {
			hour, minute, err := parseExtendedClock(test.value)
			if test.wantFailure {
				if err == nil {
					t.Fatalf("parseExtendedClock(%q) accepted invalid clock", test.value)
				}
				return
			}
			if err != nil || hour != test.wantHour || minute != test.wantMinute {
				t.Fatalf("parseExtendedClock(%q) = %d:%02d, %v; want %d:%02d", test.value, hour, minute, err, test.wantHour, test.wantMinute)
			}
		})
	}
}
