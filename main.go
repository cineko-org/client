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
	"github.com/cineko-org/client/internal/interfaces/webui"
	"github.com/cineko-org/client/internal/platform"
	clientpb "github.com/cineko-org/contracts/gen/go/cineko/client"

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
	store, launchContext, startupReadyNonce, err := openDesktopStore(context.Background(), dataDir, os.Stdin)
	if err != nil {
		return err
	}
	defer func() { runErr = errors.Join(runErr, store.Close()) }()
	if err := prepareDesktopState(context.Background(), store, launchContext, dataDir); err != nil {
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
	embeddedProbe, err := startEmbeddedProbe(context.Background(), store, dataDir, launchContext)
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
		server.RecordLocalSystemEvent(desktopErrorEvent(
			store.UserID(), "hook.delivery_failed",
			fmt.Sprintf("%s 알림을 보내지 못했습니다. 외부 알림 설정을 확인하세요.", failure.Target.GetName()),
		))
	})
	app := newDesktopApp(server, store, browsers, hooks)
	app.setUserID(store.UserID())
	app.execution = &desktopExecutionWorker{
		store: store, server: server, installationID: launchContext.GetInstallationId(), userID: store.UserID(),
	}
	err = runDesktopWindow(app, server, store, embeddedProbe, dataDir, startupReadyNonce)
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
	launchContext *clientpb.LaunchContext,
	dataDir string,
) error {
	if launchContext == nil {
		return errors.New("desktop launch context is required")
	}
	platformName := goruntime.GOOS
	architecture := goruntime.GOARCH
	installationID := launchContext.GetInstallationId()
	deviceID := launchContext.GetDeviceId()
	clientVersion := launchContext.GetClientVersion()
	device := clientpb.Device_builder{
		InstallationId: &installationID,
		DeviceId:       &deviceID,
		Platform:       &platformName,
		Architecture:   &architecture,
		AppVersion:     &clientVersion,
	}.Build()
	if _, err := store.RegisterDevice(ctx, device); err != nil {
		return fmt.Errorf("register desktop with Central: %w", err)
	}
	select {
	case <-store.UpdateRequired():
		return errUpdateRequired
	default:
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
			// A member session is useful when present, but CGV's supported
			// non-member booking path is also valid warm capacity. Keep the
			// browser ready in either case; execution reports a distinct
			// authentication-required result only if the provider demands it.
			_, err := adapter.IsAuthenticated(ctx)
			return err
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
	for _, name := range []string{"cineko.sqlite", "cineko.sqlite-wal", "cineko.sqlite-shm", "settings.json", "settings.json.migrating"} {
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
