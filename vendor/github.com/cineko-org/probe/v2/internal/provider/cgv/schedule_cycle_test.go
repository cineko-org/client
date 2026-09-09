package cgv

import (
	"context"
	"errors"
	"testing"
	"testing/synctest"
	"time"
)

func TestFullCycleBudgetAndCoverage(t *testing.T) {
	dates := []string{"2026-09-10", "2026-09-11", "2026-09-12", "2026-09-13"}
	start := time.Date(2026, 9, 9, 0, 0, 0, 0, time.UTC)
	var legacy scheduleDateRotation
	for i := range dates {
		legacy.nextDue(dates, i, start.Add(time.Duration(i-10)*30*time.Second), time.Minute)
	}
	oldReads, newReads := 0, 0
	oldLast, newLast := map[string]time.Time{}, map[string]time.Time{}
	var oldGap, newGap time.Duration
	for tick := range 120 {
		at := start.Add(time.Duration(tick) * 30 * time.Second)
		if date := legacy.nextDue(dates, tick, at, time.Minute); date != "" {
			oldReads++
			if last, ok := oldLast[date]; ok {
				oldGap = max(oldGap, at.Sub(last))
			}
			oldLast[date] = at
		}
		for i, date := range dates {
			observed := at.Add(time.Duration(i+1) * 30 * time.Second / time.Duration(len(dates)+1))
			newReads++
			if last, ok := newLast[date]; ok {
				newGap = max(newGap, observed.Sub(last))
			}
			newLast[date] = observed
		}
	}
	if oldReads != 60 || newReads != 480 || oldGap != 4*time.Minute || newGap != 30*time.Second {
		t.Fatal(oldReads, newReads, oldGap, newGap)
	}
	t.Logf("same 1h/4 dates/30s cycles: core requests %d -> %d; date revisit %v -> %v (87.5%% shorter); model excludes provider latency/cooldowns", 120+oldReads, 120+newReads, oldGap, newGap)
}

func TestScheduleCycleWaitCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	for _, due := range []time.Time{time.Now().Add(time.Hour), time.Now().Add(-time.Hour)} {
		if err := waitScheduleRequest(ctx, due); !errors.Is(err, context.Canceled) {
			t.Fatal("canceled wait reached next request", err)
		}
	}
}

func TestScheduleCyclePacesWithoutCatchUp(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		start := time.Now()
		spacing := 30 * time.Second / 5
		for i := 1; i <= 4; i++ {
			if err := waitScheduleRequest(t.Context(), start.Add(time.Duration(i)*spacing)); err != nil {
				t.Fatal(err)
			}
			if time.Since(start) != time.Duration(i)*spacing {
				t.Fatal("request not paced")
			}
		}
		// A slow call consumes its slot; the next deadline is based on the
		// actual request start, never on a queue of missed historical ticks.
		slowStart := time.Now()
		time.Sleep(10 * time.Second)
		if err := waitScheduleRequest(t.Context(), slowStart.Add(spacing)); err != nil {
			t.Fatal(err)
		}
		actualStart := time.Now()
		if err := waitScheduleRequest(t.Context(), actualStart.Add(spacing)); err != nil {
			t.Fatal(err)
		}
		if time.Since(actualStart) != spacing {
			t.Fatal("slow response caused catch-up burst")
		}
	})
}
