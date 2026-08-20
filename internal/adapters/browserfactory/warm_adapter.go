package browserfactory

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"

	"github.com/cineko-org/client/internal/adapters/cgv"
	"github.com/cineko-org/client/internal/adapters/egress"
	"github.com/cineko-org/client/internal/booking"
)

// WarmAuthInitializer authenticates one newly created persistent CGV profile.
type WarmAuthInitializer func(context.Context, *cgv.Adapter) error

type warmProcess struct {
	adapter     *cgv.Adapter
	cleanup     func()
	ready       bool
	cleanupOnce sync.Once
}

func (process *warmProcess) PID() int              { return process.adapter.ProcessPID() }
func (process *warmProcess) ProfileDir() string    { return process.adapter.ProcessProfileDir() }
func (process *warmProcess) PageCount() int        { return process.adapter.ProcessPageCount() }
func (process *warmProcess) Ready() bool           { return process.ready && process.adapter.ProcessReady() }
func (process *warmProcess) Crashed() <-chan error { return process.adapter.ProcessCrashed() }
func (process *warmProcess) Close() error          { return process.adapter.CloseWithError() }
func (process *warmProcess) KillTree() error       { return process.adapter.KillProcessTree() }
func (process *warmProcess) Wait() error {
	err := process.adapter.WaitProcess()
	process.cleanupOnce.Do(func() {
		if process.cleanup != nil {
			process.cleanup()
		}
	})
	return err
}

// WarmAutomation is a CGV automation session leased from the Client booking pool.
type WarmAutomation struct {
	*cgv.Adapter
	lease    *booking.Lease
	stateMu  sync.Mutex
	retained bool
	closed   bool
}

// RetainPayment prevents the pool from replacing the browser while payment is open.
func (automation *WarmAutomation) RetainPayment() error {
	if automation == nil || automation.lease == nil {
		return booking.ErrProcessInvalid
	}
	automation.stateMu.Lock()
	defer automation.stateMu.Unlock()
	if automation.closed {
		return booking.ErrProcessInvalid
	}
	if err := automation.lease.RetainPayment(); err != nil {
		return err
	}
	automation.retained = true
	return nil
}

// PaymentFailure signals that a retained browser exited before payment finished.
func (automation *WarmAutomation) PaymentFailure() <-chan struct{} {
	if automation == nil || automation.lease == nil {
		return nil
	}
	return automation.lease.Done()
}

// Close releases the browser slot after ordinary work or payment completion.
func (automation *WarmAutomation) Close() {
	if automation == nil || automation.lease == nil {
		return
	}
	automation.stateMu.Lock()
	if automation.closed {
		automation.stateMu.Unlock()
		return
	}
	automation.closed = true
	retained := automation.retained
	automation.stateMu.Unlock()
	if retained {
		automation.lease.ReleasePayment()
		return
	}
	automation.lease.Release()
}

// WarmAutomationFromLease converts a pool lease into its CGV automation view.
func WarmAutomationFromLease(lease *booking.Lease) (*WarmAutomation, error) {
	if lease == nil {
		return nil, booking.ErrProcessInvalid
	}
	process, ok := lease.Process().(*warmProcess)
	if !ok || process == nil || process.adapter == nil {
		return nil, booking.ErrProcessInvalid
	}
	return &WarmAutomation{Adapter: process.adapter, lease: lease}, nil
}

// NewWarmBookingPool creates demand-driven, authenticated booking capacity.
func (factory *Factory) NewWarmBookingPool(
	parent context.Context,
	task Task,
	initializer WarmAuthInitializer,
) (*booking.Pool, error) {
	if factory == nil || parent == nil || initializer == nil {
		return nil, errors.New("warm booking pool dependencies are incomplete")
	}
	if task.Purpose != egress.PurposeSession || task.SessionKey == "" {
		return nil, errors.New("warm booking pool requires a session task")
	}
	return booking.NewPool(parent, func(ctx context.Context, sequence uint64) (booking.Process, error) {
		adapter, err := factory.openWarmAdapter(ctx, task, sequence, initializer)
		if err != nil {
			return nil, err
		}
		return &warmProcess{
			adapter: adapter, ready: true,
			cleanup: func() { _ = os.RemoveAll(adapter.ProcessProfileDir()) },
		}, nil
	}, booking.Config{MaxCapacity: booking.MaximumWarmBrowserCapacity})
}

func (factory *Factory) openWarmAdapter(
	ctx context.Context,
	task Task,
	sequence uint64,
	initializer WarmAuthInitializer,
) (*cgv.Adapter, error) {
	manager, err := factory.currentEgress()
	if err != nil {
		return nil, err
	}
	lease, _, err := factory.leaseForTask(ctx, manager, egress.PurposeSession)
	if err != nil {
		return nil, err
	}
	proxy := lease.Proxy()
	configuration := browserConfigForTask(factory.base, task)
	configuration.Headless = false
	configuration.StartMinimized = true
	configuration.RestoreSession = true
	configuration.ProfileDir, err = warmProfileForTask(factory.base.ProfileDir, task.SessionKey, sequence)
	if err != nil {
		return nil, err
	}
	if proxy != nil {
		configuration.Proxy = &cgv.BrowserProxy{Server: proxy.Server, Username: proxy.Username, Password: proxy.Password}
	}
	adapter, err := cgv.NewAdapter(ctx, configuration)
	if err != nil {
		_ = os.RemoveAll(configuration.ProfileDir)
		return nil, err
	}
	if err := initializer(ctx, adapter); err != nil {
		return nil, cleanupWarmInitializerFailure(adapter, configuration.ProfileDir, err)
	}
	return adapter, nil
}

type warmInitializerLifecycle interface {
	CloseWithError() error
	ProcessNeedsForcedReap() bool
	KillProcessTree() error
	WaitProcess() error
}

// cleanupWarmInitializerFailure closes a failed warm slot before removing its
// profile. A tree kill is reserved for an OS-observed live driver.
func cleanupWarmInitializerFailure(lifecycle warmInitializerLifecycle, profileDir string, initErr error) error {
	if lifecycle == nil {
		return errors.Join(initErr, errors.New("warm initializer lifecycle is missing"))
	}
	closeErr := lifecycle.CloseWithError()
	var killErr error
	if lifecycle.ProcessNeedsForcedReap() {
		// A transport close error can occur before Playwright owns cmd.Wait;
		// force the root down before the single fallback WaitProcess owner.
		killErr = lifecycle.KillProcessTree()
	}
	waitErr := lifecycle.WaitProcess()
	_ = os.RemoveAll(profileDir)
	return errors.Join(initErr, closeErr, killErr, waitErr)
}

func warmProfileForTask(base, sessionKey string, sequence uint64) (string, error) {
	if base == "" || sessionKey == "" {
		return "", errors.New("warm browser profile inputs are incomplete")
	}
	digest := sha256.Sum256([]byte(sessionProfileDomain + sessionKey))
	name := hex.EncodeToString(digest[:])
	root := filepath.Join(base+"-tasks", "warm", name, fmt.Sprintf("slot-%d", sequence))
	if err := os.MkdirAll(root, 0o700); err != nil {
		return "", fmt.Errorf("create warm browser profile: %w", err)
	}
	return root, nil
}
