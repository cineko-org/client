package booking

import (
	"errors"
	"fmt"
	"time"
)

func (pool *Pool) terminate(process Process) error {
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

func (pool *Pool) terminateWithDone(process Process, done <-chan struct{}) error {
	closeResult := make(chan error, 1)
	go func() { closeResult <- process.Close() }()
	closeErr, closeFinished := waitResult(closeResult, pool.config.CloseTimeout)
	if waitDone(done, pool.config.CloseTimeout) {
		if !closeFinished {
			select {
			case closeErr = <-closeResult:
			default:
			}
		}
		// A close/context error is safe to surface when the independent Wait
		// owner already proved that the exact driver was reaped.
		return closeErr
	}
	// The process is still live after the bounded graceful-close wait. Force
	// descendants and root down; the existing Wait owner performs the reap.
	if err := process.KillTree(); err != nil {
		closeErr = errors.Join(closeErr, err)
	}
	if !closeFinished {
		select {
		case lateErr := <-closeResult:
			closeErr = errors.Join(closeErr, lateErr)
		default:
		}
	}
	if !waitDone(done, pool.config.KillTimeout) {
		closeErr = errors.Join(closeErr, ErrProcessReap)
	}
	return closeErr
}

func waitResult(result <-chan error, timeout time.Duration) (error, bool) {
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	select {
	case err := <-result:
		return err, true
	case <-timer.C:
		return nil, false
	}
}

func waitDone(done <-chan struct{}, timeout time.Duration) bool {
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	select {
	case <-done:
		return true
	case <-timer.C:
		return false
	}
}

func (pool *Pool) scheduleReapLocked(current *slot) {
	if current == nil || current.process == nil || current.reaping {
		return
	}
	current.reaping = true
	pool.reaping++
	go func() {
		err := pool.terminateWithDone(current.process, current.done)
		pool.mu.Lock()
		if err != nil {
			pool.reapErr = errors.Join(pool.reapErr, err)
		}
		pool.reaping--
		select {
		case pool.reapingWake <- struct{}{}:
		default:
		}
		pool.mu.Unlock()
		pool.signal()
		pool.ensureAsync()
	}()
}

func (pool *Pool) recordReapError(err error) {
	if err == nil {
		return
	}
	pool.mu.Lock()
	pool.reapErr = errors.Join(pool.reapErr, err)
	pool.mu.Unlock()
}

// Close stops demand and every owned process. It first asks each browser to
// close gracefully, then kills the complete process tree and waits/reaps it.
func (pool *Pool) Close() error {
	if pool == nil {
		return nil
	}
	pool.closeOnce.Do(func() {
		pool.mu.Lock()
		pool.closed = true
		pool.cancel()
		for id, current := range pool.processes {
			if current.lease != nil {
				current.lease.err = ErrPoolClosed
				current.lease.doneOnce.Do(func() { close(current.lease.done) })
			}
			delete(pool.processes, id)
			pool.scheduleReapLocked(current)
		}
		pool.mu.Unlock()
		deadline := time.Now().Add(pool.config.ShutdownTimeout)
		createsDone := pool.waitCounter(func() int {
			pool.mu.Lock()
			defer pool.mu.Unlock()
			return pool.creating
		}, pool.creatingWake, deadline)
		reapsDone := pool.waitCounter(func() int {
			pool.mu.Lock()
			defer pool.mu.Unlock()
			return pool.reaping
		}, pool.reapingWake, deadline)
		pool.mu.Lock()
		pool.closeErr = pool.reapErr
		if !createsDone || !reapsDone {
			pool.closeErr = errors.Join(pool.closeErr, ErrShutdownTimeout)
		}
		pool.mu.Unlock()
		close(pool.closeDone)
	})
	<-pool.closeDone
	pool.mu.Lock()
	defer pool.mu.Unlock()
	return pool.closeErr
}

func (pool *Pool) waitCounter(counter func() int, wake <-chan struct{}, deadline time.Time) bool {
	for {
		if counter() == 0 {
			return true
		}
		remaining := time.Until(deadline)
		if remaining <= 0 {
			return counter() == 0
		}
		timer := time.NewTimer(remaining)
		select {
		case <-wake:
			if !timer.Stop() {
				select {
				case <-timer.C:
				default:
				}
			}
		case <-timer.C:
			return counter() == 0
		}
	}
}

// String returns a diagnostic summary without profile paths or secrets.
func (pool *Pool) String() string {
	stats := pool.Stats()
	return fmt.Sprintf("booking browser pool desired=%d ready=%d busy=%d retained=%d", stats.Desired, stats.Ready, stats.Busy, stats.Retained)
}
