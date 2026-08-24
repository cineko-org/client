package main

import (
	"context"
	"errors"
	"sync"

	"github.com/cineko-org/client/internal/adapters/browserfactory"
	"github.com/cineko-org/client/internal/adapters/cgv"
	"github.com/cineko-org/client/internal/application"
	"github.com/cineko-org/client/internal/booking"
	"github.com/cineko-org/client/internal/interfaces/webui"
	"github.com/cineko-org/client/internal/logging"
	catalogpb "github.com/cineko-org/contracts/v3/gen/go/cineko/catalog"
	clientpb "github.com/cineko-org/contracts/v3/gen/go/cineko/client"
)

// bookingAutomationHost owns one authenticated headed Chrome process. Each
// exact showtime gets a page-local Adapter, so watcher tabs share login state
// without sharing seat-response queues or selection mutexes.
type bookingAutomationHost struct {
	pool  *booking.Pool
	probe *embeddedProbe

	mu      sync.Mutex
	opening chan struct{}
	parent  *browserfactory.WarmAutomation
	tabs    map[*bookingTabAutomation]struct{}
	winner  *bookingTabAutomation
	closed  bool

	attemptMu sync.Mutex
}

type bookingTabAutomation struct {
	*cgv.Adapter
	host   *bookingAutomationHost
	parent *browserfactory.WarmAutomation

	mu       sync.Mutex
	retained bool
	closed   bool
}

func newBookingAutomationHost(pool *booking.Pool, probe *embeddedProbe) *bookingAutomationHost {
	return &bookingAutomationHost{
		pool: pool, probe: probe, tabs: make(map[*bookingTabAutomation]struct{}),
	}
}

func (host *bookingAutomationHost) Open(ctx context.Context) (webui.Automation, error) {
	if host == nil || host.pool == nil || host.probe == nil || ctx == nil {
		return nil, errors.New("booking browser host dependencies are incomplete")
	}
	return host.probe.OpenBooking(func() (webui.Automation, error) {
		parent, err := host.acquireParent(ctx)
		if err != nil {
			return nil, err
		}
		tab, err := parent.OpenTab(ctx)
		if err != nil {
			host.invalidate(parent)
			return nil, err
		}
		automation := &bookingTabAutomation{Adapter: tab, host: host, parent: parent}
		host.mu.Lock()
		if host.closed || host.parent != parent || host.winner != nil {
			host.mu.Unlock()
			tab.Close()
			return nil, booking.ErrProcessInvalid
		}
		host.tabs[automation] = struct{}{}
		host.mu.Unlock()
		return automation, nil
	})
}

func (host *bookingAutomationHost) acquireParent(ctx context.Context) (*browserfactory.WarmAutomation, error) {
	for {
		host.mu.Lock()
		if host.closed {
			host.mu.Unlock()
			return nil, booking.ErrPoolClosed
		}
		if host.winner != nil {
			host.mu.Unlock()
			return nil, application.ErrSeatUnavailable
		}
		if host.parent != nil {
			parent := host.parent
			host.mu.Unlock()
			return parent, nil
		}
		if host.opening != nil {
			opening := host.opening
			host.mu.Unlock()
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-opening:
			}
			continue
		}
		opening := make(chan struct{})
		host.opening = opening
		host.mu.Unlock()

		lease, err := host.pool.Acquire(ctx)
		var parent *browserfactory.WarmAutomation
		if err == nil {
			parent, err = browserfactory.WarmAutomationFromLease(lease)
			if err != nil {
				lease.Release()
			}
		}
		host.mu.Lock()
		if err == nil && !host.closed && host.parent == nil {
			host.parent = parent
		}
		host.opening = nil
		close(opening)
		closed := host.closed
		host.mu.Unlock()
		if err != nil {
			return nil, err
		}
		if closed {
			parent.Close()
			return nil, booking.ErrPoolClosed
		}
		go host.watchParent(parent)
		return parent, nil
	}
}

func (host *bookingAutomationHost) watchParent(parent *browserfactory.WarmAutomation) {
	failure := parent.PaymentFailure()
	if failure == nil {
		return
	}
	<-failure
	host.invalidate(parent)
}

func (host *bookingAutomationHost) CanAccept() bool {
	if host == nil {
		return false
	}
	host.mu.Lock()
	available := !host.closed && host.winner == nil && (host.parent != nil || host.opening != nil)
	host.mu.Unlock()
	if available {
		return true
	}
	return host.pool != nil && host.pool.Stats().Ready > 0
}

func (host *bookingAutomationHost) SetDemand(active bool) {
	if host == nil || host.pool == nil {
		return
	}
	if active {
		host.pool.SetDesired(booking.DefaultWarmBrowserCapacity)
		return
	}
	host.Reset()
	host.pool.SetDesired(0)
}

