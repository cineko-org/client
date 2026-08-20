package browserfactory

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"

	"github.com/cineko-org/client/internal/adapters/cgv"
	"github.com/cineko-org/client/internal/adapters/egress"
	"github.com/cineko-org/client/internal/booking"
)

// WarmAuthInitializer must perform an explicit authenticated-state check (and
// restore credentials when needed) before the slot can be advertised ready.
// The account profile is never shared with a booking slot.
type WarmAuthInitializer func(context.Context, *cgv.Adapter) error

type warmAdapterProcess struct {
	adapter     *cgv.Adapter
	cleanup     func()
	ready       bool
	cleanupOnce sync.Once
}

func (process *warmAdapterProcess) PID() int              { return process.adapter.ProcessPID() }
func (process *warmAdapterProcess) ProfileDir() string    { return process.adapter.ProcessProfileDir() }
func (process *warmAdapterProcess) PageCount() int        { return process.adapter.ProcessPageCount() }
func (process *warmAdapterProcess) Ready() bool           { return process.ready }
func (process *warmAdapterProcess) Crashed() <-chan error { return process.adapter.ProcessCrashed() }
func (process *warmAdapterProcess) Wait() error {
	err := process.adapter.WaitProcess()
	process.cleanupOnce.Do(func() {
		if process.cleanup != nil {
			process.cleanup()
		}
	})
	return err
}
func (process *warmAdapterProcess) Close() error {
	process.adapter.Close()
	return nil
}
func (process *warmAdapterProcess) KillTree() error {
	return process.adapter.KillProcessTree()
}

// WarmBrowserAutomation binds the existing Automation implementation to a
// single warm lease. Closing it consumes that process; retaining it keeps the
// payment handoff exclusive until ReleasePayment is called.
type WarmBrowserAutomation struct {
	*cgv.Adapter
	lease    *booking.WarmBrowserLease
	mu       sync.Mutex
	retained bool
}

// RetainPayment reserves this browser until the user finishes the payment handoff.
func (automation *WarmBrowserAutomation) RetainPayment() error {
	automation.mu.Lock()
	defer automation.mu.Unlock()
	if automation.retained {
		return nil
	}
	if err := automation.lease.RetainPayment(); err != nil {
		return err
	}
	automation.retained = true
	return nil
}

// PaymentFailure closes when the leased browser exits unexpectedly, allowing
// WebUI to mark a retained handoff unknown immediately instead of waiting for
// its 15-minute timer.
func (automation *WarmBrowserAutomation) PaymentFailure() <-chan struct{} {
	return automation.lease.Done()
}

// Close releases the lease; a retained payment lease is consumed only when
// ReleasePayment is called by the WebUI lifecycle.
func (automation *WarmBrowserAutomation) Close() {
	automation.mu.Lock()
	retained := automation.retained
	automation.mu.Unlock()
	if retained {
		automation.lease.ReleasePayment()
		return
	}
	automation.lease.Release()
}

// WarmAutomationFromLease adapts one booking lease to the existing CGV
// automation contract without creating a second page or process.
func WarmAutomationFromLease(lease *booking.WarmBrowserLease) (*WarmBrowserAutomation, error) {
	if lease == nil {
		return nil, errors.New("warm browser lease is required")
	}
	select {
	case <-lease.Done():
		lease.Release()
		return nil, errors.Join(booking.ErrWarmProcessInvalid, lease.Err())
	default:
	}
	process, ok := lease.Process().(*warmAdapterProcess)
	if !ok || process.adapter == nil {
		return nil, errors.New("warm browser lease does not contain a CGV adapter")
	}
	return &WarmBrowserAutomation{Adapter: process.adapter, lease: lease}, nil
}

// NewWarmBookingPool creates per-slot Playwright drivers and disposable
// profiles. It intentionally does not use Factory.browserPool, whose shared
// driver PID cannot be safely killed per slot.
func (factory *Factory) NewWarmBookingPool(
	parent context.Context,
	task Task,
	initializer WarmAuthInitializer,
) (*booking.WarmPool, error) {
	if task.Purpose != egress.PurposeSession || task.SessionKey == "" || initializer == nil {
		return nil, errors.New("warm booking pool requires an authenticated session task")
	}
	return booking.NewWarmPool(parent, func(ctx context.Context, slot uint64) (booking.WarmBrowserProcess, error) {
		adapter, cleanup, err := factory.openWarmAdapter(ctx, task, slot)
		if err != nil {
			return nil, err
		}
		process := &warmAdapterProcess{adapter: adapter, cleanup: cleanup}
		if err := initializer(ctx, adapter); err != nil {
			return process, fmt.Errorf("initialize warm booking authentication: %w", err)
		}
		process.ready = true
		return process, nil
	}, booking.WarmPoolConfig{})
}

func (factory *Factory) openWarmAdapter(ctx context.Context, task Task, slot uint64) (*cgv.Adapter, func(), error) {
	manager, err := factory.currentEgress()
	if err != nil {
		return nil, nil, err
	}
	lease, _, err := factory.leaseForTask(ctx, manager, task.Purpose)
	if err != nil {
		return nil, nil, err
	}
	profile, cleanup, err := factory.warmProfileForTask(task, slot)
	if err != nil {
		return nil, nil, err
	}
	configuration := browserConfigForTask(factory.base, task)
	configuration.ProfileDir = profile
	if proxy := lease.Proxy(); proxy != nil {
		configuration.Proxy = &cgv.BrowserProxy{Server: proxy.Server, Username: proxy.Username, Password: proxy.Password}
	}
	adapter, err := cgv.NewAdapter(ctx, configuration)
	if err != nil {
		cleanup()
		return nil, nil, err
	}
	return adapter, cleanup, nil
}

func (factory *Factory) warmProfileForTask(task Task, slot uint64) (string, func(), error) {
	name, err := sessionProfileName(fmt.Sprintf("%s/warm/%d", task.SessionKey, slot))
	if err != nil {
		return "", nil, err
	}
	root := filepath.Join(factory.base.ProfileDir+"-tasks", "warm")
	if err := os.MkdirAll(root, 0o700); err != nil {
		return "", nil, err
	}
	path := filepath.Join(root, name)
	if err := os.MkdirAll(path, 0o700); err != nil {
		return "", nil, err
	}
	return path, func() { _ = os.RemoveAll(path) }, nil
}
