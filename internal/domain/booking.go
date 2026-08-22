package domain

import (
	"time"
)

type Showtime struct {
	ID string
	// ProviderID and SourceKey are the authoritative provider tuple for a showtime.
	// Display text must never be used as a fallback identity.
	ProviderID string
	SourceKey  string
	// MovieID is the canonical catalog identity used for execution matching.
	MovieID string
	// Movie is a display snapshot and is not an execution identity.
	Movie          string
	PosterURL      string
	TheaterID      string
	TheaterRegion  string
	TheaterName    string
	AuditoriumID   string
	AuditoriumName string
	ScreenTypes    []string
	// Date is the provider service date used to select the schedule page.
	Date string
	// CivilDate is the local calendar date of the start instant. It differs
	// from Date when CGV uses an extended service-day clock such as 25:30.
	CivilDate      string
	StartsAt       string
	EndsAt         string
	AvailableSeats int
	Capacity       int
	SoldOut        bool
	ObservedAt     time.Time
	SourceLabel    string
}

type LiveSeat struct {
	Label        string
	Available    bool
	StatusCode   string
	StatusName   string
	SaleFormCode string
	ObservedAt   time.Time
	Source       string
}

// SeatSelection is the authoritative seat state read from the live CGV
// booking flow. It deliberately travels only inside the Client process.
type SeatSelection struct {
	SeatMap   SeatMap
	LiveSeats []LiveSeat
}
