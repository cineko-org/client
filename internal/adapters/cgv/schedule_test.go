package cgv

import (
	"testing"
	"time"

	"github.com/cineko-org/client/internal/domain"
)

func TestDateButtonMatchesSemanticOrderAndSpacing(t *testing.T) {
	tests := []struct {
		name  string
		label string
		want  bool
	}{
		{name: "compact", label: "오늘12", want: true},
		{name: "spaced", label: "오늘 12", want: true},
		{name: "wide spacing", label: "오늘     12", want: true},
		{name: "reversed spaced", label: "12 오늘", want: true},
		{name: "reversed compact", label: "12오늘", want: true},
		{name: "punctuation", label: "오늘 · 12", want: true},
		{name: "weekday fallback", label: "수12", want: true},
		{name: "wrong day", label: "오늘13", want: false},
		{name: "unrelated text", label: "오늘 상영 12", want: false},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := dateButtonMatches(test.label, 12, []string{"오늘", "수"}); got != test.want {
				t.Fatalf("dateButtonMatches(%q) = %t, want %t", test.label, got, test.want)
			}
		})
	}
}

func TestParseScheduleAcceptsCompactLiveCGVLabel(t *testing.T) {
	entry, ok := parseSchedule(rawSchedule{
		Label:      "13:30- 16:326/624석",
		Movie:      "오디세이",
		Group:      "IMAX관IMAX LASER 2D",
		Auditorium: "",
	}, "2026-08-12", domain.Theater{ID: "theater", Name: "용산아이파크몰"})
	if !ok {
		t.Fatal("parseSchedule() rejected compact live label")
	}
	if entry.Showtime.StartsAt != "13:30" || entry.Showtime.EndsAt != "16:32" {
		t.Fatalf("unexpected time range: %#v", entry.Showtime)
	}
	if entry.Showtime.AvailableSeats != 6 || entry.Showtime.Capacity != 624 {
		t.Fatalf("unexpected availability: %#v", entry.Showtime)
	}
	if entry.AuditoriumName != "IMAX관" {
		t.Fatalf("auditorium = %q, want IMAX관", entry.AuditoriumName)
	}
	if entry.Showtime.ObservedAt.IsZero() || entry.Showtime.ObservedAt.After(time.Now().Add(time.Second)) {
		t.Fatal("observed time was not populated")
	}
}

func TestParseScheduleUsesStructuredAuditoriumInsteadOfShowtimeBadges(t *testing.T) {
	entry, ok := parseSchedule(rawSchedule{
		Label:      "19:45 - 21:44 매진 조조 성우 무대인사 컬처데이",
		Movie:      "명탐정 코난-하이웨이의 타천사",
		Group:      "2D",
		Auditorium: "6관 (Laser)",
		Disabled:   true,
	}, "2026-08-12", domain.Theater{ID: "theater", Name: "용산아이파크몰"})
	if !ok {
		t.Fatal("parseSchedule() rejected structured auditorium")
	}
	if entry.AuditoriumName != "6관 (Laser)" {
		t.Fatalf("auditorium = %q, want 6관 (Laser)", entry.AuditoriumName)
	}
	if entry.Showtime.ID != "9238317d2a1589ed7c5d3241" {
		t.Fatalf("canonical CGV showtime identity drifted: %q", entry.Showtime.ID)
	}
	if !entry.Showtime.SoldOut {
		t.Fatal("sold-out showtime was not preserved")
	}
}

func TestParseScheduleRejectsBadgeOnlyFallbackAsAuditorium(t *testing.T) {
	_, ok := parseSchedule(rawSchedule{
		Label: "12:50 - 15:20 38 / 50 석 조조 컬처데이",
		Movie: "스파이더맨-브랜드 뉴 데이",
		Group: "2D",
	}, "2026-08-12", domain.Theater{ID: "theater", Name: "용산아이파크몰"})
	if ok {
		t.Fatal("parseSchedule() accepted showtime badges as an auditorium name")
	}
}

func TestParseScheduleAcceptsSpacedAndSoldOutLabels(t *testing.T) {
	available, ok := parseSchedule(rawSchedule{
		Label: "12:50 - 15:20 38 / 50 석 조조",
		Movie: "스파이더맨-브랜드 뉴 데이",
		Group: "템퍼 시네마 A[CINE de CHEF]2D",
	}, "2026-08-12", domain.Theater{ID: "theater", Name: "용산아이파크몰"})
	if !ok || available.Showtime.AvailableSeats != 38 || available.Showtime.Capacity != 50 {
		t.Fatalf("parseSchedule(available) = %#v, %t", available, ok)
	}
	soldOut, ok := parseSchedule(rawSchedule{
		Label:    "10:00-12:57매진",
		Movie:    "오디세이",
		Group:    "스트레스리스 시네마[CINE de CHEF]2D",
		Disabled: true,
	}, "2026-08-12", domain.Theater{ID: "theater", Name: "용산아이파크몰"})
	if !ok || !soldOut.Showtime.SoldOut {
		t.Fatalf("parseSchedule(sold out) = %#v, %t", soldOut, ok)
	}
}
