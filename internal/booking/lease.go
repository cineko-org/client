package booking

import "sync"

// Lease is the exclusive handle for one browser process and its sole page.
type Lease struct {
	pool     *Pool
	slot     *slot
	process  Process
	done     chan struct{}
	once     sync.Once
	payment  sync.Once
	mu       sync.Mutex
	retained bool
	err      error
	doneOnce sync.Once
}

// Process returns the process behind the lease; adapters must reuse its page.
func (lease *Lease) Process() Process {
	if lease == nil {
		return nil
	}
	return lease.process
}

// Done closes when the process exits unexpectedly or the pool shuts down.
func (lease *Lease) Done() <-chan struct{} {
	if lease == nil {
		return nil
	}
	return lease.done
}

// Err returns the process failure that closed the lease, if any.
func (lease *Lease) Err() error {
	if lease == nil || lease.pool == nil {
		return ErrProcessInvalid
	}
	lease.pool.mu.Lock()
	defer lease.pool.mu.Unlock()
	return lease.err
}

// RetainPayment reserves the browser until ReleasePayment is called.
func (lease *Lease) RetainPayment() error {
	if lease == nil || lease.pool == nil {
		return ErrPoolClosed
	}
	lease.mu.Lock()
	defer lease.mu.Unlock()
	if lease.retained {
		return nil
	}
	lease.pool.mu.Lock()
	defer lease.pool.mu.Unlock()
	if lease.pool.closed {
		return ErrPoolClosed
	}
	if lease.slot.lease != lease || lease.slot.state != slotBusy {
		return ErrProcessInvalid
	}
	lease.slot.state = slotRetained
	lease.retained = true
	return nil
}

// Release consumes a normal booking process and starts a replacement after
// its driver has been reaped.
func (lease *Lease) Release() {
	if lease == nil {
		return
	}
	lease.once.Do(func() { lease.pool.release(lease) })
}

// ReleasePayment consumes a payment-retained process and starts a replacement
// only after the retained driver has been reaped.
func (lease *Lease) ReleasePayment() {
	if lease == nil || lease.pool == nil {
		return
	}
	lease.payment.Do(func() { lease.pool.releasePayment(lease) })
}

func (pool *Pool) release(lease *Lease) {
	pool.releaseLease(lease, slotBusy)
}

func (pool *Pool) releasePayment(lease *Lease) {
	pool.releaseLease(lease, slotRetained)
}

func (pool *Pool) releaseLease(lease *Lease, state slotState) {
	pool.mu.Lock()
	if lease.slot.lease != lease || lease.slot.state != state {
		pool.mu.Unlock()
		return
	}
	lease.slot.lease = nil
	delete(pool.processes, lease.slot.id)
	pool.scheduleReapLocked(lease.slot)
	pool.mu.Unlock()
	pool.signal()
	pool.ensureAsync()
}

func (pool *Pool) retireIdleLocked() {
	for len(pool.processes) > pool.desired {
		var candidate *slot
		for _, current := range pool.processes {
			if current.state == slotReady && (candidate == nil || current.id < candidate.id) {
				candidate = current
			}
		}
		if candidate == nil {
			return
		}
		delete(pool.processes, candidate.id)
		pool.scheduleReapLocked(candidate)
	}
}
