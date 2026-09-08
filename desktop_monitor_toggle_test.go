package main

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	catalogpb "github.com/cineko-org/contracts/v3/gen/go/cineko/catalog"
	clientpb "github.com/cineko-org/contracts/v3/gen/go/cineko/client"
)

type toggleMonitorStore struct {
	*monitorExecutionStore
	enabled atomic.Bool
}

func (store *toggleMonitorStore) ListMonitorsByUser(ctx context.Context, user string) ([]*clientpb.Resource, error) {
	if !store.enabled.Load() {
		return nil, nil
	}
	return store.monitorExecutionStore.ListMonitorsByUser(ctx, user)
}

type toggleExecutionServer struct {
	changed  chan struct{}
	started  chan time.Time
	canceled chan time.Time
}

func (server *toggleExecutionServer) ExecutionAvailable() <-chan struct{} { return server.changed }
func (*toggleExecutionServer) CanAcceptExecution() bool                   { return true }
func (*toggleExecutionServer) RecordLocalSystemEvent(*clientpb.AppEvent)  {}
func (server *toggleExecutionServer) ExecuteAvailability(ctx context.Context, _ string, _ *catalogpb.Showtime, _ bool) error {
	server.started <- time.Now()
	<-ctx.Done()
	server.canceled <- time.Now()
	return ctx.Err()
}

// Same execution boundary without change events reproduces the old tick-only path.
type tickOnlyExecutionServer struct{ localExecutionServer }

func TestMonitorToggleWithoutRestart(t *testing.T) {
	for _, events := range []bool{false, true} {
		name := "before_tick_only"
		if events {
			name = "after_change_event"
		}
		t.Run(name, func(t *testing.T) {
			store := &toggleMonitorStore{monitorExecutionStore: newMonitorExecutionStore(true, "showtime")}
			server := &toggleExecutionServer{changed: make(chan struct{}, 1), started: make(chan time.Time, 8), canceled: make(chan time.Time, 8)}
			var execution localExecutionServer = server
			if !events {
				execution = tickOnlyExecutionServer{server}
			}
			worker := &desktopMonitorWorker{store: store, server: execution}
			ctx, cancel := context.WithCancel(t.Context())
			done := make(chan error, 1)
			go func() { done <- worker.Run(ctx) }()
			defer func() {
				cancel()
				if err := <-done; err != nil {
					t.Error(err)
				}
			}()
			wait := func(ch <-chan time.Time) time.Time {
				t.Helper()
				select {
				case at := <-ch:
					return at
				case <-time.After(3 * time.Second):
					t.Fatal("toggle did not reach execution boundary")
					return time.Time{}
				}
			}
			for round := range 2 {
				start := time.Now()
				store.enabled.Store(true)
				if events {
					server.changed <- struct{}{}
				}
				startDelay := wait(server.started).Sub(start)
				stop := time.Now()
				store.enabled.Store(false)
				if events {
					server.changed <- struct{}{}
				}
				stopDelay := wait(server.canceled).Sub(stop)
				t.Logf("round=%d start_ms=%.3f stop_ms=%.3f", round+1, float64(startDelay)/float64(time.Millisecond), float64(stopDelay)/float64(time.Millisecond))
				if events && (startDelay >= 500*time.Millisecond || stopDelay >= 500*time.Millisecond) {
					t.Fatal("toggle waited for fallback tick")
				}
			}
		})
	}
}

func TestMonitorStopCancelsOnlyMonitorScanAndCanResume(t *testing.T) {
	embedded := &embeddedProbe{}
	for range 2 {
		ctx, finish := embedded.beginMonitorScan(t.Context())
		if ctx.Err() != nil {
			t.Fatal("fresh scan inherited stopped state")
		}
		embedded.CancelMonitorScan()
		if ctx.Err() != context.Canceled {
			t.Fatal("active monitor scan survived stop")
		}
		if t.Context().Err() != nil {
			t.Fatal("stop canceled parent/catalog context")
		}
		finish()
	}
}
