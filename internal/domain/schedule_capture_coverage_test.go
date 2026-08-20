package domain

import "testing"

func TestShowtimeOccurrenceKeyFallsBackToDisplayMovie(t *testing.T) {
	t.Parallel()

	showtime := Showtime{
		TheaterID: "theater", AuditoriumID: "auditorium", Movie: "Movie",
		Date: "2026-08-10", StartsAt: "12:30",
	}
	want := "theater\x00auditorium\x00Movie\x002026-08-10\x0012:30"
	if got := ShowtimeOccurrenceKey(showtime); got != want {
		t.Fatalf("ShowtimeOccurrenceKey(display movie fallback) = %q, want %q", got, want)
	}
}
