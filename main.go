package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	goruntime "runtime"

	"github.com/cineko-org/client/internal/adapters/browserfactory"
	"github.com/cineko-org/client/internal/adapters/configbundle"
	"github.com/cineko-org/client/internal/adapters/credentialvault"
	"github.com/cineko-org/client/internal/adapters/egress"
	"github.com/cineko-org/client/internal/adapters/eventhook"
	centralstore "github.com/cineko-org/client/internal/adapters/storage/centralhttp"
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
	eventContext, stopEvents := context.WithCancel(context.Background())
	eventDone := make(chan error, 1)
	go func() { eventDone <- store.WatchEvents(eventContext) }()
	defer func() {
		stopEvents()
		if eventErr := <-eventDone; !errors.Is(eventErr, context.Canceled) {
			runErr = errors.Join(runErr, eventErr)
		}
	}()
	if err := prepareDesktopState(context.Background(), store, identity, dataDir); err != nil {
		return err
	}
	browsers, err := browserfactory.NewFromEnvironment(dataDir)
	if err != nil {
		return err
	}
	defer browsers.Close()
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
			open := func() (webui.Automation, error) {
				return browsers.Open(ctx, browserTaskForUser(store.UserID(), background, purpose))
			}
			if purpose == webui.AutomationSession && sessionKey != "account" {
				return embeddedProbe.OpenBooking(open)
			}
			return open()
		},
		IDs: platform.IDGenerator{}, Clock: platform.Clock{}, Waiter: platform.Waiter{}, Events: hooks,
		AccountStateChanged: seatMapCapabilities.SetAuthenticated,
		Credentials:         credentialvault.New(), UserID: store.UserID(),
	})
	if err != nil {
		return err
	}
	hooks.SetFailureHandler(func(failure eventhook.Failure) {
		server.RecordLocalSystemEvent(store.UserID(), "hook.delivery_failed", domain.EventError,
			fmt.Sprintf("%s 알림 전송에 실패했습니다: %v", failure.Target.Name, failure.Err))
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

	err = wails.Run(&options.App{
		Title: "Cineko", Width: 1440, Height: 980, MinWidth: 1120, MinHeight: 760,
		BackgroundColour: options.NewRGB(10, 11, 14),
		AssetServer: &assetserver.Options{
			Assets: webui.Assets(), Handler: server.DesktopHandler(),
			Middleware: webui.SecurityHeaders,
		},
		OnStartup: func(ctx context.Context) {
			app.startup(ctx)
			go superviseEmbeddedProbe(ctx, embeddedProbe, func(probeErr error) {
				server.RecordLocalSystemEvent(store.UserID(), "probe.runtime_failed", domain.EventError,
					"분산 좌석 탐색 런타임이 중지되었습니다: "+probeErr.Error())
				wailsruntime.Quit(ctx)
			})
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
	if app.updateNeeded.Load() {
		return errors.Join(err, errUpdateRequired)
	}
	return err
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
