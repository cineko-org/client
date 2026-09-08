package cgv

import (
	"testing"
	"time"
)

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
