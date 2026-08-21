package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	goruntime "runtime"

	"github.com/cineko-org/client/internal/adapters/browserfactory"
	"github.com/cineko-org/client/internal/adapters/cgv"
	"github.com/cineko-org/client/internal/adapters/credentialvault"
	"github.com/cineko-org/client/internal/adapters/egress"
	"github.com/cineko-org/client/internal/adapters/eventhook"
	centralstore "github.com/cineko-org/client/internal/adapters/storage/centralhttp"
	"github.com/cineko-org/client/internal/booking"
	"github.com/cineko-org/client/internal/domain"
	"github.com/cineko-org/client/internal/interfaces/webui"
	"github.com/cineko-org/client/internal/platform"
	central "github.com/cineko-org/contracts/v3"

	"github.com/wailsapp/wails/v2"
	"github.com/wailsapp/wails/v2/pkg/options"
)

const updateRequiredExitCode = 75

var errUpdateRequired = errors.New("client update required")

var desktopVersion = "dev"

func main() {
	if err := runDesktop(); err != nil {
		_, _ = fmt.Fprintf(os.Stderr, "cineko: %v\n", err)
		os.Exit(desktopExitCode(err))
	}
}

func desktopExitCode(err error) int {
	if errors.Is(err, errUpdateRequired) {
		return updateRequiredExitCode
	}
	return 1
}

func runDesktop() (runErr error) {
	if bindingMode {
		return wails.Run(&options.App{Bind: []interface{}{&DesktopApp{}}})
	}
	dataDir, err := desktopDataDir()
	if err != nil {
		return err
	}
	store, identity, err := openDesktopStore(context.Background(), dataDir, os.Stdin)
	if err != nil {
		return err
	}
	defer func() { runErr = errors.Join(runErr, store.Close()) }()
	if err := prepareDesktopState(context.Background(), store, identity, dataDir); err != nil {
		return err
	}
	browsers, err := browserfactory.NewFromEnvironment(dataDir)
	if err != nil {
		return err
	}
	defer browsers.Close()
	credentials := credentialvault.New()
	warmPool, err := newWarmBookingPool(context.Background(), browsers, store.UserID())
	if err != nil {
		return err
	}
	defer func() { runErr = errors.Join(runErr, warmPool.Close()) }()
	embeddedProbe, err := startEmbeddedProbe(context.Background(), store, dataDir, identity)
	if err != nil {
		return err
	}
	defer func() { runErr = errors.Join(runErr, embeddedProbe.Close()) }()
	hooks := eventhook.New(nil)
	defer hooks.Close()

	server, err := webui.New(webui.Dependencies{
		Repository: store,
		Factory:    newAutomationFactory(browsers, warmPool, embeddedProbe, store.UserID()),
		IDs:        platform.IDGenerator{}, Clock: platform.Clock{}, Waiter: platform.Waiter{}, Events: hooks,
		Credentials: credentials, UserID: store.UserID(),
		BookingDemandChanged: func(active bool) {
			if active {
				warmPool.SetDesired(booking.DefaultWarmBrowserCapacity)
				return
			}
			warmPool.SetDesired(0)
		},
		BookingCapacityAvailable: func() bool { return warmPool.Stats().Ready > 0 },
	})
	if err != nil {
		return err
	}
	warmPool.SetReadyNotifier(server.NotifyBookingCapacityChanged)
	hooks.SetFailureHandler(func(failure eventhook.Failure) {
		server.RecordLocalSystemEvent(store.UserID(), "hook.delivery_failed", domain.EventError,
			fmt.Sprintf("%s 알림을 보내지 못했습니다. 외부 알림 설정을 확인하세요.", failure.Target.Name))
	})
	app := newDesktopApp(server, store, browsers, hooks)
	app.setUserID(store.UserID())
	app.execution = &desktopExecutionWorker{
		store: store, server: server, installationID: identity.InstallationID, userID: store.UserID(),
	}
	err = runDesktopWindow(app, server, store, embeddedProbe, dataDir, identity)
	if app.updateNeeded.Load() {
		return errors.Join(err, errUpdateRequired)
	}
	return err
}

