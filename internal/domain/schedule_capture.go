package domain

import "strings"

// ScheduleCapture is the result of one complete theater/date schedule fetch.
// Central owns observation policy and persistence; the Client only uses this
// value while executing an assigned capture.
type ScheduleCapture struct {
	TargetDate string
	Showtimes  []Showtime
	Complete   bool
	Error      string
}

func ShowtimeOccurrenceKey(showtime Showtime) string {
	if showtime.ID != "" {
		return showtime.ID
	}
	movie := showtime.MovieID
	if movie == "" {
		movie = showtime.Movie
	}
	return strings.Join([]string{
		showtime.TheaterID, showtime.AuditoriumID, movie,
		showtime.Date, showtime.StartsAt,
	}, "\x00")
}
