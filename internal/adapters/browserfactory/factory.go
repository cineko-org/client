package browserfactory

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/cineko-org/client/internal/adapters/cgv"
	"github.com/cineko-org/client/internal/adapters/egress"
)

var ErrClosed = errors.New("browser factory is closed")

type Task struct {
	Purpose        egress.Purpose
	SessionKey     string
	Headless       bool
	StartMinimized bool
}

const (
	defaultCapacity        = 3
	maximumSessionCapacity = 1
	sessionProfileDomain   = "cineko-browser-session-v1\x00"
)

// Factory owns three isolated browser-task slots, the lazy Playwright runtime,
// and egress leases. Authenticated work is serialized and each local user is
// assigned one persistent account profile so cookies are never shared between
// users.
type Factory struct {
	base           cgv.BrowserConfig
	egress         *egress.Manager
	slots          chan int
	sessions       chan struct{}
	done           chan struct{}
	closeOnce      sync.Once
	mu             sync.Mutex
	closed         bool
	pool           *cgv.BrowserPool
	sessionContext context.Context
	cancelSession  context.CancelFunc
	sessionLease   *egress.Lease
	sessionManager *egress.Manager
}

func New(base cgv.BrowserConfig, egressManager *egress.Manager) (*Factory, error) {
	if egressManager == nil {
		return nil, errors.New("egress manager is required")
	}
	base.Capacity = defaultCapacity
	sessionContext, cancelSession := context.WithCancel(context.Background())
	factory := &Factory{
		base: base, egress: egressManager, slots: make(chan int, defaultCapacity),
		sessions: make(chan struct{}, maximumSessionCapacity), done: make(chan struct{}),
		sessionContext: sessionContext, cancelSession: cancelSession,
	}
	for slot := range defaultCapacity {
		factory.slots <- slot
	}
	for range maximumSessionCapacity {
		factory.sessions <- struct{}{}
	}
	return factory, nil
}

func NewFromEnvironment(dataDir string) (*Factory, error) {
	egressManager, err := egress.NewFromEnvironment()
	if err != nil {
		return nil, err
	}
	configuration := cgv.DefaultBrowserConfig()
	configuration.ProfileDir = filepath.Join(dataDir, "chrome-profile")
	configuration.ArtifactsDir = filepath.Join(dataDir, "artifacts")
	if chromePath := strings.TrimSpace(os.Getenv("CINEKO_CHROME_PATH")); chromePath != "" {
		configuration.ChromePath = chromePath
	}
	return New(configuration, egressManager)
}

// ConfigureEgress atomically changes the proxy policy used by future browser
// tasks. Existing tasks keep their original lease for their full lifetime.
func (factory *Factory) ConfigureEgress(config egress.Config) error {
	manager, err := egress.New(config)
	if err != nil {
		return err
	}
	factory.mu.Lock()
	defer factory.mu.Unlock()
	if factory.closed {
		return ErrClosed
	}
	factory.egress = manager
	return nil
}

func (factory *Factory) Open(ctx context.Context, task Task) (*cgv.Adapter, error) {
	if ctx == nil {
		return nil, errors.New("browser task context is required")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	sessionHeld := false
	if task.Purpose == egress.PurposeSession {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-factory.done:
			return nil, ErrClosed
		case <-factory.sessions:
			sessionHeld = true
		}
	}
	releaseSession := func() {
		if sessionHeld {
			factory.sessions <- struct{}{}
			sessionHeld = false
		}
	}
	select {
	case <-ctx.Done():
		releaseSession()
		return nil, ctx.Err()
	case <-factory.done:
		releaseSession()
		return nil, ErrClosed
	case slot := <-factory.slots:
		return factory.openInSlot(ctx, task, slot, releaseSession)
	}
}

func (factory *Factory) openInSlot(
	ctx context.Context,
	task Task,
	slot int,
	releaseSession func(),
) (*cgv.Adapter, error) {
	releaseSlot := func() {
		factory.slots <- slot
		releaseSession()
	}
	succeeded := false
	defer cleanupUnlessSucceeded(&succeeded, releaseSlot)
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	manager, err := factory.currentEgress()
	if err != nil {
		return nil, err
	}
	lease, sharedLease, err := factory.leaseForTask(ctx, manager, task.Purpose)
	if err != nil {
		return nil, err
	}
	releaseLease := !sharedLease
	defer func() {
		if releaseLease {
			_ = lease.Close()
		}
	}()
	browserContext := lease.Context()
	releaseBrowserContext := func() {}
	if sharedLease {
		var cancel context.CancelFunc
		browserContext, cancel = context.WithCancel(ctx)
		stopLeaseWatch := context.AfterFunc(lease.Context(), cancel)
		releaseBrowserContext = func() {
			_ = stopLeaseWatch()
			cancel()
		}
		defer cleanupUnlessSucceeded(&succeeded, releaseBrowserContext)
	}

	pool, err := factory.browserPool()
	if err != nil {
		return nil, err
	}
	configuration := browserConfigForTask(factory.base, task)
	profileDir, cleanupProfile, err := factory.profileForTask(task, slot)
	if err != nil {
		return nil, err
	}
	configuration.ProfileDir = profileDir
	if task.Purpose == egress.PurposeSession {
		configuration.SessionStatePath = sessionStatePath(profileDir)
	}
	if cleanupProfile != nil {
		defer cleanupUnlessSucceeded(&succeeded, cleanupProfile)
	}
	if proxy := lease.Proxy(); proxy != nil {
		configuration.Proxy = &cgv.BrowserProxy{
			Server: proxy.Server, Username: proxy.Username, Password: proxy.Password,
		}
	}
	adapter, err := pool.NewAdapter(browserContext, configuration)
	if err != nil {
		return nil, err
	}
	adapter.AddCloseHook(func() {
		releaseBrowserContext()
		if !sharedLease {
			_ = lease.Close()
		}
		if cleanupProfile != nil {
			cleanupProfile()
		}
		releaseSlot()
	})
	succeeded = true
	releaseLease = false
	return adapter, nil
}

