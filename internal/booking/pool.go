// Package booking owns Client-local booking session capacity and browser
// process lifecycle. Adapters provide concrete browser processes; this
// package owns demand, leasing, retention, crash replacement, and reaping.
package booking

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"
)

const (
	// DefaultWarmBrowserCapacity is the number of booking browsers requested
	// while an authenticated monitor is active.
	DefaultWarmBrowserCapacity = 2
	// MaximumWarmBrowserCapacity bounds local process and memory consumption.
	MaximumWarmBrowserCapacity = 3
)

var (
	// ErrWarmPoolClosed means the Client is shutting down or no longer accepts leases.
	ErrWarmPoolClosed = errors.New("warm browser pool is closed")
	// ErrWarmProcessInvalid means a process failed the one-page/readiness contract.
	ErrWarmProcessInvalid = errors.New("warm browser process failed readiness invariants")
	// ErrWarmProcessReap means the process did not exit after bounded cleanup.
	ErrWarmProcessReap = errors.New("warm browser process could not be reaped")
	// ErrWarmPaymentRetained means a browser is exclusively held for payment.
	ErrWarmPaymentRetained = errors.New("warm browser process is retained for payment")
	// ErrWarmPermanent means startup cannot succeed until demand or credentials change.
	ErrWarmPermanent = errors.New("warm browser startup requires renewed demand")
)

// WarmBrowserProcess is the ownership boundary for one disposable browser
// process. The factory must return the OS process handle that it started;
// Close is graceful shutdown, KillTree is the bounded last resort, and Wait
// must reap the exact child process. The pool calls Wait exactly once.
type WarmBrowserProcess interface {
	PID() int
	ProfileDir() string
	PageCount() int
	// Ready must include the explicit CGV authentication check. The pool does
	// not infer readiness from a launched page or from a copied profile.
	Ready() bool
	// Crashed reports a browser/context crash separately from Wait. Wait must
	// still reap the owning driver process before the slot is fully retired.
	Crashed() <-chan error
	Close() error
	KillTree() error
	Wait() error
}

// WarmBrowserProcessFactory creates one isolated browser process for a pool slot.
type WarmBrowserProcessFactory func(context.Context, uint64) (WarmBrowserProcess, error)

// WarmPoolConfig controls capacity, shutdown bounds, and crash backoff.
type WarmPoolConfig struct {
	// InitialDesired defaults to zero. Demand owners call SetDesired(2) when an
	// active booking monitor exists and SetDesired(0) when there is no demand.
	InitialDesired int
	MaxCapacity    int
	CloseTimeout   time.Duration
	KillTimeout    time.Duration
	RestartBase    time.Duration
	RestartMax     time.Duration
}

// WarmPool owns Client-local booking browser demand and leases. It never
// contacts Central and it never shares a process between logical tasks.
type WarmPool struct {
	factory WarmBrowserProcessFactory
	ctx     context.Context
	cancel  context.CancelFunc
	config  WarmPoolConfig

	mu            sync.Mutex
	closed        bool
	desired       int
	creating      int
	sequence      uint64
	slots         map[uint64]*warmSlot
	wake          chan struct{}
	failures      int
	reaping       int
	readyNotifier func()

	createWG  sync.WaitGroup
	reapWG    sync.WaitGroup
	closeOnce sync.Once
	closeDone chan struct{}
	closeErr  error
	reapErr   error
}

type warmSlot struct {
	id       uint64
	process  WarmBrowserProcess
	state    warmSlotState
	retained bool
	lease    *WarmBrowserLease
	done     chan struct{}
	waitErr  error
	reaping  bool
}

type warmSlotState uint8

const (
	warmReady warmSlotState = iota
	warmBusy
	warmRetained
)

// NewWarmPool creates a demand-driven pool. It does not start a browser until
// SetDesired is called, which keeps Launcher and Central out of browser
// ownership and avoids idle browsers when no monitor is active.
func NewWarmPool(parent context.Context, factory WarmBrowserProcessFactory, config WarmPoolConfig) (*WarmPool, error) {
	if parent == nil || factory == nil {
		return nil, errors.New("warm browser pool dependencies are incomplete")
	}
	config = normalizeWarmPoolConfig(config)
	ctx, cancel := context.WithCancel(parent)
	pool := &WarmPool{
		factory: factory, ctx: ctx, cancel: cancel, config: config,
		desired: clampWarmDesired(config.InitialDesired, config.MaxCapacity),
		slots:   make(map[uint64]*warmSlot), wake: make(chan struct{}, 1), closeDone: make(chan struct{}),
	}
	if pool.desired > 0 {
		pool.ensureAsync()
	}
	return pool, nil
}

