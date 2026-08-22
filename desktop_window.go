package main

import (
	"context"
	"errors"
	"fmt"

	centralstore "github.com/cineko-org/client/internal/adapters/storage/centralhttp"
	"github.com/cineko-org/client/internal/interfaces/webui"

	"github.com/wailsapp/wails/v2"
	"github.com/wailsapp/wails/v2/pkg/options"
	"github.com/wailsapp/wails/v2/pkg/options/assetserver"
	"github.com/wailsapp/wails/v2/pkg/options/mac"
	wailsruntime "github.com/wailsapp/wails/v2/pkg/runtime"
)

// runDesktopWindow owns Wails lifecycle signals after domain dependencies are ready.
func runDesktopWindow(
	app *DesktopApp,
	server *webui.Server,
	store *centralstore.Store,
	embeddedProbe *embeddedProbe,
	dataDir string,
	startupReadyNonce string,
) error {
	eventFailure := make(chan error, 1)
	startupFailure := make(chan error, 1)
	err := wails.Run(desktopWindowOptions(
		app, server, store, embeddedProbe, dataDir, startupReadyNonce, eventFailure, startupFailure,
	))
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
	return err
}

func desktopWindowOptions(
	app *DesktopApp,
	server *webui.Server,
	store *centralstore.Store,
	embeddedProbe *embeddedProbe,
	dataDir string,
	startupReadyNonce string,
	eventFailure chan<- error,
	startupFailure chan<- error,
) *options.App {
	return &options.App{
		Title: "Cineko", Width: 1440, Height: 980, MinWidth: 360, MinHeight: 600,
		BackgroundColour: options.NewRGB(10, 11, 14),
		AssetServer: &assetserver.Options{
			Assets: webui.Assets(), Handler: server.DesktopHandler(),
			Middleware: webui.SecurityHeaders,
		},
		OnStartup: func(ctx context.Context) {
			startDesktopWindow(ctx, app, server, store, embeddedProbe, dataDir, startupReadyNonce, eventFailure, startupFailure)
		},
		Bind: []interface{}{app},
		SingleInstanceLock: &options.SingleInstanceLock{
			UniqueId: "io.cineko.desktop",
		},
		Mac: &mac.Options{
			Appearance: mac.NSAppearanceNameDarkAqua,
			About:      &mac.AboutInfo{Title: "Cineko", Message: "CGV booking control room"},
		},
	}
}

func startDesktopWindow(
	ctx context.Context,
	app *DesktopApp,
	server *webui.Server,
	store *centralstore.Store,
	embeddedProbe *embeddedProbe,
	dataDir string,
	startupReadyNonce string,
	eventFailure chan<- error,
	startupFailure chan<- error,
) {
	app.startup(ctx)
	go superviseCentralEvents(ctx, store, func(eventErr error) {
		server.RecordLocalSystemEvent(desktopErrorEvent(store.UserID(), "central.event_stream_failed",
			"Cineko 변경 알림 연결이 중지되었습니다. 앱을 다시 시작하세요."))
		select {
		case eventFailure <- eventErr:
		default:
		}
		wailsruntime.Quit(ctx)
	})
	go superviseEmbeddedProbe(ctx, embeddedProbe, func(_ error) {
		server.RecordLocalSystemEvent(desktopErrorEvent(store.UserID(), "probe.runtime_failed",
			"분산 좌석 탐색이 중지되었습니다. 앱을 다시 시작하세요."))
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
