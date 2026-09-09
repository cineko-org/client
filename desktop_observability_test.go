package main

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/cineko-org/client/internal/adapters/cgv"
	"github.com/cineko-org/client/internal/application"
	"github.com/cineko-org/client/internal/booking"
	"github.com/cineko-org/client/internal/logging"
	catalogpb "github.com/cineko-org/contracts/v3/gen/go/cineko/catalog"
)

func TestNormalEmptySchedulesDoNotFloodWarnings(t *testing.T) {
	var output strings.Builder
	restore := logging.SetOutput(&output)
	defer restore()
	disableDebug := logging.SetDebug(false)
	defer disableDebug()
	for range 1595 {
		logScheduleCaptureHealth(context.Background(), "yongsan", 1, 1, 0, 0, []int32{5, 6, 0})
	}
	if output.Len() != 0 {
		t.Fatalf("normal empties produced logs: %s", output.String())
	}
	t.Log("same 1595 complete empty captures: previous WARN=1595, current WARN=0")
	logScheduleCaptureHealth(context.Background(), "yongsan", 1, 0, 0, 0, []int32{5})
	logScheduleCaptureHealth(context.Background(), "yongsan", 1, 1, 3, 0, []int32{5})
	if strings.Count(output.String(), `"level":"WARN"`) != 2 {
		t.Fatalf("unexpected captures not reported: %s", output.String())
	}
}

type observabilityProcess struct {
	done chan struct{}
	once sync.Once
}

func (*observabilityProcess) PID() int              { return 123 }
func (*observabilityProcess) ProfileDir() string    { return "test-profile" }
func (*observabilityProcess) PageCount() int        { return 1 }
func (*observabilityProcess) Ready() bool           { return true }
func (*observabilityProcess) Crashed() <-chan error { return nil }
func (p *observabilityProcess) Close() error        { p.once.Do(func() { close(p.done) }); return nil }
func (p *observabilityProcess) KillTree() error     { return p.Close() }
func (p *observabilityProcess) Wait() error         { <-p.done; return nil }

func TestWinningOrClosedBookingHostDoesNotAcceptSpareCapacity(t *testing.T) {
	pool, err := booking.NewPool(context.Background(), func(context.Context, uint64) (booking.Process, error) {
		return &observabilityProcess{done: make(chan struct{})}, nil
	}, booking.Config{InitialDesired: 1, MaxCapacity: 1})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = pool.Close() }()
	deadline := time.Now().Add(time.Second)
	for pool.Stats().Ready != 1 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if pool.Stats().Ready != 1 {
		t.Fatal("fake browser was not admitted")
	}
	host := &bookingAutomationHost{pool: pool}
	if !host.CanAccept() {
		t.Fatal("ready capacity unavailable")
	}
	host.winner = &bookingTabAutomation{}
	if host.CanAccept() {
		t.Fatal("payment winner allowed another booking")
	}
	host.winner, host.closed = nil, true
	if host.CanAccept() {
		t.Fatal("closed host accepted spare capacity")
	}
}

func TestMonitorUnavailableResultRemainsVisibleWithoutDebug(t *testing.T) {
	var output strings.Builder
	restore := logging.SetOutput(&output)
	defer restore()
	disableDebug := logging.SetDebug(false)
	defer disableDebug()
	logLocalMonitorFailure(context.Background(), localMonitorResult{monitorID: "monitor", showtimeID: "round", signal: localMonitorSignalNewSchedule, err: application.ErrSeatUnavailable})
	if !strings.Contains(output.String(), `"outcome":"unavailable"`) || !strings.Contains(output.String(), `"level":"INFO"`) {
		t.Fatalf("terminal result missing: %s", output.String())
	}
}

func TestMonitorWaitingIsLoggedOnlyOnTransition(t *testing.T) {
	var output strings.Builder
	restore := logging.SetOutput(&output)
	defer restore()
	runtime := &localMonitorRuntime{worker: &desktopMonitorWorker{server: monitorWakeServer{}}, active: make(map[string]*localMonitorExecution)}
	id := "round"
	targets := []*localMonitorTarget{{monitorID: "monitor", signal: localMonitorSignalNewSchedule, showtime: catalogpb.Showtime_builder{Id: &id}.Build()}}
	for range 100 {
		runtime.startExecutions(context.Background(), targets)
	}
	if strings.Count(output.String(), `"level":"WARN"`) != 1 {
		t.Fatalf("waiting log not deduplicated: %s", output.String())
	}
}

func TestAuthenticationFailurePreservesNewScheduleForAfterLogin(t *testing.T) {
	worker := &desktopMonitorWorker{server: monitorWakeServer{}}
	id := "round"
	target := &localMonitorTarget{monitorID: "monitor", signal: localMonitorSignalNewSchedule, showtime: catalogpb.Showtime_builder{Id: &id}.Build()}
	execution := &localMonitorExecution{target: target, cancel: func() {}}
	key := localMonitorTargetKey(target)
	runtime := &localMonitorRuntime{worker: worker, active: map[string]*localMonitorExecution{key: execution}}
	runtime.handleResult(context.Background(), localMonitorResult{monitorID: "monitor", showtimeID: id, signal: localMonitorSignalNewSchedule, execution: execution, err: cgv.ErrAuthenticationRequired})
	if _, pending := worker.newTargets[key]; !pending {
		t.Fatal("authentication failure consumed the only new-schedule signal")
	}
}