func normalizeWarmPoolConfig(config WarmPoolConfig) WarmPoolConfig {
	if config.MaxCapacity <= 0 || config.MaxCapacity > MaximumWarmBrowserCapacity {
		config.MaxCapacity = MaximumWarmBrowserCapacity
	}
	if config.CloseTimeout <= 0 {
		config.CloseTimeout = 2 * time.Second
	}
	if config.KillTimeout <= 0 {
		config.KillTimeout = time.Second
	}
	if config.RestartBase <= 0 {
		config.RestartBase = 100 * time.Millisecond
	}
	if config.RestartMax <= 0 {
		config.RestartMax = 5 * time.Second
	}
	if config.RestartMax < config.RestartBase {
		config.RestartMax = config.RestartBase
	}
	return config
}

func clampWarmDesired(value, max int) int {
	if value < 0 {
		return 0
	}
	if value > max {
		return max
	}
	return value
}

// SetDesired changes the local browser demand. The normal booking target is
// DefaultWarmBrowserCapacity and the hard safety cap is three. Lowering the
// target retires idle processes immediately; busy and payment-retained
// processes are never reused or force-closed by this operation.
func (pool *WarmPool) SetDesired(desired int) {
	if pool == nil {
		return
	}
	pool.mu.Lock()
	if !pool.closed {
		pool.desired = clampWarmDesired(desired, pool.config.MaxCapacity)
		pool.retireIdleLocked()
	}
	pool.mu.Unlock()
	pool.signal()
	pool.ensureAsync()
}

// Desired returns the current local browser demand.
func (pool *WarmPool) Desired() int {
	if pool == nil {
		return 0
	}
	pool.mu.Lock()
	defer pool.mu.Unlock()
	return pool.desired
}

// SetReadyNotifier installs a local wake callback. It is used by Client's
// execution worker so a slot becoming ready cannot leave a Central command
// waiter asleep. The callback carries no Central state.
func (pool *WarmPool) SetReadyNotifier(notifier func()) {
	if pool == nil {
		return
	}
	pool.mu.Lock()
	pool.readyNotifier = notifier
	pool.mu.Unlock()
}

// WarmPoolStats is a point-in-time view of local booking capacity.
type WarmPoolStats struct {
	Desired, Ready, Busy, Retained, Creating int
}

// Stats returns current local booking capacity.
func (pool *WarmPool) Stats() WarmPoolStats {
	if pool == nil {
		return WarmPoolStats{}
	}
	pool.mu.Lock()
	defer pool.mu.Unlock()
	stats := WarmPoolStats{Desired: pool.desired, Creating: pool.creating}
	for _, slot := range pool.slots {
		switch slot.state {
		case warmReady:
			stats.Ready++
		case warmBusy:
			stats.Busy++
		case warmRetained:
			stats.Retained++
		}
	}
	return stats
}

// ReapError exposes asynchronous process-shutdown failures that have already
// been observed, without making callers inspect logs or races with Close.
func (pool *WarmPool) ReapError() error {
	if pool == nil {
		return nil
	}
	pool.mu.Lock()
	defer pool.mu.Unlock()
	return pool.reapErr
}

// Acquire leases exactly one prewarmed process and its only page. The process
// is consumed on Release, or held exclusively when retained for payment.
func (pool *WarmPool) Acquire(ctx context.Context) (*WarmBrowserLease, error) {
	if ctx == nil {
		return nil, errors.New("warm browser acquire context is required")
	}
	for {
		pool.mu.Lock()
		if pool.closed {
			pool.mu.Unlock()
			return nil, ErrWarmPoolClosed
		}
		for _, slot := range pool.slots {
			if slot.state != warmReady {
				continue
			}
			slot.state = warmBusy
			lease := &WarmBrowserLease{pool: pool, slot: slot, process: slot.process, done: make(chan struct{})}
			slot.lease = lease
			pool.mu.Unlock()
			return lease, nil
		}
		wake := pool.wake
		pool.mu.Unlock()
		pool.ensureAsync()
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-pool.ctx.Done():
			return nil, ErrWarmPoolClosed
		case <-wake:
		}
	}
}