type centralEventWatcher interface {
	WatchEvents(context.Context) error
}

func superviseCentralEvents(ctx context.Context, watcher centralEventWatcher, onFailure func(error)) {
	err := watcher.WatchEvents(ctx)
	if err == nil || errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) || ctx.Err() != nil {
		return
	}
	onFailure(err)
}

func prepareDesktopState(
	ctx context.Context,
	store *centralstore.Store,
	identity desktopRuntimeIdentity,
	dataDir string,
) error {
	if _, err := store.RegisterDevice(ctx, central.ClientDevice{
		InstallationID: identity.InstallationID,
		DeviceID:       identity.DeviceID,
		Platform:       goruntime.GOOS,
		Arch:           goruntime.GOARCH,
		AppVersion:     identity.ClientVersion,
	}); err != nil {
		return fmt.Errorf("register desktop with Central: %w", err)
	}
	select {
	case <-store.UpdateRequired():
		return errUpdateRequired
	default:
	}
	if err := migrateLegacyDesktopSettings(ctx, store, dataDir); err != nil {
		return fmt.Errorf("migrate local settings to Central: %w", err)
	}
	return discardLegacyLocalDomainState(dataDir)
}

func browserTaskForUser(userID string, background bool, purpose webui.AutomationPurpose) browserfactory.Task {
	task := browserfactory.Task{Purpose: egress.PurposeSession, SessionKey: userID, Headless: background}
	if purpose == webui.AutomationScan {
		task.Purpose = egress.PurposeScan
		task.SessionKey = ""
	}
	return task
}

func newWarmBookingPool(
	ctx context.Context,
	factory *browserfactory.Factory,
	userID string,
) (*booking.Pool, error) {
	if factory == nil || userID == "" {
		return nil, errors.New("warm booking pool dependencies are incomplete")
	}
	return factory.NewWarmBookingPool(
		ctx,
		browserfactory.Task{Purpose: egress.PurposeSession, SessionKey: userID},
		func(ctx context.Context, adapter *cgv.Adapter) error {
			authenticated, err := adapter.IsAuthenticated(ctx)
			if err != nil {
				return err
			}
			if !authenticated {
				return fmt.Errorf("%w: manual CGV login is required", booking.ErrPermanent)
			}
			return nil
		},
	)
}

func newAutomationFactory(
	factory *browserfactory.Factory,
	warmPool *booking.Pool,
	probe *embeddedProbe,
	userID string,
) webui.AutomationFactory {
	return func(ctx context.Context, background bool, purpose webui.AutomationPurpose, sessionKey string) (webui.Automation, error) {
		open := func() (webui.Automation, error) {
			return factory.Open(ctx, browserTaskForUser(userID, background, purpose))
		}
		if purpose == webui.AutomationCancellation {
			return probe.OpenBooking(open)
		}
		if purpose != webui.AutomationSession || sessionKey == "account" {
			return open()
		}
		lease, err := warmPool.Acquire(ctx)
		if err != nil {
			return nil, err
		}
		automation, err := browserfactory.WarmAutomationFromLease(lease)
		if err != nil {
			lease.Release()
			return nil, err
		}
		return probe.OpenBooking(func() (webui.Automation, error) { return automation, nil })
	}
}

func discardLegacyLocalDomainState(dataDir string) error {
	for _, name := range []string{"cineko.sqlite", "cineko.sqlite-wal", "cineko.sqlite-shm"} {
		if err := os.Remove(filepath.Join(dataDir, name)); err != nil && !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("remove obsolete local domain state %s: %w", name, err)
		}
	}
	return nil
}

func desktopDataDir() (string, error) {
	if value := os.Getenv("CINEKO_DATA_DIR"); value != "" {
		return value, nil
	}
	root, err := os.UserConfigDir()
	if err != nil {
		return "", fmt.Errorf("find application support directory: %w", err)
	}
	return filepath.Join(root, "Cineko"), nil
}
