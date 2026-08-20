package booking

import (
	"errors"
	"time"
)

func (pool *Pool) observe(current *slot) {
	waitResult := make(chan error, 1)
	go func() {
		err := current.process.Wait()
		pool.mu.Lock()
		current.waitErr = err
		current.doneOnceClose()
		pool.mu.Unlock()
		waitResult <- err
	}()
	crashed, waitFinished, err := waitForProcess(current.process, waitResult)
	exists := pool.recordObserved(current, err, waitFinished, crashed)
	if crashed {
		// The reaper owns the replacement wake. This prevents a replacement
		// process from starting before the dead driver has been waited on.
		if !exists {
			return
		}
		return
	}
	if exists {
		pool.signal()
		pool.ensureAsync()
	}
}

func waitForProcess(process Process, waitResult <-chan error) (bool, bool, error) {
	select {
	case err := <-waitResult:
		return false, true, err
	case crashErr := <-process.Crashed():
		if crashErr == nil {
			crashErr = errors.New("booking browser process crashed")
		}
		return true, false, crashErr
	}
}

func (pool *Pool) recordObserved(current *slot, err error, waitFinished, crashed bool) bool {
	pool.mu.Lock()
	defer pool.mu.Unlock()
	if waitFinished {
		current.waitErr = err
		current.doneOnceClose()
	}
	if current.lease != nil {
		current.lease.err = err
		current.lease.doneOnce.Do(func() { close(current.lease.done) })
	}
	existing, ok := pool.processes[current.id]
	if !ok || existing != current {
		return false
	}
	delete(pool.processes, current.id)
	if crashed {
		pool.scheduleReapLocked(current)
	}
	return true
}

func (current *slot) doneOnceClose() {
	current.doneOnce.Do(func() { close(current.done) })
}

func (pool *Pool) scheduleRetry() {
	pool.mu.Lock()
	if pool.closed || pool.desired == 0 {
		pool.mu.Unlock()
		return
	}
	pool.failures++
	delay := pool.config.RestartBase
	for index := 1; index < pool.failures && delay < pool.config.RestartMax; index++ {
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

func (pool *Pool) signal() {
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