// WarmBrowserLease is intentionally small: callers use Process for the
// existing browser/Playwright adapter and must not create another page.
type WarmBrowserLease struct {
	pool     *WarmPool
	slot     *warmSlot
	process  WarmBrowserProcess
	once     sync.Once
	mu       sync.Mutex
	retained bool
	done     chan struct{}
	err      error
	doneOnce sync.Once
}

// Process returns the leased process so an adapter can use its existing page.
func (lease *WarmBrowserLease) Process() WarmBrowserProcess {
	if lease == nil {
		return nil
	}
	return lease.process
}

// Done closes if the leased process exits unexpectedly. A caller holding a
// booking or payment handoff can therefore fail closed instead of silently
// retaining a dead process.
func (lease *WarmBrowserLease) Done() <-chan struct{} {
	if lease == nil {
		return nil
	}
	return lease.done
}

// Err returns the process failure that closed this lease, if any.
func (lease *WarmBrowserLease) Err() error {
	if lease == nil || lease.pool == nil {
		return ErrWarmProcessInvalid
	}
	lease.pool.mu.Lock()
	defer lease.pool.mu.Unlock()
	return lease.err
}

// RetainPayment makes the leased browser unavailable to other work until
// ReleasePayment is called.
func (lease *WarmBrowserLease) RetainPayment() error {
	if lease == nil || lease.pool == nil {
		return ErrWarmPoolClosed
	}
	lease.mu.Lock()
	defer lease.mu.Unlock()
	if lease.retained {
		return nil
	}
	lease.pool.mu.Lock()
	defer lease.pool.mu.Unlock()
	if lease.pool.closed {
		return ErrWarmPoolClosed
	}
	if lease.slot.lease != lease || lease.slot.state != warmBusy {
		return ErrWarmProcessInvalid
	}
	lease.slot.state = warmRetained
	lease.slot.retained = true
	lease.retained = true
	return nil
}

// Release consumes an ordinary lease. A browser process owns one logical task
// and is never reused after it closes; the pool prewarms a replacement. A
// payment-retained lease deliberately stays unavailable until ReleasePayment.
func (lease *WarmBrowserLease) Release() {
	if lease == nil {
		return
	}
	lease.once.Do(func() { lease.pool.releaseLease(lease) })
}

// ReleasePayment ends an exclusive payment handoff and consumes the process.
func (lease *WarmBrowserLease) ReleasePayment() {
	if lease == nil || lease.pool == nil {
		return
	}
	lease.pool.releasePayment(lease)
}

func (pool *WarmPool) releaseLease(lease *WarmBrowserLease) {
	pool.mu.Lock()
	if lease.slot.lease != lease || lease.slot.state != warmBusy {
		pool.mu.Unlock()
		return
	}
	lease.slot.lease = nil
	delete(pool.slots, lease.slot.id)
	slot := lease.slot
	pool.scheduleReapLocked(slot)
	pool.mu.Unlock()
	pool.signal()
	pool.ensureAsync()
}

func (pool *WarmPool) releasePayment(lease *WarmBrowserLease) {
	pool.mu.Lock()
	if lease.slot.lease != lease || lease.slot.state != warmRetained {
		pool.mu.Unlock()
		return
	}
	delete(pool.slots, lease.slot.id)
	slot := lease.slot
	lease.slot.lease = nil
	pool.scheduleReapLocked(slot)
	pool.mu.Unlock()
	pool.signal()
	pool.ensureAsync()
}

func (pool *WarmPool) retireIdleLocked() {
	for len(pool.slots) > pool.desired {
		var candidate *warmSlot
		for _, slot := range pool.slots {
			if slot.state == warmReady && (candidate == nil || slot.id < candidate.id) {
				candidate = slot
			}
		}
		if candidate == nil {
			return
		}
		delete(pool.slots, candidate.id)
		pool.scheduleReapLocked(candidate)
	}
}

func (pool *WarmPool) ensureAsync() {
	pool.mu.Lock()
	if pool.closed || pool.creating > 0 || len(pool.slots)+pool.creating+pool.reaping >= pool.desired {
		pool.mu.Unlock()
		return
	}
	pool.creating++
	pool.sequence++
	slotID := pool.sequence
	pool.createWG.Add(1)
	pool.mu.Unlock()
	go pool.create(slotID)
}

