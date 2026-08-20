package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	goruntime "runtime"
	"time"

	"github.com/cineko-org/client/internal/adapters/browserfactory"
	"github.com/cineko-org/client/internal/adapters/cgv"
	"github.com/cineko-org/client/internal/adapters/configbundle"
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
	"github.com/wailsapp/wails/v2/pkg/options/assetserver"
	"github.com/wailsapp/wails/v2/pkg/options/mac"
	wailsruntime "github.com/wailsapp/wails/v2/pkg/runtime"
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
	warmBookingPool, err := browsers.NewWarmBookingPool(
		context.Background(),
		browserfactory.Task{Purpose: egress.PurposeSession, SessionKey: store.UserID()},
		func(ctx context.Context, adapter *cgv.Adapter) error {
			if authenticated, checkErr := adapter.IsAuthenticated(ctx); checkErr != nil {
				return checkErr
			} else if authenticated {
				return nil
			}
			accountCredentials, loadErr := credentials.Load(ctx, store.UserID())
			if loadErr != nil {
				if errors.Is(loadErr, domain.ErrAccountCredentialsNotFound) {
					return fmt.Errorf("%w: warm booking authentication requires saved CGV credentials", booking.ErrWarmPermanent)
				}
				return fmt.Errorf("warm booking authentication requires saved CGV credentials: %w", loadErr)
			}
			return adapter.AuthenticateSavedUntil(ctx, accountCredentials, 5*time.Minute)
		},
	)
	if err != nil {
		return err
	}
	defer func() { runErr = errors.Join(runErr, warmBookingPool.Close()) }()
	seatMapCapabilities := &seatMapCapabilityState{}
	embeddedProbe, err := startEmbeddedProbe(
		context.Background(), store, dataDir, identity, browsers, seatMapCapabilities,
	)
	if err != nil {
		return err
	}
	defer func() { runErr = errors.Join(runErr, embeddedProbe.Close()) }()
	hooks := eventhook.New(nil)
	defer hooks.Close()

	server, err := webui.New(webui.Dependencies{
		Repository: store,
		Factory: func(ctx context.Context, background bool, purpose webui.AutomationPurpose, sessionKey string) (webui.Automation, error) {
			if purpose == webui.AutomationSession && sessionKey != "account" {
				lease, acquireErr := warmBookingPool.Acquire(ctx)
				if acquireErr != nil {
					return nil, acquireErr
				}
				automation, wrapErr := browserfactory.WarmAutomationFromLease(lease)
				if wrapErr != nil {
					lease.Release()
					return nil, wrapErr
				}
				return embeddedProbe.OpenBooking(func() (webui.Automation, error) { return automation, nil })
			}
			open := func() (webui.Automation, error) {
				return browsers.Open(ctx, browserTaskForUser(store.UserID(), background, purpose))
			}
			return open()
		},
		IDs: platform.IDGenerator{}, Clock: platform.Clock{}, Waiter: platform.Waiter{}, Events: hooks,
		AccountStateChanged: seatMapCapabilities.SetAuthenticated,
		Credentials:         credentials, UserID: store.UserID(),
		BookingDemandChanged:     warmBookingPool.SetDesired,
		BookingCapacityAvailable: func() bool { return warmBookingPool.Stats().Ready > 0 },
	})
	if err != nil {
		return err
	}
	warmBookingPool.SetReadyNotifier(server.NotifyBookingCapacityChanged)
	hooks.SetFailureHandler(func(failure eventhook.Failure) {
		server.RecordLocalSystemEvent(store.UserID(), "hook.delivery_failed", domain.EventError,
			fmt.Sprintf("%s 알림을 보내지 못했습니다. 외부 알림 설정을 확인하세요.", failure.Target.Name))
	})
	bundles, err := configbundle.New(store, platform.Clock{})
	if err != nil {
		return err
	}
	app := newDesktopApp(server, bundles, store, browsers, os.Args[1:], hooks)
	app.setUserID(store.UserID())
	app.execution = &desktopExecutionWorker{
		store: store, server: server, installationID: identity.InstallationID, userID: store.UserID(),
	}
	eventFailure := make(chan error, 1)
	startupFailure := make(chan error, 1)

	err = wails.Run(&options.App{
		Title: "Cineko", Width: 1440, Height: 980, MinWidth: 1120, MinHeight: 760,
		BackgroundColour: options.NewRGB(10, 11, 14),
		AssetServer: &assetserver.Options{
			Assets: webui.Assets(), Handler: server.DesktopHandler(),
			Middleware: webui.SecurityHeaders,
		},
		OnStartup: func(ctx context.Context) {
			app.startup(ctx)
			go superviseCentralEvents(ctx, store, func(eventErr error) {
				server.RecordLocalSystemEvent(store.UserID(), "central.event_stream_failed", domain.EventError,
					"Cineko 변경 알림 연결이 중지되었습니다. 앱을 다시 시작하세요.")
				select {
				case eventFailure <- eventErr:
				default:
				}
				wailsruntime.Quit(ctx)
			})
			go superviseEmbeddedProbe(ctx, embeddedProbe, func(probeErr error) {
				server.RecordLocalSystemEvent(store.UserID(), "probe.runtime_failed", domain.EventError,
					"분산 좌석 탐색이 중지되었습니다. 앱을 다시 시작하세요.")
				wailsruntime.Quit(ctx)
			})
			if readyErr := signalDesktopStartupReady(dataDir, identity.StartupReadyNonce); readyErr != nil {
				startupFailure <- readyErr
				wailsruntime.Quit(ctx)
			}
		},
		OnDomReady: app.domReady,
		Bind:       []interface{}{app},
		SingleInstanceLock: &options.SingleInstanceLock{
			UniqueId:               "io.cineko.desktop",
			OnSecondInstanceLaunch: app.secondInstance,
		},
		Mac: &mac.Options{
			Appearance: mac.NSAppearanceNameDarkAqua,
			About:      &mac.AboutInfo{Title: "Cineko", Message: "CGV booking control room"},
			OnFileOpen: app.openFile,
		},
	})
	select {
	case startupErr := <-startupFailure:
		err = errors.Join(err, fmt.Errorf("signal Launcher startup readiness: %w", startupErr))
	default:
	}
	select {
	case eventErr := <-eventFailure:
		err = errors.Join(err, eventErr)
	default:
	}
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
