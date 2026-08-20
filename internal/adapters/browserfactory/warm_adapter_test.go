package browserfactory

import (
	"context"
	"errors"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/cineko-org/client/internal/adapters/cgv"
	"github.com/cineko-org/client/internal/adapters/egress"
	"github.com/cineko-org/client/internal/booking"
)

type adapterTestProcess struct {
	done  chan struct{}
	crash chan error
	once  sync.Once
	err   error
}

func newAdapterTestProcess() *adapterTestProcess {
	return &adapterTestProcess{done: make(chan struct{}), crash: make(chan error, 1)}
}

func (process *adapterTestProcess) PID() int              { return 81 }
func (process *adapterTestProcess) ProfileDir() string    { return "dead-before-wrap" }
func (process *adapterTestProcess) PageCount() int        { return 1 }
func (process *adapterTestProcess) Ready() bool           { return true }
func (process *adapterTestProcess) Crashed() <-chan error { return process.crash }
func (process *adapterTestProcess) Wait() error           { <-process.done; return process.err }
func (process *adapterTestProcess) Close() error          { process.Exit(); return nil }
func (process *adapterTestProcess) KillTree() error       { process.Exit(); return nil }
func (process *adapterTestProcess) Exit()                 { process.once.Do(func() { close(process.done) }) }
func (process *adapterTestProcess) ExitWith(err error)    { process.err = err; process.Exit() }

func TestWarmBookingProfilesAreDisposableAndDistinctPerSlot(t *testing.T) {
	manager, err := egress.New(egress.Config{})
	if err != nil {
		t.Fatal(err)
	}
	config := cgv.DefaultBrowserConfig()
	config.ProfileDir = filepath.Join(t.TempDir(), "account")
	factory, err := New(config, manager)
	if err != nil {
		t.Fatal(err)
	}
	defer factory.Close()
	task := Task{Purpose: egress.PurposeSession, SessionKey: "user"}
	first, cleanupFirst, err := factory.warmProfileForTask(task, 1)
	if err != nil {
		t.Fatal(err)
	}
	second, cleanupSecond, err := factory.warmProfileForTask(task, 2)
	if err != nil {
		t.Fatal(err)
	}
	if first == second || first == config.ProfileDir || filepath.Dir(first) != filepath.Dir(second) {
		t.Fatalf("warm profiles are not isolated: %q, %q", first, second)
	}
	cleanupFirst()
	cleanupSecond()
}

func TestWarmBookingPoolRequiresExplicitAuthInitializer(t *testing.T) {
	manager, err := egress.New(egress.Config{})
	if err != nil {
		t.Fatal(err)
	}
	factory, err := New(cgv.DefaultBrowserConfig(), manager)
	if err != nil {
		t.Fatal(err)
	}
	defer factory.Close()
	if _, err := factory.NewWarmBookingPool(context.Background(), Task{Purpose: egress.PurposeSession, SessionKey: "user"}, nil); err == nil {
		t.Fatal("warm booking pool accepted a missing auth initializer")
	}
}

func TestWarmAutomationRejectsLeaseThatDiedBeforeWrap(t *testing.T) {
	process := newAdapterTestProcess()
	pool, err := booking.NewWarmPool(context.Background(), func(context.Context, uint64) (booking.WarmBrowserProcess, error) {
		return process, nil
	}, booking.WarmPoolConfig{CloseTimeout: 20 * time.Millisecond, KillTimeout: 20 * time.Millisecond})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = pool.Close() })
	pool.SetDesired(1)
	lease, err := pool.Acquire(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	process.ExitWith(errors.New("driver exited"))
	select {
	case <-lease.Done():
	case <-time.After(time.Second):
		t.Fatal("dead lease did not close")
	}
	if _, err := WarmAutomationFromLease(lease); !errors.Is(err, booking.ErrWarmProcessInvalid) {
		t.Fatalf("dead lease wrap error = %v", err)
	}
}
