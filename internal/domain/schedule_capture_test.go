package domain

import "testing"

func TestShowtimeOccurrenceKey(t *testing.T) {
	t.Parallel()

	if got := ShowtimeOccurrenceKey(Showtime{ID: "source-showtime"}); got != "source-showtime" {
		t.Fatalf("ShowtimeOccurrenceKey(with ID) = %q", got)
	}

	showtime := Showtime{
		TheaterID: "theater", AuditoriumID: "auditorium", Movie: "Movie",
		Date: "2026-08-10", StartsAt: "12:30",
	}
	want := "theater\x00auditorium\x00Movie\x002026-08-10\x0012:30"
	if got := ShowtimeOccurrenceKey(showtime); got != want {
		t.Fatalf("ShowtimeOccurrenceKey(without ID) = %q, want %q", got, want)
	}
}
