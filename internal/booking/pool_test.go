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

type fakeWarmProcess struct {
	pid     int
	profile string
	pages   int
	ready   bool
	done    chan struct{}
	crash   chan error
	close   atomic.Int32
	kill    atomic.Int32
	onClose bool
	onKill  bool
	once    sync.Once
	waitErr error
}

func newFakeWarmProcess(pid int, profile string) *fakeWarmProcess {
	return &fakeWarmProcess{pid: pid, profile: profile, pages: 1, ready: true, done: make(chan struct{}), crash: make(chan error, 1), onClose: true}
}
func (process *fakeWarmProcess) PID() int              { return process.pid }
func (process *fakeWarmProcess) ProfileDir() string    { return process.profile }
func (process *fakeWarmProcess) PageCount() int        { return process.pages }
func (process *fakeWarmProcess) Ready() bool           { return process.ready }
func (process *fakeWarmProcess) Crashed() <-chan error { return process.crash }
func (process *fakeWarmProcess) Wait() error           { <-process.done; return process.waitErr }
func (process *fakeWarmProcess) Close() error {
	process.close.Add(1)
	if process.onClose {
		process.Exit()
	}
	return nil
}
func (process *fakeWarmProcess) KillTree() error {
	process.kill.Add(1)
	if process.onKill {
		process.Exit()
	}
	return nil
}
func (process *fakeWarmProcess) Exit() { process.once.Do(func() { close(process.done) }) }
func (process *fakeWarmProcess) ExitWith(err error) {
	process.waitErr = err
	process.Exit()
}
func (process *fakeWarmProcess) Crash(err error) { process.crash <- err }

func newTestWarmPool(t *testing.T, factory WarmBrowserProcessFactory) *WarmPool {
	t.Helper()
	pool, err := NewWarmPool(context.Background(), factory, WarmPoolConfig{
		CloseTimeout: 20 * time.Millisecond, KillTimeout: 20 * time.Millisecond,
		RestartBase: 5 * time.Millisecond, RestartMax: 20 * time.Millisecond,
	})
	if err != nil {
		t.Fatal(err)
	}
	return pool
}

func cleanupWarmPool(t *testing.T, pool *WarmPool) {
	t.Helper()
	if err := pool.Close(); err != nil {
		t.Errorf("warm pool close: %v", err)
	}
}

