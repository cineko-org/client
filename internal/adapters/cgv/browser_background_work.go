package cgv

import (
	"context"
	"sync"
)

// Browser event handlers must not wait for CDP or the visibility lock. Keep
// one worker and at most one follow-up hide, even during a navigation burst.
type coalescedWindowWork struct {
	mu               sync.Mutex
	running, pending bool
}

func (work *coalescedWindowWork) request(action func()) {
	work.mu.Lock()
	work.pending = true
	if work.running {
		work.mu.Unlock()
		return
	}
	work.running = true
	work.mu.Unlock()
	go func() {
		for {
			work.mu.Lock()
			if !work.pending {
				work.running = false
				work.mu.Unlock()
				return
			}
			work.pending = false
			work.mu.Unlock()
			action()
		}
	}()
}

// Only one not-yet-initialized page may exist per booking browser. The gate
// covers NewPage, identity setup and hiding; canceled waiters never open tabs.
type tabCreationGate struct {
	once  sync.Once
	token chan struct{}
}

func (gate *tabCreationGate) acquire(ctx context.Context) (func(), error) {
	gate.once.Do(func() { gate.token = make(chan struct{}, 1) })
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case gate.token <- struct{}{}:
	}
	release := func() { <-gate.token }
	if err := ctx.Err(); err != nil {
		release()
		return nil, err
	}
	return release, nil
}
