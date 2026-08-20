//go:build !windows

package cgv

import (
	"errors"
	"os/exec"
	"sync/atomic"
	"testing"
	"time"
)

func TestAdapterTransportErrorUsesOneFallbackRootReap(t *testing.T) {
	command := exec.Command("sleep", "30") // #nosec G204 -- fixed lifecycle test command.
	if err := command.Start(); err != nil {
		t.Fatal(err)
	}
	stopErr := errors.New("transport writer close failed")
	adapter := &Adapter{
		processPID:       command.Process.Pid,
		processDone:      make(chan struct{}),
		closeAttemptDone: make(chan struct{}),
		forceWait:        make(chan struct{}),
		processWaitDone:  make(chan struct{}),
		stopPlaywright:   func() error { return stopErr },
	}
	if err := adapter.CloseWithError(); !errors.Is(err, stopErr) {
		t.Fatalf("CloseWithError() = %v, want %v", err, stopErr)
	}
	if !adapter.ProcessNeedsForcedReap() {
		t.Fatal("a live direct child must enter the forced-reap path")
	}
	if err := adapter.KillProcessTree(); err != nil {
		t.Fatalf("KillProcessTree() = %v", err)
	}
	if err := adapter.WaitProcess(); err != nil {
		t.Fatalf("WaitProcess() = %v", err)
	}
	select {
	case <-adapter.processDone:
	case <-time.After(time.Second):
		t.Fatal("processDone did not follow the fallback reap")
	}
	if err := command.Wait(); err == nil {
		t.Fatal("the fallback owner did not reap the direct child")
	}
}

func TestAdapterTransportExitErrorDoesNotFallbackAfterDriverReaped(t *testing.T) {
	command := exec.Command("sleep", "30") // #nosec G204 -- fixed lifecycle test command.
	if err := command.Start(); err != nil {
		t.Fatal(err)
	}
	stopErr := errors.New("driver exited with a transport status")
	var waitCalls atomic.Int32
	adapter := &Adapter{
		processPID:       command.Process.Pid,
		processDone:      make(chan struct{}),
		closeAttemptDone: make(chan struct{}),
		forceWait:        make(chan struct{}),
		processWaitDone:  make(chan struct{}),
		stopPlaywright: func() error {
			if err := command.Process.Kill(); err != nil {
				t.Fatalf("kill test driver: %v", err)
			}
			_, waitErr := command.Process.Wait()
			waitCalls.Add(1)
			if waitErr != nil {
				t.Fatalf("wait test driver: %v", waitErr)
			}
			return stopErr
		},
	}
	if err := adapter.CloseWithError(); !errors.Is(err, stopErr) {
		t.Fatalf("CloseWithError() = %v, want %v", err, stopErr)
	}
	if adapter.ProcessNeedsForcedReap() {
		t.Fatal("an already-reaped direct child must not enter forced cleanup")
	}
	if err := adapter.WaitProcess(); err != nil {
		t.Fatalf("WaitProcess() = %v", err)
	}
	if got := waitCalls.Load(); got != 1 {
		t.Fatalf("transport wait calls = %d, want 1", got)
	}
	select {
	case <-adapter.processDone:
	case <-time.After(time.Second):
		t.Fatal("processDone did not follow the transport-owned reap")
	}
}
