package main

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/cineko-org/client/internal/logging"
	observationpb "github.com/cineko-org/contracts/v3/gen/go/cineko/observation"
	"github.com/cineko-org/probe/v2/probe"
)

type dateScanSummary struct {
	Succeeded int `json:"succeeded"`
	Failed    int `json:"failed"`
	Partial   int `json:"partial"`
}

type theaterScanSummary struct {
	Started             int                        `json:"started"`
	Completed           int                        `json:"completed"`
	Succeeded           int                        `json:"succeeded"`
	Failed              int                        `json:"failed"`
	Partial             int                        `json:"partial"`
	Canceled            int                        `json:"canceled"`
	Throttled           int                        `json:"throttled"`
	NoDetailCapture     int                        `json:"no_detail_capture"`
	UnknownDateFailures int                        `json:"failures_without_target_date"`
	InFlight            int                        `json:"in_flight"`
	DurationMS          int64                      `json:"completed_duration_ms"`
	LastStartedAt       string                     `json:"last_started_at,omitempty"`
	LastCompletedAt     string                     `json:"last_completed_at,omitempty"`
	LastSuccessAt       string                     `json:"last_success_at,omitempty"`
	Dates               map[string]dateScanSummary `json:"dates"`
}

type monitoringSummary struct {
	mu              sync.Mutex
	windowStart     time.Time
	theaters        map[string]*theaterScanSummary
	activeMonitors  []string
	lastInventoryAt string
	discovered      map[string]int
}

type monitoringSummaryWindow struct {
	Start           time.Time
	End             time.Time
	Final           bool
	ActiveMonitors  []string
	LastInventoryAt string
	Theaters        map[string]theaterScanSummary
	Discovered      map[string]int
}

func newMonitoringSummary(now time.Time) *monitoringSummary {
	return &monitoringSummary{windowStart: now, theaters: make(map[string]*theaterScanSummary), discovered: make(map[string]int)}
}

func (summary *monitoringSummary) theater(id string) *theaterScanSummary {
	current := summary.theaters[id]
	if current == nil {
		current = &theaterScanSummary{Dates: make(map[string]dateScanSummary)}
		summary.theaters[id] = current
	}
	return current
}

func (summary *monitoringSummary) scanStarted(theaterID string, now time.Time) {
	if summary == nil {
		return
	}
	summary.mu.Lock()
	defer summary.mu.Unlock()
	current := summary.theater(theaterID)
	current.Started++
	current.InFlight++
	current.LastStartedAt = now.Format(time.RFC3339Nano)
}

// Results describe completed capture-and-storage work, not HTTP requests.
// If the scanner returned no target date on error, never invent one from the
// weekday inputs; retain that failure explicitly as unattributed.
func (summary *monitoringSummary) scanFinished(theaterID string, started, now time.Time, captures []*observationpb.Capture, err error, published bool) {
	if summary == nil {
		return
	}
	summary.mu.Lock()
	defer summary.mu.Unlock()
	current := summary.theater(theaterID)
	current.InFlight--
	current.Completed++
	current.DurationMS += now.Sub(started).Milliseconds()
	current.LastCompletedAt = now.Format(time.RFC3339Nano)
	complete, _, _ := scheduleCaptureCounts(captures)
	switch {
	case errors.Is(err, context.Canceled):
		current.Canceled++
	case errors.Is(err, probe.ErrProviderThrottled):
		current.Throttled++
	case err != nil:
		current.Failed++
	case complete != len(captures):
		current.Partial++
	default:
		current.Succeeded++
		current.LastSuccessAt = current.LastCompletedAt
		if len(captures) == 0 {
			// No detail observation was produced by this round.
			current.NoDetailCapture++
		}
	}
	if published && len(captures) > 0 {
		// Streamed dates have already been stored successfully. A later date's
		// throttle/cancel must not erase that evidence or relabel it as failure.
		err = nil
	} else if errors.Is(err, context.Canceled) || errors.Is(err, probe.ErrProviderThrottled) {
		return
	}
	current.recordDateResults(captures, err)
}

func (current *theaterScanSummary) recordDateResults(captures []*observationpb.Capture, err error) {
	knownDates := 0
	for _, capture := range captures {
		date := capture.GetTargetDate()
		if date == nil {
			continue
		}
		key := fmt.Sprintf("%04d-%02d-%02d", date.GetYear(), date.GetMonth(), date.GetDay())
		counts := current.Dates[key]
		switch {
		case err != nil:
			counts.Failed++
		case capture.GetComplete():
			counts.Succeeded++
		default:
			counts.Partial++
		}
		current.Dates[key] = counts
		knownDates++
	}
	if err != nil && knownDates == 0 {
		current.UnknownDateFailures++
	}
}

func (summary *monitoringSummary) inventory(active []string, now time.Time) {
	if summary == nil {
		return
	}
	summary.mu.Lock()
	defer summary.mu.Unlock()
	summary.activeMonitors = append([]string{}, active...)
	summary.lastInventoryAt = now.Format(time.RFC3339Nano)
}

func (summary *monitoringSummary) discovery(monitorID string) {
	if summary == nil {
		return
	}
	summary.mu.Lock()
	defer summary.mu.Unlock()
	summary.discovered[monitorID]++
}

func (summary *monitoringSummary) take(now time.Time, final bool) *monitoringSummaryWindow {
	summary.mu.Lock()
	defer summary.mu.Unlock()
	window := &monitoringSummaryWindow{Start: summary.windowStart, End: now, Final: final,
		ActiveMonitors: append([]string{}, summary.activeMonitors...), LastInventoryAt: summary.lastInventoryAt,
		Theaters: make(map[string]theaterScanSummary), Discovered: summary.discovered}
	changed := len(window.ActiveMonitors) > 0 || len(window.Discovered) > 0
	for id, current := range summary.theaters {
		window.Theaters[id] = *current
		changed = changed || current.Started > 0 || current.Completed > 0 || current.InFlight > 0
		summary.theaters[id] = &theaterScanSummary{InFlight: current.InFlight,
			LastStartedAt: current.LastStartedAt, LastCompletedAt: current.LastCompletedAt, LastSuccessAt: current.LastSuccessAt,
			Dates: make(map[string]dateScanSummary)}
	}
	summary.windowStart = now
	summary.discovered = make(map[string]int)
	if !changed {
		return nil
	}
	return window
}

func (summary *monitoringSummary) start(interval time.Duration) func() {
	if interval <= 0 {
		interval = 5 * time.Minute
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		flush := func(final bool) {
			window := summary.take(time.Now(), final)
			if window == nil {
				return
			}
			logging.Info(context.Background(), "monitoring.summary", "event", "monitoring.summary", "scenario", "booking_monitoring",
				"operation", "summarize_monitoring", "scope", "interval", "window_start", window.Start, "window_end", window.End,
				"final", final, "active_monitor_ids", window.ActiveMonitors, "last_inventory_at", window.LastInventoryAt,
				"theaters", window.Theaters, "new_showtimes_by_monitor", window.Discovered)
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
