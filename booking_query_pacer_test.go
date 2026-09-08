package main

import (
	"context"
	"errors"
	"slices"
	"sync"
	"testing"
	"time"
)

func TestConcurrentSeatQueriesShareOneBudget(t *testing.T) {
	var pacer bookingQueryPacer
	measure := func(wait func(context.Context) error) []time.Time {
		starts := make(chan time.Time, 4)
		var wg sync.WaitGroup
		for range 4 {
			wg.Go(func() {
				if err := wait(t.Context()); err != nil {
					t.Error(err)
					return
				}
				starts <- time.Now()
			})
		}
		wg.Wait()
		close(starts)
		var result []time.Time
		for started := range starts {
			result = append(result, started)
		}
		slices.SortFunc(result, func(a, b time.Time) int { return a.Compare(b) })
		if len(result) != 4 {
			t.Fatalf("started %d/4 queries", len(result))
		}
		return result
	}
	// The previous boundary called the adapter directly, with no shared wait.
	before := measure(func(context.Context) error { return nil })
	after := measure(pacer.wait)
	var first, previous time.Time
	for _, started := range after {
		if first.IsZero() {
			first = started
		}
		if !previous.IsZero() && started.Sub(previous) < bookingQueryInterval-20*time.Millisecond {
			t.Fatalf("query starts only %s apart", started.Sub(previous))
		}
		previous = started
	}
	t.Logf("4 simultaneous query starts, same no-network workload: previous span=%s, paced span=%s, minimum spacing=%s", before[3].Sub(before[0]), previous.Sub(first), bookingQueryInterval)
}

func TestCanceledQueryDoesNotConsumeFutureSlot(t *testing.T) {
	var pacer bookingQueryPacer
	if err := pacer.wait(t.Context()); err != nil {
		t.Fatal(err)
	}
	deadline := pacer.next
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if err := pacer.wait(ctx); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if !pacer.next.Equal(deadline) {
		t.Fatal("canceled query delayed the next tab")
	}
}