func cleanupUnlessSucceeded(succeeded *bool, cleanup func()) {
	if !*succeeded {
		cleanup()
	}
}

func (factory *Factory) leaseForTask(
	ctx context.Context,
	manager *egress.Manager,
	purpose egress.Purpose,
) (*egress.Lease, bool, error) {
	if purpose != egress.PurposeSession {
		lease, err := manager.Acquire(ctx, purpose)
		return lease, false, err
	}

	factory.mu.Lock()
	current := factory.sessionLease
	currentManager := factory.sessionManager
	if current != nil && currentManager == manager && context.Cause(current.Context()) == nil {
		factory.mu.Unlock()
		return current, true, nil
	}
	factory.sessionLease = nil
	factory.sessionManager = nil
	factory.mu.Unlock()
	if current != nil {
		_ = current.Close()
	}

	lease, err := manager.Acquire(factory.sessionContext, purpose)
	if err != nil {
		return nil, false, err
	}
	factory.mu.Lock()
	if factory.closed {
		factory.mu.Unlock()
		_ = lease.Close()
		return nil, false, ErrClosed
	}
	factory.sessionLease = lease
	factory.sessionManager = manager
	factory.mu.Unlock()
	return lease, true, nil
}

func browserConfigForTask(base cgv.BrowserConfig, task Task) cgv.BrowserConfig {
	base.Headless = task.Headless
	base.StartMinimized = task.StartMinimized
	base.RestoreSession = task.Purpose == egress.PurposeSession
	base.BlockResources = task.Purpose == egress.PurposeScan
	base.UserAgentMode = cgv.UserAgentSession
	switch task.Purpose {
	case egress.PurposeSession:
		base.Headless = false
		base.StartMinimized = task.StartMinimized || task.Headless
	case egress.PurposeScan:
		base.UserAgentMode = cgv.UserAgentRandomizedScan
	}
	return base
}

func (factory *Factory) profileForTask(task Task, slot int) (string, func(), error) {
	isolationRoot := factory.base.ProfileDir + "-tasks"
	if task.Purpose == egress.PurposeScan {
		root := filepath.Join(isolationRoot, "scans")
		if err := os.MkdirAll(root, 0o700); err != nil {
			return "", nil, err
		}
		path, err := os.MkdirTemp(root, fmt.Sprintf("slot-%d-", slot))
		if err != nil {
			return "", nil, err
		}
		return path, func() { _ = os.RemoveAll(path) }, nil
	}
	path, err := sessionProfilePath(factory.base.ProfileDir, task.SessionKey)
	if err != nil {
		return "", nil, err
	}
	if err := os.MkdirAll(path, 0o700); err != nil {
		return "", nil, fmt.Errorf("create browser session profile: %w", err)
	}
	return path, nil, nil
}

func sessionProfilePath(base, sessionKey string) (string, error) {
	profileName, err := sessionProfileName(sessionKey)
	if err != nil {
		return "", err
	}
	return filepath.Join(base+"-tasks", "sessions", profileName), nil
}

func sessionStatePath(profileDir string) string {
	return filepath.Join(profileDir, "cgv-storage-state.json")
}

func sessionProfileName(sessionKey string) (string, error) {
	sessionKey = strings.TrimSpace(sessionKey)
	if sessionKey == "" {
		return "", errors.New("browser session key is required")
	}
	digest := sha256.Sum256([]byte(sessionProfileDomain + sessionKey))
	return hex.EncodeToString(digest[:]), nil
}

func (factory *Factory) Close() {
	factory.closeOnce.Do(func() {
		factory.mu.Lock()
		factory.closed = true
		pool := factory.pool
		sessionLease := factory.sessionLease
		close(factory.done)
		factory.mu.Unlock()
		if pool != nil {
			pool.Close()
		}
		if sessionLease != nil {
			_ = sessionLease.Close()
		}
		factory.cancelSession()
	})
}

func (factory *Factory) currentEgress() (*egress.Manager, error) {
	factory.mu.Lock()
	defer factory.mu.Unlock()
	if factory.closed {
		return nil, ErrClosed
	}
	return factory.egress, nil
}

func (factory *Factory) browserPool() (*cgv.BrowserPool, error) {
	factory.mu.Lock()
	defer factory.mu.Unlock()
	if factory.closed {
		return nil, ErrClosed
	}
	if factory.pool != nil {
		return factory.pool, nil
	}
	pool, err := cgv.NewBrowserPool(factory.base)
	if err != nil {
		return nil, err
	}
	factory.pool = pool
	return pool, nil
}
