package booking

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type fakeProcess struct {
	pid     int
	profile string
	pages   int
	ready   bool

	crashed      chan error
	exited       chan struct{}
	waitReturned chan struct{}
	closeCalled  chan struct{}

	closeExits   bool
	closeBlocks  bool
	closeUnblock chan struct{}
	killExits    bool
	waitErr      error
	closeErr     error
	killErr      error

	exitOnce        sync.Once
	waitReturnOnce  sync.Once
	closeSignalOnce sync.Once
	waitCalls       atomic.Int32
	closeCalls      atomic.Int32
	killCalls       atomic.Int32
}

func newFakeProcess(pid int, profile string) *fakeProcess {
	return &fakeProcess{
		pid: pid, profile: profile, pages: 1, ready: true,
		crashed: make(chan error, 1), exited: make(chan struct{}),
		waitReturned: make(chan struct{}), closeCalled: make(chan struct{}),
		closeExits: true, killExits: true,
	}
}

func (process *fakeProcess) PID() int              { return process.pid }
func (process *fakeProcess) ProfileDir() string    { return process.profile }
func (process *fakeProcess) PageCount() int        { return process.pages }
func (process *fakeProcess) Ready() bool           { return process.ready }
func (process *fakeProcess) Crashed() <-chan error { return process.crashed }
func (process *fakeProcess) Wait() error {
	process.waitCalls.Add(1)
	<-process.exited
	process.waitReturnOnce.Do(func() { close(process.waitReturned) })
	return process.waitErr
}
func (process *fakeProcess) Close() error {
	process.closeCalls.Add(1)
	process.closeSignalOnce.Do(func() { close(process.closeCalled) })
	if process.closeBlocks {
		<-process.closeUnblock
	}
	if process.closeExits {
		process.exitOnce.Do(func() { close(process.exited) })
	}
	return process.closeErr
}
func (process *fakeProcess) KillTree() error {
	process.killCalls.Add(1)
	if process.closeBlocks && process.closeUnblock != nil {
		select {
		case <-process.closeUnblock:
		default:
			close(process.closeUnblock)
		}
	}
	if process.killExits {
		process.exitOnce.Do(func() { close(process.exited) })
	}
	return process.killErr
}

func testPoolConfig() Config {
	return Config{
		MaxCapacity:  3,
		CloseTimeout: 20 * time.Millisecond,
		KillTimeout:  20 * time.Millisecond,
		RestartBase:  5 * time.Millisecond,
		RestartMax:   20 * time.Millisecond,
	}
}

func waitForStats(t *testing.T, pool *Pool, want func(Stats) bool) Stats {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		stats := pool.Stats()
		if want(stats) {
			return stats
		}
		time.Sleep(time.Millisecond)
	}
	stats := pool.Stats()
	t.Fatalf("pool did not reach expected state: %+v", stats)
	return stats
}

func TestPoolIsDemandDrivenAndReplenishesIsolatedSlots(t *testing.T) {
	var creates atomic.Int32
	var nextPID atomic.Int32
	nextPID.Store(100)
	pool, err := NewPool(context.Background(), func(_ context.Context, sequence uint64) (Process, error) {
		creates.Add(1)
		return newFakeProcess(int(nextPID.Add(1)), fmt.Sprintf("profile-%d", sequence)), nil
	}, testPoolConfig())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = pool.Close() }()

	if stats := pool.Stats(); stats.Desired != 0 || stats.Ready != 0 || creates.Load() != 0 {
		t.Fatalf("pool created a process without demand: stats=%+v creates=%d", stats, creates.Load())
	}

	pool.SetDesired(2)
	waitForStats(t, pool, func(stats Stats) bool { return stats.Ready == 2 })
	first, err := pool.Acquire(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	second, err := pool.Acquire(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if first.Process().PID() == second.Process().PID() || first.Process().ProfileDir() == second.Process().ProfileDir() {
		t.Fatal("two leases share an isolated process")
	}
	first.Release()
	second.Release()
	waitForStats(t, pool, func(stats Stats) bool { return stats.Ready == 2 && stats.Busy == 0 })
	if creates.Load() < 4 {
		t.Fatalf("released slots were not replenished: creates=%d", creates.Load())
	}
}

func TestPoolCapsDemandAtThree(t *testing.T) {
	var creates atomic.Int32
	var nextPID atomic.Int32
	nextPID.Store(200)
	pool, err := NewPool(context.Background(), func(_ context.Context, sequence uint64) (Process, error) {
		creates.Add(1)
		return newFakeProcess(int(nextPID.Add(1)), fmt.Sprintf("cap-%d", sequence)), nil
	}, testPoolConfig())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = pool.Close() }()

	pool.SetDesired(99)
	stats := waitForStats(t, pool, func(stats Stats) bool { return stats.Ready == MaximumWarmBrowserCapacity })
	if stats.Desired != MaximumWarmBrowserCapacity || creates.Load() != MaximumWarmBrowserCapacity {
		t.Fatalf("unexpected cap: stats=%+v creates=%d", stats, creates.Load())
	}
}

