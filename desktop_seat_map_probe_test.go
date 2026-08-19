package main

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/cineko-org/client/internal/domain"
	central "github.com/cineko-org/contracts/v3"
)

func TestSeatMapCapabilityTracksAuthenticatedSession(t *testing.T) {
	t.Parallel()
	state := &seatMapCapabilityState{}
	if capabilities := state.AvailableCapabilities(); len(capabilities) != 2 {
		t.Fatalf("logged-out capabilities = %v", capabilities)
	}
	state.SetAuthenticated(true)
	capabilities := state.AvailableCapabilities()
	if len(capabilities) != 3 || capabilities[2] != central.CapabilityCGVSeatMapCapture {
		t.Fatalf("logged-in capabilities = %v", capabilities)
	}
	state.SetAuthenticated(false)
	if capabilities := state.AvailableCapabilities(); len(capabilities) != 2 {
		t.Fatalf("logged-out capabilities after transition = %v", capabilities)
	}
}

func TestStaticSeatMapResultLeavesCanonicalIdentityToCentral(t *testing.T) {
	t.Parallel()
	result, err := staticSeatMapVersion("auditorium", domain.SeatMap{
		Version: "client-layout-hash",
		Seats: []domain.Seat{{
			ID: "seat-a1", AuditoriumID: "auditorium", Label: "A1", Row: "A", Number: 1,
			X: 0.5, Y: 0.5, Type: domain.SeatTypeStandard,
		}},
		ObservedAt: time.Date(2026, 8, 20, 12, 0, 0, 0, time.UTC),
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.ID != "" || result.LayoutHash != "" || result.AuditoriumID != "auditorium" || result.Capacity != 1 {
		t.Fatalf("static seat-map result = %+v", result)
	}
	payload := string(result.Layout)
	for _, forbidden := range []string{"available", "soldOut", "evidence", "screenshot", "client-layout-hash"} {
		if strings.Contains(payload, forbidden) {
			t.Fatalf("static seat-map payload contains %q: %s", forbidden, payload)
		}
	}
}

func TestStaticSeatMapResultIsStableAcrossSourceOrdering(t *testing.T) {
	t.Parallel()
	observedAt := time.Date(2026, 8, 20, 12, 0, 0, 0, time.UTC)
	first := domain.SeatMap{
		Seats: []domain.Seat{
			{Label: "A2", Features: []string{"recliner", "premium"}, SourceClasses: []string{"right", "seat"}},
			{Label: "A1", Features: []string{"wheelchair"}, SourceClasses: []string{}},
		},
		Zones:      []domain.LayoutZone{{Code: "B", Name: "뒤"}, {Code: "A", Name: "앞"}},
		Blocks:     []domain.LayoutBlock{{Code: "B", MinX: 0.5}, {Code: "A", MinX: 0.1}},
		ObservedAt: observedAt,
	}
	second := domain.SeatMap{
		Seats: []domain.Seat{
			{Label: "A1", Features: []string{"wheelchair"}, SourceClasses: []string{}},
			{Label: "A2", Features: []string{"premium", "recliner"}, SourceClasses: []string{"seat", "right"}},
		},
		Zones:      []domain.LayoutZone{{Code: "A", Name: "앞"}, {Code: "B", Name: "뒤"}},
		Blocks:     []domain.LayoutBlock{{Code: "A", MinX: 0.1}, {Code: "B", MinX: 0.5}},
		ObservedAt: observedAt,
	}
	firstResult, err := staticSeatMapVersion("auditorium", first)
	if err != nil {
		t.Fatal(err)
	}
	secondResult, err := staticSeatMapVersion("auditorium", second)
	if err != nil {
		t.Fatal(err)
	}
	if string(firstResult.Layout) != string(secondResult.Layout) {
		t.Fatalf("static layouts differ:\n%s\n%s", firstResult.Layout, secondResult.Layout)
	}
}

func TestSeatMapAssignmentConversionAndValidation(t *testing.T) {
	t.Parallel()
	startsAt := time.Date(2026, 8, 20, 12, 30, 0, 0, time.UTC)
	endsAt := startsAt.Add(2 * time.Hour)
	auditorium := central.Auditorium{ID: "auditorium", TheaterID: "theater", Name: "IMAX관"}
	task := central.AssignmentTask{
		Kind:       central.CapabilityCGVSeatMapCapture,
		Theater:    central.Theater{ID: "theater", Region: "서울", Name: "용산아이파크몰"},
		Auditorium: &auditorium,
		Showtime: &central.Showtime{
			ID: "showtime", Movie: central.Movie{Title: "영화"}, Auditorium: auditorium,
			StartsAt: startsAt, EndsAt: endsAt,
		},
		TimeZone: "Asia/Seoul",
	}
	domainAuditorium := domain.Auditorium{ID: auditorium.ID, TheaterID: auditorium.TheaterID, Name: auditorium.Name}
	showtime, err := seatMapShowtime(task, domainAuditorium)
	if err != nil || showtime.Date != "2026-08-20" || showtime.StartsAt != "21:30" ||
		showtime.TheaterName != "용산아이파크몰" {
		t.Fatalf("seatMapShowtime() = %+v, %v", showtime, err)
	}
	if _, err := (&clientSeatMapExecutor{}).CaptureSeatMap(context.Background(), central.AssignmentTask{}); err == nil {
		t.Fatal("incomplete seat-map assignment accepted")
	}
	task.TimeZone = "invalid"
	if _, err := seatMapShowtime(task, domainAuditorium); err == nil {
		t.Fatal("invalid seat-map time zone accepted")
	}
}
