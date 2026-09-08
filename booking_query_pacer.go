package main

import (
	"context"
	"sync"
	"time"

	observationpb "github.com/cineko-org/contracts/v3/gen/go/cineko/observation"
	seatmappb "github.com/cineko-org/contracts/v3/gen/go/cineko/seatmap"
)

const bookingQueryInterval = 2 * time.Second

// One start budget for all seat-query tabs, not one budget per showtime.
// This only paces reads. Preparing the selected seats never waits here.
type bookingQueryPacer struct {
	once  sync.Once
	token chan struct{}
	next  time.Time
}

func (p *bookingQueryPacer) wait(ctx context.Context) error {
	p.once.Do(func() { p.token = make(chan struct{}, 1); p.token <- struct{}{} })
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-p.token:
	}
	defer func() { p.token <- struct{}{} }()
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
	return nil
}

func (automation *bookingTabAutomation) OpenSeatSelection(ctx context.Context, task *observationpb.SeatAvailabilityTask, count int) (*seatmappb.LiveSeatObservation, error) {
	if err := automation.host.queryPacer.wait(ctx); err != nil {
		return nil, err
	}
	return automation.Adapter.OpenSeatSelection(ctx, task, count)
}

func (automation *bookingTabAutomation) RefreshSeatSelection(ctx context.Context, task *observationpb.SeatAvailabilityTask) (*seatmappb.LiveSeatObservation, error) {
	if err := automation.host.queryPacer.wait(ctx); err != nil {
		return nil, err
	}
	return automation.Adapter.RefreshSeatSelection(ctx, task)
}