func (pool *WarmPool) create(slotID uint64) {
	defer pool.createWG.Done()
	process, err := pool.factory(pool.ctx, slotID)
	pool.mu.Lock()
	pool.creating--
	closed := pool.closed
	pool.mu.Unlock()
	if err != nil {
		pool.handleCreateFailure(process, err, closed)
		return
	}
	if !validWarmProcess(process) {
		pool.handleCreateFailure(process, ErrWarmProcessInvalid, closed)
		return
	}
	if !pool.admitProcess(slotID, process) {
		pool.recordReapError(pool.terminate(process))
		pool.signal()
		return
	}
	pool.signal()
	pool.ensureAsync()
}

func validWarmProcess(process WarmBrowserProcess) bool {
	return process != nil && process.PID() > 0 && process.PageCount() == 1 &&
		process.ProfileDir() != "" && process.Ready()
}

func (pool *WarmPool) handleCreateFailure(process WarmBrowserProcess, err error, closed bool) {
	if process != nil {
		pool.recordReapError(pool.terminate(process))
	}
	pool.mu.Lock()
	desired := pool.desired
	pool.mu.Unlock()
	if !closed && desired > 0 && !errors.Is(err, ErrWarmPermanent) {
		pool.scheduleRetry()
	}
	pool.signal()
}

func (pool *WarmPool) admitProcess(slotID uint64, process WarmBrowserProcess) bool {
	pool.mu.Lock()
	defer pool.mu.Unlock()
	if pool.closed || pool.desired == 0 || len(pool.slots) >= pool.desired || pool.hasIdentityLocked(process) {
		return false
	}
	pool.failures = 0
	slot := &warmSlot{id: slotID, process: process, state: warmReady, done: make(chan struct{})}
	pool.slots[slotID] = slot
	go pool.observe(slot)
	return true
}

func (pool *WarmPool) hasIdentityLocked(process WarmBrowserProcess) bool {
	for _, slot := range pool.slots {
		if slot.process.PID() == process.PID() || slot.process.ProfileDir() == process.ProfileDir() {
			return true
		}
	}
	return false
}

func (pool *WarmPool) observe(slot *warmSlot) {
	waitResult := make(chan error, 1)
	go func() { waitResult <- slot.process.Wait() }()
	crashed, waitFinished, err := waitForWarmProcess(slot.process, waitResult)
	exists, desired := pool.recordObserved(slot, err, waitFinished, crashed)
	if crashed {
		pool.finishCrash(slot, waitResult)
	}
	if !exists || pool.closed {
		return
	}
	pool.signal()
	if !crashed && desired > 0 {
		pool.scheduleRetry()
	}
}

func waitForWarmProcess(process WarmBrowserProcess, waitResult <-chan error) (bool, bool, error) {
	select {
	case err := <-waitResult:
		return false, true, err
	case crashErr := <-process.Crashed():
		if crashErr == nil {
			crashErr = errors.New("warm browser process crashed")
		}
		return true, false, crashErr
	}
}

func (pool *WarmPool) recordObserved(slot *warmSlot, err error, waitFinished, crashed bool) (bool, int) {
	pool.mu.Lock()
	defer pool.mu.Unlock()
	if waitFinished {
		slot.waitErr = err
		select {
		case <-slot.done:
		default:
			close(slot.done)
		}
	}
	if slot.lease != nil {
		slot.lease.err = err
		slot.lease.doneOnce.Do(func() { close(slot.lease.done) })
	}
	current, exists := pool.slots[slot.id]
	if exists && current == slot && !pool.closed {
		delete(pool.slots, slot.id)
	}
	if crashed && !slot.reaping {
		pool.scheduleReapLocked(slot)
	}
	return exists && current == slot, pool.desired
}

func (pool *WarmPool) finishCrash(slot *warmSlot, waitResult <-chan error) {
	go func() {
		<-waitResult
		pool.mu.Lock()
		select {
		case <-slot.done:
		default:
			close(slot.done)
		}
		pool.mu.Unlock()
		pool.signal()
		pool.scheduleRetry()
	}()
}

func (pool *WarmPool) scheduleRetry() {
	// The retry is bounded and cancellable. A process crash never causes a
	// tight launch loop, and pool shutdown prevents a delayed replacement.
	pool.mu.Lock()
	if pool.closed || pool.desired == 0 {
		pool.mu.Unlock()
		return
	}
	pool.failures++
	delay := pool.config.RestartBase
	for i := 1; i < pool.failures && delay < pool.config.RestartMax; i++ {
		delay *= 2
		if delay > pool.config.RestartMax {
			delay = pool.config.RestartMax
		}
	}
	pool.mu.Unlock()
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-timer.C:
		pool.ensureAsync()
	case <-pool.ctx.Done():
	}
}

