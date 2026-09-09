package main

import (
	"context"
	"errors"
	"slices"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/cineko-org/client/internal/adapters/cgv"
)

func (p *bookingQueryPacer) wait(ctx context.Context) error {
	return p.run(ctx, func() error { return nil })
}

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

func TestSharedCinemaFailureStopsOtherShowtimeQueries(t *testing.T) {
	for _, cause := range []error{cgv.ErrUIContractChanged, cgv.ErrAuthenticationRequired, cgv.ErrCaptchaRequired} {
		t.Run(cause.Error(), func(t *testing.T) {
			// Compare query invocations only: the previous gate paced starts
			// but did not share failures between different showtime tabs.
			beforeCalls := 0
			previousQuery := func() error { beforeCalls++; return cause }
			for range 21 {
				_ = previousQuery()
			}
			var pacer bookingQueryPacer
			var calls atomic.Int32
			var group sync.WaitGroup
			for range 21 {
				group.Go(func() {
					err := pacer.run(t.Context(), func() error { calls.Add(1); return cause })
					if !errors.Is(err, cause) {
						t.Errorf("query error = %v", err)
					}
				})
			}
			group.Wait()
			t.Logf("21 showtimes, same failing query: independent failure handling=%d calls, shared gate=%d calls (query-count comparison, not a timing benchmark)", beforeCalls, calls.Load())
			if calls.Load() != 1 {
				t.Fatalf("broken cinema flow repeated %d times", calls.Load())
			}
			if pacer.pauseError() == nil {
				t.Fatal("browser capacity should be paused")
			}
			pacer.mu.Lock()
			pacer.paused.after = time.Now().Add(-time.Second)
			pacer.mu.Unlock()
			pacer.next = time.Now().Add(-time.Second)
			if err := pacer.run(t.Context(), func() error { calls.Add(1); return cause }); !errors.Is(err, cause) {
				t.Fatal(err)
			}
			if calls.Load() != 2 || time.Until(pacer.paused.after) < 59*time.Second {
				t.Fatal("shared retry did not back off to 60 seconds")
			}
			pacer.paused.after = time.Now().Add(-time.Second)
			pacer.next = time.Now().Add(-time.Second)
			if err := pacer.run(t.Context(), func() error { return nil }); err != nil {
				t.Fatal(err)
			}
			if pacer.pauseError() != nil || pacer.failures != 0 {
				t.Fatal("successful recovery did not clear the shared failure")
			}
		})
	}
}
