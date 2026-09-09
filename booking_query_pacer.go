package main

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/cineko-org/client/internal/adapters/cgv"
	"github.com/cineko-org/client/internal/logging"
	observationpb "github.com/cineko-org/contracts/v3/gen/go/cineko/observation"
	seatmappb "github.com/cineko-org/contracts/v3/gen/go/cineko/seatmap"
)

const bookingQueryInterval = 2 * time.Second

// One start budget for all seat-query tabs, not one budget per showtime.
// This only paces reads. Preparing the selected seats never waits here.
type bookingQueryPacer struct {
	once     sync.Once
	token    chan struct{}
	next     time.Time
	mu       sync.Mutex
	failures int
	paused   *bookingQueryPausedError
}

// One shared failure deadline prevents another showtime from immediately
// repeating a broken cinema/login flow. It does not change provider 429 gates.
type bookingQueryPausedError struct {
	after time.Time
	cause error
}

func (err *bookingQueryPausedError) Error() string {
	return fmt.Sprintf("booking queries paused until %s: %v", err.after.Format(time.RFC3339), err.cause)
}

func (err *bookingQueryPausedError) Unwrap() error { return err.cause }

func (p *bookingQueryPacer) pauseError() error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.paused != nil && time.Now().Before(p.paused.after) {
		return p.paused
	}
	return nil
}

func (p *bookingQueryPacer) run(ctx context.Context, query func() error) error {
	p.once.Do(func() { p.token = make(chan struct{}, 1); p.token <- struct{}{} })
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-p.token:
	}
	defer func() { p.token <- struct{}{} }()
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := p.pauseError(); err != nil {
		return err
	}
	if delay := time.Until(p.next); delay > 0 {
		timer := time.NewTimer(delay)
		defer timer.Stop()
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-timer.C:
		}
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	p.next = time.Now().Add(bookingQueryInterval)
	// Keep the token until the read completes, so queued tabs observe its
	// failure before issuing requests of their own. Seat claims bypass this.
	err := query()
	p.mu.Lock()
	if errors.Is(err, cgv.ErrUIContractChanged) || errors.Is(err, cgv.ErrAuthenticationRequired) || errors.Is(err, cgv.ErrAuthenticationUnverified) || errors.Is(err, cgv.ErrCaptchaRequired) {
		p.failures++
		delay := min(30*time.Second*time.Duration(1<<min(p.failures-1, 5)), 15*time.Minute)
		p.paused = &bookingQueryPausedError{after: time.Now().Add(delay), cause: err}
		logging.WarnUnexpected(ctx, "booking.queries.paused", "booking_monitoring", "query_seats",
			"shared CGV page is ready before other tabs query", "all seat queries paused after a common browser failure",
			"retry_at", p.paused.after, "failures", p.failures, "error", err.Error())
	} else if err == nil {
		p.failures, p.paused = 0, nil
	}
	p.mu.Unlock()
	return err
}

func (automation *bookingTabAutomation) OpenSeatSelection(ctx context.Context, task *observationpb.SeatAvailabilityTask, count int) (*seatmappb.LiveSeatObservation, error) {
	var observation *seatmappb.LiveSeatObservation
	err := automation.host.queryPacer.run(ctx, func() error {
		var err error
		observation, err = automation.Adapter.OpenSeatSelection(ctx, task, count)
		return err
	})
	return observation, err
}

func (automation *bookingTabAutomation) RefreshSeatSelection(ctx context.Context, task *observationpb.SeatAvailabilityTask) (*seatmappb.LiveSeatObservation, error) {
	var observation *seatmappb.LiveSeatObservation
	err := automation.host.queryPacer.run(ctx, func() error {
		var err error
		observation, err = automation.Adapter.RefreshSeatSelection(ctx, task)
		return err
	})
	return observation, err
}