func TestPoolPrewarmsMissingSlotsConcurrently(t *testing.T) {
	var creates atomic.Int32
	var inFlight atomic.Int32
	var maxInFlight atomic.Int32
	var nextPID atomic.Int32
	nextPID.Store(250)
	starts := make(chan struct{}, 2)
	release := make(chan struct{})
	p, err := NewPool(context.Background(), func(_ context.Context, sequence uint64) (Process, error) {
		creates.Add(1)
		current := inFlight.Add(1)
		for {
			previous := maxInFlight.Load()
			if current <= previous || maxInFlight.CompareAndSwap(previous, current) {
				break
			}
		}
		starts <- struct{}{}
		<-release
		inFlight.Add(-1)
		return newFakeProcess(int(nextPID.Add(1)), fmt.Sprintf("parallel-%d", sequence)), nil
	}, Config{InitialDesired: 2, MaxCapacity: 3, CloseTimeout: 20 * time.Millisecond, KillTimeout: 20 * time.Millisecond})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = p.Close() }()

	for range 2 {
		select {
		case <-starts:
		case <-time.After(time.Second):
			t.Fatal("second warm slot did not start while first was blocked")
		}
	}
	if got := maxInFlight.Load(); got != 2 {
		t.Fatalf("warm slot creation remained serial: max in-flight=%d", got)
	}
	close(release)
	waitForStats(t, p, func(stats Stats) bool { return stats.Ready == 2 })
	if creates.Load() != 2 {
		t.Fatalf("unexpected warm slot creation count: %d", creates.Load())
	}
}

func TestPaymentRetentionBlocksReplacementUntilExplicitRelease(t *testing.T) {
	var creates atomic.Int32
	nextPID := 300
	pool, err := NewPool(context.Background(), func(_ context.Context, sequence uint64) (Process, error) {
		creates.Add(1)
		nextPID++
		return newFakeProcess(nextPID, fmt.Sprintf("payment-%d", sequence)), nil
	}, Config{InitialDesired: 1, MaxCapacity: 1, CloseTimeout: 20 * time.Millisecond, KillTimeout: 20 * time.Millisecond})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = pool.Close() }()

	waitForStats(t, pool, func(stats Stats) bool { return stats.Ready == 1 })
	lease, err := pool.Acquire(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if err := lease.RetainPayment(); err != nil {
		t.Fatal(err)
	}
	if stats := pool.Stats(); stats.Retained != 1 {
		t.Fatalf("payment lease was not retained: %+v", stats)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()
	if _, err := pool.Acquire(ctx); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("acquire ignored payment retention: %v", err)
	}
	if creates.Load() != 1 {
		t.Fatalf("payment retention started a replacement: %d", creates.Load())
	}
	lease.ReleasePayment()
	waitForStats(t, pool, func(stats Stats) bool { return stats.Ready == 1 && stats.Retained == 0 })
	if creates.Load() != 2 {
		t.Fatalf("payment release did not replenish: %d", creates.Load())
	}
}