func (host *bookingAutomationHost) PreparePayment(
	automation *bookingTabAutomation,
	ctx context.Context,
	showtime *catalogpb.Showtime,
	labels []string,
) (*clientpb.Reservation, error) {
	host.attemptMu.Lock()
	defer host.attemptMu.Unlock()
	host.mu.Lock()
	if host.closed || host.parent != automation.parent || host.winner != nil && host.winner != automation {
		host.mu.Unlock()
		return nil, application.ErrSeatUnavailable
	}
	host.mu.Unlock()
	reservation, err := automation.Adapter.PreparePayment(ctx, showtime, labels)
	if err != nil {
		return nil, err
	}
	host.mu.Lock()
	if !host.closed && host.parent == automation.parent {
		host.winner = automation
	}
	host.mu.Unlock()
	return reservation, nil
}

func (automation *bookingTabAutomation) PreparePayment(
	ctx context.Context,
	showtime *catalogpb.Showtime,
	labels []string,
) (*clientpb.Reservation, error) {
	return automation.host.PreparePayment(automation, ctx, showtime, labels)
}

func (automation *bookingTabAutomation) RetainPayment() error {
	automation.mu.Lock()
	defer automation.mu.Unlock()
	if automation.closed {
		return booking.ErrProcessInvalid
	}
	automation.host.mu.Lock()
	winner := automation.host.winner == automation && automation.host.parent == automation.parent
	automation.host.mu.Unlock()
	if !winner {
		return booking.ErrProcessInvalid
	}
	if err := automation.parent.RetainPayment(); err != nil {
		return err
	}
	automation.retained = true
	automation.host.closeLosers(automation)
	return nil
}

func (automation *bookingTabAutomation) PaymentFailure() <-chan struct{} {
	if automation == nil || automation.parent == nil {
		return nil
	}
	return automation.parent.PaymentFailure()
}

func (automation *bookingTabAutomation) Close() {
	if automation == nil {
		return
	}
	automation.mu.Lock()
	if automation.closed {
		automation.mu.Unlock()
		return
	}
	automation.closed = true
	retained := automation.retained
	automation.mu.Unlock()
	automation.Adapter.Close()
	automation.host.removeTab(automation)
	if retained {
		automation.host.releasePayment(automation.parent)
	}
}

func (host *bookingAutomationHost) closeLosers(winner *bookingTabAutomation) {
	host.mu.Lock()
	losers := make([]*bookingTabAutomation, 0, len(host.tabs))
	for tab := range host.tabs {
		if tab != winner {
			losers = append(losers, tab)
		}
	}
	host.mu.Unlock()
	for _, tab := range losers {
		tab.Close()
	}
}

func (host *bookingAutomationHost) removeTab(tab *bookingTabAutomation) {
	host.mu.Lock()
	delete(host.tabs, tab)
	if host.winner == tab && !tab.retained {
		host.winner = nil
	}
	host.mu.Unlock()
}

func (host *bookingAutomationHost) releasePayment(parent *browserfactory.WarmAutomation) {
	host.mu.Lock()
	if host.parent != parent {
		host.mu.Unlock()
		return
	}
	host.parent = nil
	host.winner = nil
	host.mu.Unlock()
	parent.Close()
	logging.Info(context.Background(), "cgv.booking.host.released",
		"event", "cgv.booking.host.released", "scenario", "booking_monitoring",
		"operation", "release_payment_browser", "outcome", "completed")
}

func (host *bookingAutomationHost) invalidate(parent *browserfactory.WarmAutomation) {
	host.mu.Lock()
	if host.parent != parent {
		host.mu.Unlock()
		return
	}
	host.parent = nil
	host.winner = nil
	tabs := make([]*bookingTabAutomation, 0, len(host.tabs))
	for tab := range host.tabs {
		tabs = append(tabs, tab)
	}
	host.mu.Unlock()
	for _, tab := range tabs {
		tab.Close()
	}
	parent.Close()
}

func (host *bookingAutomationHost) Reset() {
	if host == nil {
		return
	}
	host.mu.Lock()
	parent := host.parent
	host.parent = nil
	host.winner = nil
	tabs := make([]*bookingTabAutomation, 0, len(host.tabs))
	for tab := range host.tabs {
		tabs = append(tabs, tab)
	}
	host.mu.Unlock()
	for _, tab := range tabs {
		tab.Close()
	}
	if parent != nil {
		parent.Close()
	}
}

func (host *bookingAutomationHost) Close() {
	if host == nil {
		return
	}
	host.mu.Lock()
	host.closed = true
	host.mu.Unlock()
	host.Reset()
}