func waitWarm(t *testing.T, condition func() bool) {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if condition() {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("warm pool condition timed out")
}

func TestWarmPoolDemandDrivenAndNormalShutdown(t *testing.T) {
	var next atomic.Int32
	processes := make(chan *fakeWarmProcess, 4)
	pool := newTestWarmPool(t, func(context.Context, uint64) (WarmBrowserProcess, error) {
		process := newFakeWarmProcess(int(next.Add(1)), fmt.Sprintf("profile-%d", next.Load()))
		process.onClose = true
		processes <- process
		return process, nil
	})
	if stats := pool.Stats(); stats.Desired != 0 || stats.Creating != 0 {
		t.Fatalf("new pool started without demand: %+v", stats)
	}
	pool.SetDesired(DefaultWarmBrowserCapacity)
	first := make([]*WarmBrowserLease, 0, 2)
	for range 2 {
		lease, err := pool.Acquire(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		first = append(first, lease)
	}
	if first[0].Process().PID() == first[1].Process().PID() || first[0].Process().ProfileDir() == first[1].Process().ProfileDir() {
		t.Fatal("warm slots shared a process or profile")
	}
	for _, lease := range first {
		lease.Release()
	}
	waitWarm(t, func() bool { return pool.Stats().Ready == 2 })
	if err := pool.Close(); err != nil {
		t.Fatal(err)
	}
	if err := pool.Close(); err != nil {
		t.Fatal(err)
	}
	for len(processes) > 0 {
		process := <-processes
		if process.close.Load() == 0 {
			t.Fatal("process was not gracefully closed")
		}
	}
}

func TestWarmPoolReadyNotifierWakesExecutionOwner(t *testing.T) {
	pool := newTestWarmPool(t, func(context.Context, uint64) (WarmBrowserProcess, error) {
		return newFakeWarmProcess(9, "notify"), nil
	})
	t.Cleanup(func() { cleanupWarmPool(t, pool) })
	ready := make(chan struct{}, 1)
	pool.SetReadyNotifier(func() {
		select {
		case ready <- struct{}{}:
		default:
		}
	})
	pool.SetDesired(1)
	waitWarm(t, func() bool { return pool.Stats().Ready == 1 })
	select {
	case <-ready:
	case <-time.After(time.Second):
		t.Fatal("warm slot readiness did not notify execution owner")
	}
}

func TestWarmPoolCrashReplacementAndFailureBackoffReset(t *testing.T) {
	var next atomic.Int32
	var mu sync.Mutex
	created := make([]*fakeWarmProcess, 0, 4)
	pool := newTestWarmPool(t, func(context.Context, uint64) (WarmBrowserProcess, error) {
		process := newFakeWarmProcess(int(next.Add(1)), fmt.Sprintf("profile-%d", next.Load()))
		mu.Lock()
		created = append(created, process)
		mu.Unlock()
		return process, nil
	})
	t.Cleanup(func() { cleanupWarmPool(t, pool) })
	pool.SetDesired(1)
	waitWarm(t, func() bool { return pool.Stats().Ready == 1 })
	mu.Lock()
	first := created[0]
	mu.Unlock()
	first.Exit()
	waitWarm(t, func() bool {
		mu.Lock()
		count := len(created)
		mu.Unlock()
		return count >= 2 && pool.Stats().Ready == 1
	})
	// A successful replacement resets failure backoff; the next crash must not
	// inherit a delay from the first crash's slot sequence number.
	mu.Lock()
	second := created[1]
	mu.Unlock()
	started := time.Now()
	second.Exit()
	waitWarm(t, func() bool {
		mu.Lock()
		count := len(created)
		mu.Unlock()
		return count >= 3
	})
	if time.Since(started) > 100*time.Millisecond {
		t.Fatalf("replacement retained stale backoff: %s", time.Since(started))
	}
}

func TestWarmPoolStartupFailureCleansPartialProcess(t *testing.T) {
	partial := newFakeWarmProcess(41, "partial")
	partial.onClose = true
	var attempts atomic.Int32
	pool := newTestWarmPool(t, func(context.Context, uint64) (WarmBrowserProcess, error) {
		if attempts.Add(1) == 1 {
			return partial, errors.New("startup failed after process creation")
		}
		return newFakeWarmProcess(42, "ready"), nil
	})
	t.Cleanup(func() { cleanupWarmPool(t, pool) })
	pool.SetDesired(1)
	waitWarm(t, func() bool { return pool.Stats().Ready == 1 })
	if partial.close.Load() != 1 {
		t.Fatalf("partial process close calls = %d", partial.close.Load())
	}
}

func TestWarmPoolOnlyExplicitPermanentErrorsStopReplacement(t *testing.T) {
	var attempts atomic.Int32
	pool := newTestWarmPool(t, func(context.Context, uint64) (WarmBrowserProcess, error) {
		attempts.Add(1)
		return nil, fmt.Errorf("%w: credentials are not configured", ErrWarmPermanent)
	})
	pool.SetDesired(1)
	time.Sleep(50 * time.Millisecond)
	if got := attempts.Load(); got != 1 {
		t.Fatalf("permanent startup attempts = %d, want 1", got)
	}
	if err := pool.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestWarmPoolTimeoutForcesProcessTreeKill(t *testing.T) {
	process := newFakeWarmProcess(51, "timeout")
	process.onClose = false
	process.onKill = true
	pool := newTestWarmPool(t, func(context.Context, uint64) (WarmBrowserProcess, error) { return process, nil })
	pool.SetDesired(1)
	waitWarm(t, func() bool { return pool.Stats().Ready == 1 })
	if err := pool.Close(); err != nil {
		t.Fatal(err)
	}
	if process.close.Load() != 1 || process.kill.Load() != 1 {
		t.Fatalf("shutdown close/kill = %d/%d", process.close.Load(), process.kill.Load())
	}
}

func TestWarmPoolAsyncReapErrorIsReturnedByClose(t *testing.T) {
	process := newFakeWarmProcess(52, "unreaped")
	process.onClose = false
	pool := newTestWarmPool(t, func(context.Context, uint64) (WarmBrowserProcess, error) {
		return process, nil
	})
	pool.SetDesired(1)
	lease, err := pool.Acquire(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	lease.Release()
	err = pool.Close()
	if !errors.Is(err, ErrWarmProcessReap) {
		t.Fatalf("Close() error = %v, want async reap error", err)
	}
	if !errors.Is(pool.ReapError(), ErrWarmProcessReap) {
		t.Fatalf("ReapError() = %v, want async reap error", pool.ReapError())
	}
}

func TestWarmPoolConcurrentShutdownAndAcquire(t *testing.T) {
	pool := newTestWarmPool(t, func(ctx context.Context, _ uint64) (WarmBrowserProcess, error) {
		<-ctx.Done()
		return nil, ctx.Err()
	})
	pool.SetDesired(1)
	result := make(chan error, 1)
	go func() { _, err := pool.Acquire(context.Background()); result <- err }()
	time.Sleep(5 * time.Millisecond)
	if err := pool.Close(); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-result:
		if !errors.Is(err, ErrWarmPoolClosed) {
			t.Fatalf("Acquire after concurrent shutdown = %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("Acquire remained blocked after shutdown")
	}
}

func TestWarmPoolPaymentRetentionNeverReusesBusyProcess(t *testing.T) {
	var next atomic.Int32
	pool := newTestWarmPool(t, func(context.Context, uint64) (WarmBrowserProcess, error) {
		id := int(next.Add(1))
		return newFakeWarmProcess(id, fmt.Sprintf("profile-%d", id)), nil
	})
	t.Cleanup(func() { cleanupWarmPool(t, pool) })
	pool.SetDesired(1)
	lease, err := pool.Acquire(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	retained := lease.Process()
	if err := lease.RetainPayment(); err != nil {
		t.Fatal(err)
	}
	acquireCtx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	if _, err := pool.Acquire(acquireCtx); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("Acquire while payment retained = %v", err)
	}
	lease.Release() // retained leases cannot accidentally become reusable
	lease.ReleasePayment()
	nextLease, err := pool.Acquire(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if nextLease.Process().PID() == retained.PID() {
		t.Fatal("payment-retained process was reused")
	}
	nextLease.Release()
}

func TestWarmPoolCrashSignalsLeaseOwner(t *testing.T) {
	process := newFakeWarmProcess(55, "crashed")
	pool := newTestWarmPool(t, func(context.Context, uint64) (WarmBrowserProcess, error) { return process, nil })
	t.Cleanup(func() { cleanupWarmPool(t, pool) })
	pool.SetDesired(1)
	lease, err := pool.Acquire(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	crash := errors.New("browser crashed")
	process.ExitWith(crash)
	select {
	case <-lease.Done():
		if !errors.Is(lease.Err(), crash) {
			t.Fatalf("lease crash error = %v", lease.Err())
		}
	case <-time.After(time.Second):
		t.Fatal("lease owner was not notified of process crash")
	}
}

func TestWarmPoolCrashSignalWaitsForDriverReap(t *testing.T) {
	process := newFakeWarmProcess(56, "crash-signal")
	process.onClose = false
	pool := newTestWarmPool(t, func(context.Context, uint64) (WarmBrowserProcess, error) { return process, nil })
	t.Cleanup(func() { cleanupWarmPool(t, pool) })
	pool.SetDesired(1)
	lease, err := pool.Acquire(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	process.Crash(errors.New("context crashed"))
	select {
	case <-lease.Done():
	case <-time.After(time.Second):
		t.Fatal("crash signal did not fail lease closed")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Millisecond)
	if _, err := pool.Acquire(ctx); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("replacement appeared before driver reap: %v", err)
	}
	cancel()
	// The replacement is only created after the old driver's Wait/reap path
	// has completed; the fake closes that path explicitly here.
	process.Exit()
	lease2, err := pool.Acquire(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	lease2.Release()
}

func TestWarmPoolDemandDecreaseRetiresOnlyIdleSlots(t *testing.T) {
	var next atomic.Int32
	pool := newTestWarmPool(t, func(context.Context, uint64) (WarmBrowserProcess, error) {
		id := int(next.Add(1))
		process := newFakeWarmProcess(id, fmt.Sprintf("profile-%d", id))
		process.onClose = true
		return process, nil
	})
	t.Cleanup(func() { cleanupWarmPool(t, pool) })
	pool.SetDesired(2)
	busy, err := pool.Acquire(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	pool.SetDesired(0)
	if stats := pool.Stats(); stats.Busy != 1 {
		t.Fatalf("busy slot retired during demand decrease: %+v", stats)
	}
	busy.Release()
	waitWarm(t, func() bool { return pool.Stats().Ready == 0 && pool.Stats().Busy == 0 })
}

func TestWarmPoolRejectsDuplicateProcessIdentity(t *testing.T) {
	var attempts atomic.Int32
	pool := newTestWarmPool(t, func(context.Context, uint64) (WarmBrowserProcess, error) {
		attempts.Add(1)
		return newFakeWarmProcess(71, "same-profile"), nil
	})
	t.Cleanup(func() { cleanupWarmPool(t, pool) })
	pool.SetDesired(2)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	if _, err := pool.Acquire(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Acquire(ctx); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("duplicate identity was admitted or wrong error: %v", err)
	}
}

func TestWarmPoolValidatesOnePageIsolatedAuthenticatedProcesses(t *testing.T) {
	invalid := newFakeWarmProcess(61, "invalid")
	invalid.pages = 2
	invalid.onClose = true
	var attempts atomic.Int32
	pool := newTestWarmPool(t, func(context.Context, uint64) (WarmBrowserProcess, error) {
		if attempts.Add(1) == 1 {
			return invalid, nil
		}
		return newFakeWarmProcess(62, "isolated-profile"), nil
	})
	t.Cleanup(func() { cleanupWarmPool(t, pool) })
	pool.SetDesired(1)
	lease, err := pool.Acquire(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if lease.Process().PageCount() != 1 || !lease.Process().Ready() || lease.Process().ProfileDir() == "" {
		t.Fatal("pool returned a process without readiness invariants")
	}
	lease.Release()
}