func TestCrashFailsLeaseBeforeWaitAndReplenishesAfterReap(t *testing.T) {
	var creates atomic.Int32
	nextPID := 400
	var first *fakeProcess
	pool, err := NewPool(context.Background(), func(_ context.Context, sequence uint64) (Process, error) {
		nextPID++
		process := newFakeProcess(nextPID, fmt.Sprintf("crash-%d", sequence))
		if creates.Load() == 0 {
			first = process
		}
		creates.Add(1)
		return process, nil
	}, Config{InitialDesired: 1, MaxCapacity: 1, CloseTimeout: 20 * time.Millisecond, KillTimeout: 20 * time.Millisecond})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = pool.Close() }()

	waitForStats(t, pool, func(stats Stats) bool { return stats.Ready == 1 })
	first.closeExits = false
	lease, err := pool.Acquire(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	crashErr := errors.New("browser crashed")
	first.crashed <- crashErr
	select {
	case <-lease.Done():
	case <-time.After(time.Second):
		t.Fatal("crashed lease did not fail")
	}
	if !errors.Is(lease.Err(), crashErr) {
		t.Fatalf("crashed lease did not retain failure: %v", lease.Err())
	}
	if creates.Load() != 1 {
		t.Fatalf("replacement started before the process was reaped: %d", creates.Load())
	}
	select {
	case <-first.closeCalled:
	case <-time.After(time.Second):
		t.Fatal("crashed process was not asked to close before reaping")
	}
	first.exitOnce.Do(func() { close(first.exited) })
	select {
	case <-first.waitReturned:
	case <-time.After(time.Second):
		t.Fatal("crashed process did not return from Wait")
	}
	waitForStats(t, pool, func(stats Stats) bool { return stats.Ready == 1 })
	if creates.Load() != 2 {
		t.Fatalf("crashed process was not replaced: %d", creates.Load())
	}
	if first.waitCalls.Load() != 1 {
		t.Fatalf("crashed process was waited more than once: %d", first.waitCalls.Load())
	}
	if first.killCalls.Load() != 0 {
		t.Fatalf("crashed process needed tree kill after graceful close and Wait: %d", first.killCalls.Load())
	}
	if err := pool.ReapError(); err != nil {
		t.Fatalf("crashed process reported a reap error after Wait returned: %v", err)
	}
}

func TestPermanentStartupFailureDoesNotRetry(t *testing.T) {
	var creates atomic.Int32
	pool, err := NewPool(context.Background(), func(context.Context, uint64) (Process, error) {
		creates.Add(1)
		return nil, ErrPermanent
	}, Config{InitialDesired: 1, MaxCapacity: 1, RestartBase: time.Millisecond, RestartMax: time.Millisecond})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = pool.Close() }()
	time.Sleep(20 * time.Millisecond)
	if creates.Load() != 1 || pool.Stats().Ready != 0 {
		t.Fatalf("permanent startup failure retried or admitted: creates=%d stats=%+v", creates.Load(), pool.Stats())
	}
}

func TestTerminationFallsBackToTreeKill(t *testing.T) {
	process := newFakeProcess(500, "kill")
	process.closeExits = false
	process.killExits = true
	closeErr := errors.New("graceful close failed")
	process.closeErr = closeErr
	pool, err := NewPool(context.Background(), func(context.Context, uint64) (Process, error) {
		return process, nil
	}, Config{InitialDesired: 1, MaxCapacity: 1, CloseTimeout: time.Millisecond, KillTimeout: 20 * time.Millisecond})
	if err != nil {
		t.Fatal(err)
	}
	waitForStats(t, pool, func(stats Stats) bool { return stats.Ready == 1 })
	if err := pool.Close(); !errors.Is(err, closeErr) {
		t.Fatalf("Pool.Close() error = %v, want %v", err, closeErr)
	}
	if process.closeCalls.Load() == 0 || process.killCalls.Load() == 0 || process.waitCalls.Load() != 1 {
		t.Fatalf("termination did not close, kill, and reap exactly once: close=%d kill=%d wait=%d", process.closeCalls.Load(), process.killCalls.Load(), process.waitCalls.Load())
	}
}

