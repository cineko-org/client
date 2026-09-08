package cgv

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestHideEventsCoalesceWithoutBlockingDispatcher(t *testing.T) {
	var work coalescedWindowWork
	var calls atomic.Int32
	started, release, followed := make(chan struct{}), make(chan struct{}), make(chan struct{})
	action := func() {
		switch calls.Add(1) {
		case 1:
			close(started)
			<-release
		case 2:
			close(followed)
		default:
			t.Error("event burst created extra hide work")
		}
	}
	work.request(action)
	<-started
	queued := make(chan struct{})
	go func() {
		for range 1000 {
			work.request(action)
		}
		close(queued)
	}()
	select {
	case <-queued:
	case <-time.After(time.Second):
		close(release)
		t.Fatal("browser events blocked on hide work")
	}
	close(release)
	select {
	case <-followed:
	case <-time.After(time.Second):
		t.Fatal("pending hide was lost")
	}
	if calls.Load() != 2 {
		t.Fatalf("1000 queued events: calls=%d, want 2", calls.Load())
	}
}

func TestTabInitializationIsBoundedAndCanceledWaitersDoNotOpen(t *testing.T) {
	var gate tabCreationGate
	release, err := gate.acquire(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if unexpected, err := gate.acquire(ctx); err == nil {
		unexpected()
		t.Fatal("canceled waiter acquired initialization")
	}
	release()
	var current, peak atomic.Int32
	var group sync.WaitGroup
	for range 12 {
		group.Go(func() {
			release, err := gate.acquire(t.Context())
			if err != nil {
				t.Error(err)
				return
			}
			defer release()
			n := current.Add(1)
			for previous := peak.Load(); n > previous && !peak.CompareAndSwap(previous, n); previous = peak.Load() {
			}
			time.Sleep(time.Millisecond)
			current.Add(-1)
		})
	}
	group.Wait()
	if peak.Load() != 1 {
		t.Fatalf("initializing tabs=%d, want 1", peak.Load())
	}
}
