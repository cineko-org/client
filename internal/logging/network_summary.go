package logging

import (
	"context"
	"time"

	"github.com/cineko-org/probe/v2/networkcapture"
)

// StartNetworkSummary retains bounded operational evidence without restoring
// per-request success logs or response bodies. Stop flushes before returning.
func StartNetworkSummary(store *networkcapture.Store, interval time.Duration) func() {
	if interval <= 0 {
		interval = 5 * time.Minute
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		var previous networkcapture.Statistics
		flush := func(final bool) {
			current := store.SessionStatistics()
			if !final && current == previous {
				return
			}
			previous = current
			Info(context.Background(), "network.statistics", "event", "network.statistics", "scenario", "network",
				"operation", "summarize_session_requests", "scope", "since_start_or_clear", "final", final,
				"completed", current.Captured, "provider_completed", current.ProviderSent,
				"blocked", current.Blocked, "canceled", current.Canceled, "failed", current.Failed, "status_429", current.Status429)
		}
		for {
			select {
			case <-ticker.C:
				flush(false)
			case <-ctx.Done():
				flush(true)
				return
			}
		}
	}()
	return func() { cancel(); <-done }
}