func TestTerminationDoesNotKillAfterWaitDespiteCloseError(t *testing.T) {
	process := newFakeProcess(525, "close-error-after-wait")
	process.closeErr = errors.New("context close reported an error")
	pool, err := NewPool(context.Background(), func(context.Context, uint64) (Process, error) {
		return process, nil
	}, Config{InitialDesired: 1, MaxCapacity: 1, CloseTimeout: 20 * time.Millisecond, KillTimeout: 20 * time.Millisecond})
	if err != nil {
		t.Fatal(err)
	}
	waitForStats(t, pool, func(stats Stats) bool { return stats.Ready == 1 })
	if err := pool.Close(); !errors.Is(err, process.closeErr) {
		t.Fatalf("Pool.Close() = %v, want %v", err, process.closeErr)
	}
	if process.killCalls.Load() != 0 || process.waitCalls.Load() != 1 {
		t.Fatalf("reaped close-error lifecycle = kill:%d wait:%d", process.killCalls.Load(), process.waitCalls.Load())
	}
}

func TestTerminationKillUnblocksCloseWithoutWaitLeak(t *testing.T) {
	process := newFakeProcess(550, "kill-unblocks-close")
	process.closeBlocks = true
	process.closeExits = false
	process.closeUnblock = make(chan struct{})
	pool, err := NewPool(context.Background(), func(context.Context, uint64) (Process, error) {
		return process, nil
	}, Config{InitialDesired: 1, MaxCapacity: 1, CloseTimeout: time.Millisecond, KillTimeout: 20 * time.Millisecond})
	if err != nil {
		t.Fatal(err)
	}
	waitForStats(t, pool, func(stats Stats) bool { return stats.Ready == 1 })
	if err := pool.Close(); err != nil {
		t.Fatalf("Pool.Close() = %v", err)
	}
	if process.killCalls.Load() != 1 || process.waitCalls.Load() != 1 {
		t.Fatalf("forced close lifecycle = kill:%d wait:%d", process.killCalls.Load(), process.waitCalls.Load())
	}
}

func TestInvalidProcessIsRejected(t *testing.T) {
	process := newFakeProcess(0, "invalid")
	pool, err := NewPool(context.Background(), func(context.Context, uint64) (Process, error) {
		return process, nil
	}, Config{InitialDesired: 1, MaxCapacity: 1, RestartBase: time.Hour})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = pool.Close() }()
	time.Sleep(10 * time.Millisecond)
	if pool.Stats().Ready != 0 || process.closeCalls.Load() == 0 {
		t.Fatalf("invalid process was admitted: stats=%+v close=%d", pool.Stats(), process.closeCalls.Load())
	}
}

func TestParentCancellationClosesPoolAndOwnedProcess(t *testing.T) {
	parent, cancel := context.WithCancel(context.Background())
	process := newFakeProcess(600, "parent-cancel")
	pool, err := NewPool(parent, func(context.Context, uint64) (Process, error) {
		return process, nil
	}, Config{InitialDesired: 1, MaxCapacity: 1, CloseTimeout: 20 * time.Millisecond, KillTimeout: 20 * time.Millisecond})
	if err != nil {
		t.Fatal(err)
	}
	waitForStats(t, pool, func(stats Stats) bool { return stats.Ready == 1 })
	cancel()
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		probeContext, probeCancel := context.WithTimeout(context.Background(), 5*time.Millisecond)
		_, acquireErr := pool.Acquire(probeContext)
		probeCancel()
		if errors.Is(acquireErr, ErrPoolClosed) {
			if process.closeCalls.Load() > 0 {
				return
			}
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("parent cancellation did not close the pool")
}

func TestPoolCloseHasBoundedFactoryShutdown(t *testing.T) {
	started := make(chan struct{})
	release := make(chan struct{})
	pool, err := NewPool(context.Background(), func(context.Context, uint64) (Process, error) {
		close(started)
		<-release
		return nil, ErrPermanent
	}, Config{InitialDesired: 1, MaxCapacity: 1, ShutdownTimeout: 20 * time.Millisecond})
	if err != nil {
		t.Fatal(err)
	}
	pool.SetDesired(1)
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("factory did not start")
	}
	startedAt := time.Now()
	if err := pool.Close(); !errors.Is(err, ErrShutdownTimeout) {
		t.Fatalf("Pool.Close() = %v, want shutdown timeout", err)
	}
	if elapsed := time.Since(startedAt); elapsed > 300*time.Millisecond {
		t.Fatalf("Pool.Close() exceeded bound: %s", elapsed)
	}
	close(release)
}
