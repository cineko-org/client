package domain

import "testing"

func TestScheduleWindowMatchesBoundariesAndOvernight(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name   string
		window ScheduleWindow
		date   string
		start  string
		want   bool
	}{
		{name: "empty window", window: ScheduleWindow{}, date: "2026-08-15", start: "01:00", want: true},
		{name: "inclusive start", window: ScheduleWindow{Earliest: "18:00", Latest: "21:00"}, date: "2026-08-15", start: "18:00", want: true},
		{name: "exclusive end", window: ScheduleWindow{Earliest: "18:00", Latest: "21:00"}, date: "2026-08-15", start: "21:00", want: false},
		{name: "overnight late", window: ScheduleWindow{Earliest: "21:00", Latest: "06:00"}, date: "2026-08-14", start: "23:59", want: true},
		{name: "overnight early", window: ScheduleWindow{Earliest: "21:00", Latest: "06:00"}, date: "2026-08-15", start: "01:00", want: true},
		{name: "overnight end excluded", window: ScheduleWindow{Earliest: "21:00", Latest: "06:00"}, date: "2026-08-15", start: "06:00", want: false},
		{name: "overnight middle excluded", window: ScheduleWindow{Earliest: "21:00", Latest: "06:00"}, date: "2026-08-15", start: "12:00", want: false},
		{name: "saturday belongs saturday", window: ScheduleWindow{Weekdays: []int{6}, Earliest: "00:00", Latest: "06:00"}, date: "2026-08-15", start: "01:00", want: true},
		{name: "weekday mismatch", window: ScheduleWindow{Weekdays: []int{5}}, date: "2026-08-15", start: "01:00", want: false},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := test.window.Matches(test.date, test.start); got != test.want {
				t.Fatalf("Matches(%q, %q) = %t, want %t", test.date, test.start, got, test.want)
			}
		})
	}
}

func TestScheduleWindowValidationAllowsOvernightAndRejectsEmptyRange(t *testing.T) {
	t.Parallel()
	if err := (ScheduleWindow{Earliest: "21:00", Latest: "06:00"}).Validate(); err != nil {
		t.Fatalf("overnight window rejected: %v", err)
	}
	if err := (ScheduleWindow{Earliest: "18:00", Latest: "18:00"}).Validate(); err == nil {
		t.Fatal("zero-length window accepted")
	}
}

func TestScheduleWindowUsesCivilDateForExtendedProviderClock(t *testing.T) {
	t.Parallel()
	showtime := Showtime{Date: "2026-08-14", CivilDate: "2026-08-15", StartsAt: "01:00"}
	if !(ScheduleWindow{Weekdays: []int{6}, Earliest: "00:00", Latest: "06:00"}).MatchesShowtime(showtime) {
		t.Fatal("Saturday civil start was rejected for a Friday service-day show")
	}
	if (ScheduleWindow{Weekdays: []int{5}, Earliest: "00:00", Latest: "06:00"}).MatchesShowtime(showtime) {
		t.Fatal("Friday service date was used instead of the Saturday civil start")
	}
	if !(ScheduleWindow{Weekdays: []int{6}}).Matches("2026-08-14", "25:00") {
		t.Fatal("extended 25:00 clock was rejected")
	}
}

func TestScheduleDateFromShowtimeSourceKey(t *testing.T) {
	t.Parallel()
	if got, err := ScheduleDateFromShowtimeSourceKey("0056/2026-08-14/0007/0003"); err != nil || got != "2026-08-14" {
		t.Fatalf("ScheduleDateFromShowtimeSourceKey() = %q, %v", got, err)
	}
	for _, sourceKey := range []string{"source", "0056/not-a-date/0007/0003", "0056/2026-08-14//0003"} {
		if _, err := ScheduleDateFromShowtimeSourceKey(sourceKey); err == nil {
			t.Fatalf("invalid source key %q was accepted", sourceKey)
		}
	}
}
