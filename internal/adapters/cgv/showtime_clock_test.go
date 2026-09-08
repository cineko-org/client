package cgv

import (
	"fmt"
	"testing"
	"time"

	"github.com/cineko-org/client/internal/domain"
	catalogpb "github.com/cineko-org/contracts/v3/gen/go/cineko/catalog"
	"google.golang.org/protobuf/encoding/protojson"
)

func overnightShowtime(t *testing.T) domain.Showtime {
	t.Helper()
	entry, err := scheduleEntryFromProviderRow(providerScheduleRow{
		SiteNo: "0013", MovieNo: "30001323", MovieTitle: "오디세이",
		AuditoriumNo: "018", AuditoriumName: "IMAX관", Sequence: "6",
		Date: "2026-09-10", StartClock: "2500", EndClock: "2802",
	}, domain.Theater{ID: "theater", Name: "용산아이파크몰"})
	if err != nil {
		t.Fatal(err)
	}
	return entry.Showtime
}

func TestShowtimeServiceAndCivilClocks(t *testing.T) {
	for _, tc := range []struct{ date, start, end, civilDate, civilStart, civilEnd, providerStart, providerEnd string }{
		{"2026-09-10", "2500", "2802", "2026-09-11", "01:00", "04:02", "25:00", "28:02"},
		{"2026-09-10", "1930", "2131", "2026-09-10", "19:30", "21:31", "19:30", "21:31"},
		{"2026-09-10", "2130", "2432", "2026-09-10", "21:30", "00:32", "21:30", "24:32"},
		{"2026-09-30", "2400", "2600", "2026-10-01", "00:00", "02:00", "24:00", "26:00"},
		{"2026-12-31", "2500", "2802", "2027-01-01", "01:00", "04:02", "25:00", "28:02"},
	} {
		t.Run(tc.date+"/"+tc.start, func(t *testing.T) {
			entry, err := scheduleEntryFromProviderRow(providerScheduleRow{Date: tc.date, StartClock: tc.start, EndClock: tc.end, AuditoriumName: "IMAX관"}, domain.Theater{})
			if err != nil {
				t.Fatal(err)
			}
			s := entry.Showtime
			if s.Date != tc.date || s.CivilDate != tc.civilDate || s.StartsAt != tc.civilStart || s.EndsAt != tc.civilEnd || s.ProviderStartsAt != tc.providerStart || s.ProviderEndsAt != tc.providerEnd {
				t.Fatalf("projection = %+v", s)
			}
			base, _ := time.ParseInLocation(time.DateOnly, tc.date, domain.KoreaLocation)
			for _, clock := range []struct{ raw, want string }{{tc.start, tc.providerStart}, {tc.end, tc.providerEnd}} {
				hour, minute, _ := parseProviderClock(clock.raw)
				instant := base.Add(time.Duration(hour)*time.Hour + time.Duration(minute)*time.Minute)
				if got := serviceClockFromInstant(tc.date, instant.UTC()); got != clock.want {
					t.Fatalf("reconstructed clock %q, want %q", got, clock.want)
				}
			}
		})
	}
	s := overnightShowtime(t)
	if !(domain.ScheduleWindow{Weekdays: []int{5}, Earliest: "00:00", Latest: "06:00"}).MatchesShowtime(s) {
		t.Fatal("Friday 01:00 rejected")
	}
	if (domain.ScheduleWindow{Weekdays: []int{4}}).MatchesShowtime(s) {
		t.Fatal("Thursday service date used as civil weekday")
	}
}

func TestShowtimeProtoRestoresServiceClocks(t *testing.T) {
	var value catalogpb.Showtime
	if err := protojson.Unmarshal([]byte(`{"providerId":"cgv","identity":{"cgv":{"siteNo":"0013","scheduleDate":{"year":2026,"month":9,"day":10},"screenNo":"018","sequence":"6"}},"startsAt":"2026-09-10T16:00:00Z","endsAt":"2026-09-10T19:02:00Z"}`), &value); err != nil {
		t.Fatal(err)
	}
	s := showtimeDomainFromProto(&value)
	if s.Date != "2026-09-10" || s.CivilDate != "2026-09-11" || s.StartsAt != "01:00" || s.EndsAt != "04:02" || s.ProviderStartsAt != "25:00" || s.ProviderEndsAt != "28:02" {
		t.Fatalf("projection = %+v", s)
	}
	base := time.Date(2026, 9, 10, 0, 0, 0, 0, domain.KoreaLocation)
	for _, instant := range []time.Time{base.Add(-time.Second), base.Add(48 * time.Hour)} {
		if got := serviceClockFromInstant("2026-09-10", instant); got != "" {
			t.Fatalf("invalid instant returned %q", got)
		}
	}
	if serviceClockFromInstant("invalid", base) != "" {
		t.Fatal("invalid date accepted")
	}
}

func TestBrowserShowtimeServiceClockSelection(t *testing.T) {
	if testing.Short() {
		t.Skip("launches installed Chromium with local HTML only")
	}
	adapter, err := NewAdapter(t.Context(), localBrowserTestConfig(t))
	if err != nil {
		t.Fatal(err)
	}
	defer adapter.Close()
	setPage := func(body string) {
		t.Helper()
		if err := adapter.page.SetContent("<!doctype html><body>" + body + "</body>"); err != nil {
			t.Fatal(err)
		}
		if err := adapter.evaluate(shadowDOMBootstrap, nil); err != nil {
			t.Fatal(err)
		}
	}
	s := overnightShowtime(t)
	setPage(`<section>오디세이 IMAX관<button onclick="this.dataset.selected='yes'">25:00- 28:02 31/624석</button></section>`)
	// Prove the old normalized-clock lookup fails on the identical fixture.
	old := s
	old.ProviderStartsAt, old.ProviderEndsAt = old.StartsAt, old.EndsAt
	if clicked, err := adapter.clickExactShowtime(old); err != nil || clicked {
		t.Fatalf("old lookup = %v, %v; want no match", clicked, err)
	}
	if clicked, err := adapter.clickExactShowtime(s); err != nil || !clicked {
		t.Fatalf("provider lookup = %v, %v", clicked, err)
	}
	var selected string
	if err := adapter.evaluate(`document.querySelector('button').dataset.selected`, &selected); err != nil || selected != "yes" {
		t.Fatalf("actual DOM click = %q, %v", selected, err)
	}
	t.Log("identical 25:00–28:02 button: old civil-clock lookup=0 clicks; provider-clock lookup=1 actual click")
	setPage(`<section>오디세이 IMAX관<button>25:00-28:02</button><button>25:00-28:02</button></section>`)
	if clicked, err := adapter.clickExactShowtime(s); err == nil || clicked {
		t.Fatal("ambiguous display accepted")
	}
	for _, tc := range []struct {
		label, text string
		valid       bool
	}{
		{"service clock", "2026-09-10 25:00-28:02", true},
		{"civil clock", "2026-09-11 01:00-04:02", true},
		{"mixed date and clock", "2026-09-10 01:00-04:02", false},
		{"wrong date", "2026-09-12 25:00-28:02", false},
		{"wrong end", "2026-09-10 25:00-27:02", false},
	} {
		t.Run(tc.label, func(t *testing.T) {
			setPage(fmt.Sprintf("<main>오디세이 용산아이파크몰 IMAX관 %s</main>", tc.text))
			if err := adapter.verifySeatPageShowtime(s); (err == nil) != tc.valid {
				t.Fatalf("verification = %v, want valid=%t", err, tc.valid)
			}
		})
	}
}
