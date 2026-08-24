package main

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"

	localstore "github.com/cineko-org/client/internal/adapters/storage/local"
	"github.com/cineko-org/client/internal/interfaces/webui"
	"github.com/cineko-org/client/internal/logging"

	"github.com/wailsapp/wails/v2"
	"github.com/wailsapp/wails/v2/pkg/options"
	"github.com/wailsapp/wails/v2/pkg/options/assetserver"
	"github.com/wailsapp/wails/v2/pkg/options/mac"
	"github.com/wailsapp/wails/v2/pkg/options/windows"
	wailsruntime "github.com/wailsapp/wails/v2/pkg/runtime"
)

// runDesktopWindow owns Wails lifecycle signals after domain dependencies are ready.
func runDesktopWindow(
	app *DesktopApp,
	server *webui.Server,
	store *localstore.Store,
	embeddedProbe *embeddedProbe,
	dataDir string,
	startupReadyNonce string,
) error {
	startupFailure := make(chan error, 1)
	err := wails.Run(desktopWindowOptions(
		app, server, store, embeddedProbe, dataDir, startupReadyNonce, startupFailure,
	))
	select {
	case startupErr := <-startupFailure:
		err = errors.Join(err, fmt.Errorf("signal Launcher startup readiness: %w", startupErr))
	default:
	}
	return err
}

func desktopWindowOptions(
	app *DesktopApp,
	server *webui.Server,
	store *localstore.Store,
	embeddedProbe *embeddedProbe,
	dataDir string,
	startupReadyNonce string,
	startupFailure chan<- error,
) *options.App {
	return &options.App{
		Title: "Cineko", Width: 1440, Height: 980, MinWidth: 360, MinHeight: 600,
		BackgroundColour: options.NewRGB(10, 11, 14),
		AssetServer: &assetserver.Options{
			Assets: webui.Assets(), Handler: server.DesktopHandler(),
			Middleware: assetserver.ChainMiddleware(logging.HTTPMiddleware, webui.SecurityHeaders),
		},
		OnStartup: func(ctx context.Context) {
			if useLauncherOwnedActivationPolicy() {
				logging.Info(ctx, "Client attached to Launcher application", "event", "client.application.attached", "outcome", "succeeded")
			} else {
				logging.Warn(ctx, "Client could not attach to Launcher application", "event", "client.application.attached", "outcome", "failed")
			}
			startDesktopWindow(ctx, app, server, store, embeddedProbe, dataDir, startupReadyNonce, startupFailure)
		},
		Bind: []interface{}{app},
		SingleInstanceLock: &options.SingleInstanceLock{
			UniqueId: "io.cineko.desktop",
		},
		Mac: &mac.Options{
			Appearance: mac.NSAppearanceNameDarkAqua,
			About:      &mac.AboutInfo{Title: "Cineko", Message: "CGV booking control room"},
		},
		Windows: &windows.Options{WebviewUserDataPath: filepath.Join(dataDir, "webview")},
	}
}

func startDesktopWindow(
	ctx context.Context,
	app *DesktopApp,
	server *webui.Server,
	store *localstore.Store,
	embeddedProbe *embeddedProbe,
	dataDir string,
	startupReadyNonce string,
	startupFailure chan<- error,
) {
	app.startup(ctx)
	go superviseEmbeddedProbe(ctx, embeddedProbe, func(_ error) {
		server.RecordLocalSystemEvent(desktopErrorEvent(store.UserID(), "probe.runtime_failed",
			"일정 스캐너가 중지되었습니다. 앱을 다시 시작하세요."))
		wailsruntime.Quit(ctx)
	})
	if readyErr := signalDesktopStartupReady(dataDir, startupReadyNonce); readyErr != nil {
		select {
		case startupFailure <- readyErr:
		default:
		}
		wailsruntime.Quit(ctx)
	}
}
