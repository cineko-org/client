package domain

import (
	"fmt"
	"strings"
	"time"
)

// ScheduleDateFromShowtimeSourceKey extracts the provider service date from a
// canonical showtime tuple. The provider date is distinct from the civil date
// for extended clocks such as 25:30.
func ScheduleDateFromShowtimeSourceKey(sourceKey string) (string, error) {
	parts := strings.Split(strings.TrimSpace(sourceKey), "/")
	if len(parts) != 4 {
		return "", fmt.Errorf("showtime source key must contain provider, date, auditorium, and sequence")
	}
	for _, part := range parts {
		if strings.TrimSpace(part) == "" {
			return "", fmt.Errorf("showtime source key contains an empty tuple component")
		}
	}
	if _, err := time.Parse(time.DateOnly, parts[1]); err != nil {
		return "", fmt.Errorf("showtime source key has invalid schedule date: %w", err)
	}
	return parts[1], nil
}

type Showtime struct {
	ID string `json:"id"`
	// ProviderID and SourceKey are the authoritative provider tuple for a showtime.
	// Display text must never be used as a fallback identity.
	ProviderID string `json:"providerId"`
	SourceKey  string `json:"sourceKey"`
	// MovieID is the canonical catalog identity used for execution matching.
	MovieID string `json:"movieId"`
	// Movie is a display snapshot and is not an execution identity.
	Movie          string   `json:"movie"`
	PosterURL      string   `json:"posterUrl,omitempty"`
	TheaterID      string   `json:"theaterId"`
	TheaterRegion  string   `json:"theaterRegion,omitempty"`
	TheaterName    string   `json:"theaterName"`
	AuditoriumID   string   `json:"auditoriumId"`
	AuditoriumName string   `json:"auditoriumName"`
	ScreenTypes    []string `json:"screenTypes"`
	// Date is the provider service date used to select the schedule page.
	Date string `json:"date"`
	// CivilDate is the local calendar date of the start instant. It differs
	// from Date when CGV uses an extended service-day clock such as 25:30.
	CivilDate      string    `json:"civilDate,omitempty"`
	StartsAt       string    `json:"startsAt"`
	EndsAt         string    `json:"endsAt"`
	AvailableSeats int       `json:"availableSeats"`
	Capacity       int       `json:"capacity"`
	SoldOut        bool      `json:"soldOut"`
	ObservedAt     time.Time `json:"observedAt"`
	SourceLabel    string    `json:"sourceLabel"`
}

type LiveSeat struct {
	Label        string    `json:"label"`
	Available    bool      `json:"available"`
	StatusCode   string    `json:"statusCode"`
	StatusName   string    `json:"statusName"`
	SaleFormCode string    `json:"saleFormCode"`
	ObservedAt   time.Time `json:"observedAt"`
	Source       string    `json:"source"`
}

// SeatSelection is the authoritative seat state read from the live CGV
// booking flow. It deliberately travels only inside the Client process.
type SeatSelection struct {
	SeatMap   SeatMap    `json:"seatMap"`
	LiveSeats []LiveSeat `json:"liveSeats"`
}

type BookingDraft struct {
	Showtime   Showtime `json:"showtime"`
	SeatLabels []string `json:"seatLabels"`
	TotalPrice string   `json:"totalPrice"`
}

type Reservation struct {
	ID            string       `json:"id"`
	UserID        string       `json:"userId"`
	MonitorID     string       `json:"monitorId"`
	BookingNumber string       `json:"bookingNumber"`
	Draft         BookingDraft `json:"draft"`
	Status        string       `json:"status"`
	BookedAt      time.Time    `json:"bookedAt"`
	CancelledAt   *time.Time   `json:"cancelledAt"`
	RefundAmount  string       `json:"refundAmount"`
}

type CancellationDraft struct {
	ReservationID string `json:"reservationId"`
	BookingNumber string `json:"bookingNumber"`
	RefundAmount  string `json:"refundAmount"`
}
