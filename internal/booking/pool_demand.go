package booking

import "errors"

func (pool *Pool) ensureAsync() {
	pool.mu.Lock()
	if pool.closed {
		pool.mu.Unlock()
		return
	}
	missing := pool.desired - len(pool.processes) - pool.creating - pool.reaping
	if missing <= 0 {
		pool.mu.Unlock()
		return
	}
	sequences := make([]uint64, missing)
	for index := range sequences {
		pool.creating++
		pool.sequence++
		sequences[index] = pool.sequence
	}
	pool.mu.Unlock()
	for _, sequence := range sequences {
		go pool.create(sequence)
	}
}

func (pool *Pool) create(sequence uint64) {
	process, err := pool.factory(pool.ctx, sequence)
	if err != nil {
		if process != nil {
			pool.recordReapError(pool.terminate(process))
		}
		closed := pool.finishCreating()
		pool.failStartup(err, closed)
		return
	}
	if !validProcess(process) {
		pool.recordReapError(pool.terminate(process))
		closed := pool.finishCreating()
		pool.failStartup(ErrProcessInvalid, closed)
		return
	}
	if !pool.admit(sequence, process) {
		pool.recordReapError(pool.terminate(process))
		pool.finishCreating()
		pool.signal()
		pool.ensureAsync()
		return
	}
	pool.finishCreating()
	pool.signal()
	pool.ensureAsync()
}

func (pool *Pool) finishCreating() bool {
	pool.mu.Lock()
	defer pool.mu.Unlock()
	pool.creating--
	select {
	case pool.creatingWake <- struct{}{}:
	default:
	}
	return pool.closed
}

func validProcess(process Process) bool {
	return process != nil && process.PID() > 0 && process.PageCount() == 1 &&
		process.ProfileDir() != "" && process.Ready()
}

func (pool *Pool) failStartup(startupErr error, closed bool) {
	pool.mu.Lock()
	desired := pool.desired
	pool.mu.Unlock()
	if !closed && desired > 0 && !errors.Is(startupErr, ErrPermanent) {
		pool.scheduleRetry()
	}
	pool.signal()
}

func (pool *Pool) admit(sequence uint64, process Process) bool {
	pool.mu.Lock()
	defer pool.mu.Unlock()
	if pool.closed || pool.desired == 0 || len(pool.processes) >= pool.desired || pool.duplicateLocked(process) {
		return false
	}
	pool.failures = 0
	current := &slot{id: sequence, process: process, state: slotReady, done: make(chan struct{})}
	pool.processes[sequence] = current
	go pool.observe(current)
	return true
}

func (pool *Pool) duplicateLocked(process Process) bool {
	for _, current := range pool.processes {
		if current.process.PID() == process.PID() || current.process.ProfileDir() == process.ProfileDir() {
			return true
		}
	}
	return false
}
