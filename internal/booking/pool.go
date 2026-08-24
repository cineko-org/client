// Package booking owns the Client's local booking-browser capacity.
//
// It contains no CGV, network-control-plane, or UI code. Adapters provide one process that
// satisfies Process; this package owns demand, leases, payment retention,
// crash replacement, and bounded process reaping.
package booking

import (
	"context"
	"errors"
	"sync"
	"time"
)

const (
	// DefaultWarmBrowserCapacity is one shared authenticated browser; exact
	// showtimes scale as tabs inside this process.
	DefaultWarmBrowserCapacity = 1
	// MaximumWarmBrowserCapacity is the hard local process safety cap.
	MaximumWarmBrowserCapacity = 3
)

var (
	// ErrPoolClosed means the local booking pool is shutting down.
	ErrPoolClosed = errors.New("booking browser pool is closed")
	// ErrProcessInvalid means a process failed the readiness invariants.
	ErrProcessInvalid = errors.New("booking browser process failed readiness invariants")
	// ErrProcessReap means a process did not exit after bounded cleanup.
	ErrProcessReap = errors.New("booking browser process could not be reaped")
	// ErrPermanent means startup cannot succeed until demand or credentials change.
	ErrPermanent = errors.New("booking browser startup requires changed demand")
	// ErrShutdownTimeout means owned lifecycle work exceeded the shutdown bound.
	ErrShutdownTimeout = errors.New("booking browser pool shutdown timed out")
)

// Process is the lifecycle boundary for one disposable browser process. Wait
// must reap the exact driver process and is called exactly once by Pool.
type Process interface {
	PID() int
	ProfileDir() string
	PageCount() int
	Ready() bool
	Crashed() <-chan error
	Close() error
	KillTree() error
	Wait() error
}

// ProcessFactory creates one isolated process for one pool slot.
type ProcessFactory func(context.Context, uint64) (Process, error)

// Config controls pool capacity, shutdown bounds, and crash retry backoff.
type Config struct {
	InitialDesired  int
	MaxCapacity     int
	CloseTimeout    time.Duration
	KillTimeout     time.Duration
	RestartBase     time.Duration
	RestartMax      time.Duration
	ShutdownTimeout time.Duration
}

// Stats describes the current local booking capacity.
type Stats struct {
	Desired, Ready, Busy, Retained, Creating int
}

// Pool owns demand-driven booking browser slots. A slot is isolated to one
// process, one page, and one logical booking task.
type Pool struct {
	factory ProcessFactory
	ctx     context.Context
	cancel  context.CancelFunc
	config  Config

	mu            sync.Mutex
	closed        bool
	desired       int
	creating      int
	sequence      uint64
	processes     map[uint64]*slot
	reaping       int
	failures      int
	wake          chan struct{}
	creatingWake  chan struct{}
	reapingWake   chan struct{}
	readyNotifier func()
	reapErr       error
	closeErr      error

	closeOnce sync.Once
	closeDone chan struct{}
}

type slot struct {
	id       uint64
	process  Process
	state    slotState
	lease    *Lease
	done     chan struct{}
	doneOnce sync.Once
	reaping  bool
	waitErr  error
}

type slotState uint8

const (
	slotReady slotState = iota
	slotBusy
	slotRetained
)

// NewPool creates a demand-driven pool. It starts no browser when desired is
// zero, which keeps idle Client installations free of browser processes.
func NewPool(parent context.Context, factory ProcessFactory, config Config) (*Pool, error) {
	if parent == nil || factory == nil {
		return nil, errors.New("booking browser pool dependencies are incomplete")
	}
	config = normalizeConfig(config)
	ctx, cancel := context.WithCancel(parent)
	pool := &Pool{
		factory: factory, ctx: ctx, cancel: cancel, config: config,
		desired:   clampDesired(config.InitialDesired, config.MaxCapacity),
		processes: make(map[uint64]*slot), wake: make(chan struct{}, 1),
		creatingWake: make(chan struct{}, 1), reapingWake: make(chan struct{}, 1),
		closeDone: make(chan struct{}),
	}
	pool.ensureAsync()
	go func() {
		<-ctx.Done()
		_ = pool.Close()
	}()
	return pool, nil
}

func normalizeConfig(config Config) Config {
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
	if config.ShutdownTimeout <= 0 {
		config.ShutdownTimeout = config.CloseTimeout + config.KillTimeout + time.Second
	}
	return config
}

func clampDesired(value, maximum int) int {
	if value < 0 {
		return 0
	}
	if value > maximum {
		return maximum
	}
	return value
}

// SetDesired changes local demand. Idle slots above the new target are
// retired; busy and payment-retained slots are allowed to finish.
func (pool *Pool) SetDesired(desired int) {
	if pool == nil {
		return
	}
	pool.mu.Lock()
	if !pool.closed {
		pool.desired = clampDesired(desired, pool.config.MaxCapacity)
		pool.retireIdleLocked()
	}
	pool.mu.Unlock()
	pool.signal()
	pool.ensureAsync()
}

// Desired returns the current local browser demand.
func (pool *Pool) Desired() int {
	if pool == nil {
		return 0
	}
	pool.mu.Lock()
	defer pool.mu.Unlock()
	return pool.desired
}

// SetReadyNotifier installs a local wake callback for the execution worker.
func (pool *Pool) SetReadyNotifier(notifier func()) {
	if pool == nil {
		return
	}
	pool.mu.Lock()
	pool.readyNotifier = notifier
	pool.mu.Unlock()
}

// Stats returns a synchronized point-in-time capacity view.
func (pool *Pool) Stats() Stats {
	if pool == nil {
		return Stats{}
	}
	pool.mu.Lock()
	defer pool.mu.Unlock()
	stats := Stats{Desired: pool.desired, Creating: pool.creating}
	for _, current := range pool.processes {
		switch current.state {
		case slotReady:
			stats.Ready++
		case slotBusy:
			stats.Busy++
		case slotRetained:
			stats.Retained++
		}
	}
	return stats
}

// ReapError returns an already observed process cleanup failure.
func (pool *Pool) ReapError() error {
	if pool == nil {
		return nil
	}
	pool.mu.Lock()
	defer pool.mu.Unlock()
	return pool.reapErr
}

// Acquire waits for one authenticated, isolated ready process.
func (pool *Pool) Acquire(ctx context.Context) (*Lease, error) {
	if pool == nil || ctx == nil {
		return nil, errors.New("booking browser acquire context is required")
	}
	for {
		pool.mu.Lock()
		if pool.closed {
			pool.mu.Unlock()
			return nil, ErrPoolClosed
		}
		for _, current := range pool.processes {
			if current.state != slotReady {
				continue
			}
			current.state = slotBusy
			lease := &Lease{pool: pool, slot: current, process: current.process, done: make(chan struct{})}
			current.lease = lease
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
			return nil, ErrPoolClosed
		case <-wake:
		}
	}
}
