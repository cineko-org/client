package cgv

import (
	"fmt"
	"testing"
	"time"
)

func TestMovieCalendarDetailBudgetAndCoverage(t *testing.T) {
	now := time.Date(2026, 9, 8, 0, 0, 0, 0, time.UTC)
	dates := []string{"2026-09-10", "2026-09-11", "2026-09-12", "2026-09-13"}
	var rotation scheduleDateRotation
	// Warm up initial discovery, then measure identical one-hour windows.
	for i := range len(dates) {
		rotation.nextDue(dates, i, now.Add(time.Duration(i)*30*time.Second), time.Minute)
	}
	start := now.Add(3 * time.Minute)
	reads := 0
	last := make(map[string]time.Time)
	maxGap := time.Duration(0)
	for tick := range 120 {
		at := start.Add(time.Duration(tick) * 30 * time.Second)
		date := rotation.nextDue(dates, tick, at, time.Minute)
		if date == "" {
			continue
		}
		reads++
		if previous, ok := last[date]; ok && at.Sub(previous) > maxGap {
			maxGap = at.Sub(previous)
		}
		last[date] = at
	}
	if reads != 60 || len(last) != 4 || maxGap != 4*time.Minute {
		t.Fatal("detail coverage/budget", reads, len(last), maxGap)
	}
	// Baseline: the same four relevant dates in a 16-date theater inventory.
	var baseline scheduleDateRotation
	broad := append([]string(nil), dates...)
	for i := range 12 {
		broad = append(broad, fmt.Sprintf("unrelated-%02d", i))
	}
	baselineLast := make(map[string]time.Time)
	baselineGap := time.Duration(0)
	for tick := range 120 {
		at := start.Add(time.Duration(tick) * 30 * time.Second)
		date := baseline.next(broad, tick)
		if previous, ok := baselineLast[date]; ok && at.Sub(previous) > baselineGap {
			baselineGap = at.Sub(previous)
		}
		baselineLast[date] = at
	}
	if baselineGap != 8*time.Minute {
		t.Fatal("baseline changed", baselineGap)
	}
	t.Logf("warm 1h, same movie/weekdays: inventory+detail 240 -> %d; relevant detail revisit %v -> %v; new-date inventory 30s unchanged", 120+reads, baselineGap, maxGap)
}

func TestMovieCalendarNewDatesAndEmptyInventory(t *testing.T) {
	now := time.Now()
	var rotation scheduleDateRotation
	if rotation.nextDue([]string{"a"}, 0, now, time.Minute) != "a" {
		t.Fatal("initial date not read")
	}
	if rotation.nextDue([]string{"a"}, 1, now.Add(30*time.Second), time.Minute) != "" {
		t.Fatal("unchanged inventory caused early detail read")
	}
	if rotation.nextDue([]string{"a", "b"}, 2, now.Add(30*time.Second), time.Minute) != "b" {
		t.Fatal("new date delayed by known-date interval")
	}
	if rotation.nextDue(nil, 3, now.Add(45*time.Second), time.Minute) != "" {
		t.Fatal("empty inventory triggered detail")
	}
	if len(rotation.lastAttempt) != 0 || rotation.nextDue([]string{"a"}, 4, now.Add(50*time.Second), time.Minute) != "a" {
		t.Fatal("reappearing date was not rediscovered")
	}
	if rotation.nextDue([]string{"a"}, 5, now.Add(24*time.Hour), time.Minute) != "a" ||
		rotation.nextDue([]string{"a"}, 6, now.Add(24*time.Hour), time.Minute) != "" {
		t.Fatal("long pause caused catch-up burst")
	}
}

func TestScheduleRotationPrioritizesNewDateWithoutMoreRequests(t *testing.T) {
	before := []string{"01", "02", "03", "04", "05", "06", "07"}
	var rotation scheduleDateRotation
	for shard := range 8 {
		rotation.next(before, shard)
	}
	after := append(append([]string(nil), before...), "08")
	oldSlots := 0
	for shard := 8; ; shard++ {
		oldSlots++
		if after[shard%len(after)] == "08" {
			break
		}
	}
	if got := rotation.next(after, 8); got != "08" {
		t.Fatalf("next = %q, want newly visible date", got)
	}
	t.Logf("same 30s scan budget, date added after 8 scans: modulo=%d slots/%ds; age-based=1 slot/30s", oldSlots, oldSlots*30)
	seen := map[string]bool{"08": true}
	for shard := 9; shard < 16; shard++ {
		seen[rotation.next(after, shard)] = true
	}
	if len(seen) != len(after) {
		t.Fatalf("older dates starved: %v", seen)
	}
}

func TestScheduleRotationPrunesRemovedDatesAndIsFair(t *testing.T) {
	var rotation scheduleDateRotation
	for shard := range 3 {
		rotation.next([]string{"a", "b", "c"}, shard)
	}
	dates := []string{"b", "c", "d"}
	if got := rotation.next(dates, 3); got != "d" {
		t.Fatal(got)
	}
	if _, retained := rotation.lastAttempt["a"]; retained {
		t.Fatal("removed date retained")
	}
	seen := make(map[string]int)
	for shard := 4; shard < 34; shard++ {
		seen[rotation.next(dates, shard)]++
	}
	for date, count := range seen {
		if count != 10 {
			t.Fatalf("%s checked %d times", date, count)
		}
	}
}

func TestScheduleCalendarRefreshBoundaries(t *testing.T) {
	now := time.Date(2026, 9, 8, 4, 0, 0, 0, time.UTC)
	for _, test := range []struct {
		name           string
		refreshed, now time.Time
		reusable       bool
	}{
		{"fresh", now.Add(-time.Minute), now, true},
		{"five minutes", now.Add(-5 * time.Minute), now, false},
		{"uninitialized", time.Time{}, now, false},
		{"clock backwards", now.Add(time.Minute), now, false},
		{"KST midnight", time.Date(2026, 9, 7, 14, 59, 0, 0, time.UTC), time.Date(2026, 9, 7, 15, 0, 0, 0, time.UTC), false},
	} {
		t.Run(test.name, func(t *testing.T) {
			if got := reusableCinemaSelection("서울", "용산", "서울", "용산", test.refreshed, test.now); got != test.reusable {
				t.Fatalf("reuse=%v", got)
			}
		})
	}
	if reusableCinemaSelection("서울", "용산", "서울", "왕십리", now, now) {
		t.Fatal("different theater reused")
	}
}
