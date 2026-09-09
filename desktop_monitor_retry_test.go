package main

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/cineko-org/client/internal/adapters/cgv"
	catalogpb "github.com/cineko-org/contracts/v3/gen/go/cineko/catalog"
	clientpb "github.com/cineko-org/contracts/v3/gen/go/cineko/client"
)

type failingMonitorServer struct{ calls int }

func (*failingMonitorServer) CanAcceptExecution() bool                  { return true }
func (*failingMonitorServer) RecordLocalSystemEvent(*clientpb.AppEvent) {}
func (s *failingMonitorServer) ExecuteAvailability(context.Context, string, *catalogpb.Showtime, bool) error {
	s.calls++
	return errors.New("provider temporarily unavailable")
}

func TestMonitorFailureDoesNotRestartEveryTick(t *testing.T) {
	server := &failingMonitorServer{}
	worker := &desktopMonitorWorker{server: server}
	runtime := &localMonitorRuntime{worker: worker, active: map[string]*localMonitorExecution{}, done: make(chan localMonitorResult, 1)}
	target := testLocalMonitorTarget("round", time.Now().Add(time.Hour))
	target.watchCancellations = true
	// Repeated catalog notifications can arrive faster than the 1-second tick.
	// A failure must not turn each notification into another browser navigation.
	for range 60 {
		runtime.startExecutions(t.Context(), []*localMonitorTarget{target})
		if len(runtime.active) != 0 {
			runtime.handleResult(t.Context(), <-runtime.done)
		}
	}
	t.Logf("60 immediate scheduler notifications, failed browser starts=%d", server.calls)
	if server.calls != 1 {
		t.Fatalf("failed browser restarted %d times; want 1 until retry deadline", server.calls)
	}
	// Once the deadline has elapsed there must be a real retry, not a stall.
	key := localMonitorTargetKey(target)
	retry := runtime.retries[key]
	retry.after = time.Now().Add(-time.Second)
	runtime.retries[key] = retry
	runtime.startExecutions(t.Context(), []*localMonitorTarget{target})
	runtime.handleResult(t.Context(), <-runtime.done)
	if server.calls != 2 || runtime.retries[key].failures != 2 {
		t.Fatalf("retry did not resume with increasing backoff: calls=%d retry=%+v", server.calls, runtime.retries[key])
	}
	if remaining := time.Until(runtime.retries[key].after); remaining < 59*time.Second {
		t.Fatalf("second failure backoff=%s; want 60s", remaining)
	}
}

func TestNewScheduleTransientFailureRetainsIntent(t *testing.T) {
	worker := &desktopMonitorWorker{}
	target := testLocalMonitorTarget("new-round", time.Now().Add(time.Hour))
	target.signal = localMonitorSignalNewSchedule
	execution := &localMonitorExecution{target: target, cancel: func() {}}
	key := localMonitorTargetKey(target)
	runtime := &localMonitorRuntime{worker: worker, active: map[string]*localMonitorExecution{key: execution}}
	runtime.handleResult(t.Context(), localMonitorResult{monitorID: target.monitorID, showtimeID: target.showtime.GetId(), signal: target.signal, execution: execution, err: errors.New("temporary provider failure")})
	if _, pending := worker.newTargets[key]; !pending {
		t.Fatal("new schedule lost after transient failure")
	}
}

func TestSharedQueryPauseRetainsNewScheduleAndUsesSharedDeadline(t *testing.T) {
	worker := &desktopMonitorWorker{}
	target := testLocalMonitorTarget("new-round", time.Now().Add(time.Hour))
	target.signal = localMonitorSignalNewSchedule
	execution := &localMonitorExecution{target: target, cancel: func() {}}
	key := localMonitorTargetKey(target)
	runtime := &localMonitorRuntime{worker: worker, active: map[string]*localMonitorExecution{key: execution}}
	paused := &bookingQueryPausedError{after: time.Now().Add(time.Minute), cause: cgv.ErrUIContractChanged}
	runtime.handleResult(t.Context(), localMonitorResult{monitorID: target.monitorID, showtimeID: target.showtime.GetId(), signal: target.signal, execution: execution, err: paused})
	if _, pending := worker.newTargets[key]; !pending {
		t.Fatal("shared pause consumed the new schedule")
	}
	if !runtime.retries[key].after.Equal(paused.after) || runtime.retries[key].failures != 0 {
		t.Fatal("queued round created a separate failure/backoff")
	}
}