func (pool *WarmPool) signal() {
	select {
	case pool.wake <- struct{}{}:
	default:
	}
	pool.mu.Lock()
	notifier := pool.readyNotifier
	pool.mu.Unlock()
	if notifier != nil {
		notifier()
	}
}

func (pool *WarmPool) terminate(process WarmBrowserProcess) error {
	if process == nil {
		return nil
	}
	done := make(chan struct{})
	go func() {
		_ = process.Wait()
		close(done)
	}()
	return pool.terminateWithDone(process, done)
}

func (pool *WarmPool) terminateWithDone(process WarmBrowserProcess, done <-chan struct{}) error {
	closeResult := make(chan error, 1)
	go func() { closeResult <- process.Close() }()
	closeErr, gracefulReturned := waitForClose(closeResult, pool.config.CloseTimeout)
	if gracefulReturned && waitForDone(done, pool.config.CloseTimeout) {
		return closeErr
	}
	if err := process.KillTree(); err != nil && closeErr == nil {
		closeErr = err
	}
	if !gracefulReturned {
		select {
		case closeErr = <-closeResult:
		default:
		}
	}
	if !waitForDone(done, pool.config.KillTimeout) && closeErr == nil {
		closeErr = ErrWarmProcessReap
	}
	return closeErr
}

func waitForClose(result <-chan error, timeout time.Duration) (error, bool) {
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	select {
	case err := <-result:
		return err, true
	case <-timer.C:
		return nil, false
	}
}

// scheduleReapLocked registers the shutdown goroutine before Close can begin
// waiting. Callers must hold pool.mu; this avoids Add/Wait races when release
// and app shutdown overlap.
func (pool *WarmPool) scheduleReapLocked(slot *warmSlot) {
	if slot == nil || slot.process == nil {
		return
	}
	if slot.reaping {
		return
	}
	slot.reaping = true
	pool.reapWG.Add(1)
	pool.reaping++
	go func() {
		defer pool.reapWG.Done()
		reapErr := pool.terminateWithDone(slot.process, slot.done)
		pool.mu.Lock()
		if reapErr != nil {
			pool.reapErr = errors.Join(pool.reapErr, reapErr)
		}
		pool.reaping--
		pool.mu.Unlock()
		pool.signal()
		pool.ensureAsync()
	}()
}

func (pool *WarmPool) recordReapError(err error) {
	if err == nil {
		return
	}
	pool.mu.Lock()
	pool.reapErr = errors.Join(pool.reapErr, err)
	pool.mu.Unlock()
}

func waitForDone(done <-chan struct{}, timeout time.Duration) bool {
	if done == nil {
		return false
	}
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	select {
	case <-done:
		return true
	case <-timer.C:
		return false
	}
}

// Close is idempotent and safe to race with Acquire, SetDesired, crash
// observation, or startup. Every process is gracefully closed, then its
// complete tree is killed if it misses the bound, and its child is reaped.
func (pool *WarmPool) Close() error {
	if pool == nil {
		return nil
	}
	pool.closeOnce.Do(func() {
		pool.mu.Lock()
		pool.closed = true
		pool.cancel()
		slots := make([]*warmSlot, 0, len(pool.slots))
		for id, slot := range pool.slots {
			if !slot.reaping {
				slot.reaping = true
				slots = append(slots, slot)
			}
			delete(pool.slots, id)
		}
		pool.mu.Unlock()
		pool.createWG.Wait()
		pool.reapWG.Wait()
		pool.mu.Lock()
		pool.closeErr = errors.Join(pool.closeErr, pool.reapErr)
		pool.mu.Unlock()
		for _, slot := range slots {
			pool.closeErr = errors.Join(pool.closeErr, pool.terminateWithDone(slot.process, slot.done))
		}
		close(pool.closeDone)
	})
	<-pool.closeDone
	return pool.closeErr
}

// String summarizes local warm capacity for diagnostics without exposing
// profile paths, credentials, or process arguments.
func (pool *WarmPool) String() string {
	stats := pool.Stats()
	return fmt.Sprintf("warm browser pool desired=%d ready=%d busy=%d retained=%d", stats.Desired, stats.Ready, stats.Busy, stats.Retained)
}
