package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"sync"
	"sync/atomic"

	"buf.build/go/protovalidate"
	"github.com/cineko-org/client/internal/adapters/egress"
	"github.com/cineko-org/client/internal/application"
	"github.com/cineko-org/client/internal/interfaces/webui"
	clientpb "github.com/cineko-org/contracts/v3/gen/go/cineko/client"
	seatmappb "github.com/cineko-org/contracts/v3/gen/go/cineko/seatmap"
	servicepb "github.com/cineko-org/contracts/v3/gen/go/cineko/service"

	"github.com/wailsapp/wails/v2/pkg/runtime"
	"google.golang.org/protobuf/encoding/protojson"
)

const (
	desktopSeatMapEvent      = "cineko.seat-map"
	desktopSeatMapErrorEvent = "cineko.seat-map.error"
)

type desktopSettingsRepository interface {
	GetSettings(context.Context, *clientpb.Settings) (int64, error)
	PutSettings(context.Context, *clientpb.Settings, int64) error
}

type desktopSeatMapWatcher interface {
	WatchSeatMap(context.Context, string, func(*seatmappb.Resolution) error) error
}

type DesktopApp struct {
	server    *webui.Server
	settings  desktopSettingsRepository
	egress    egressConfigurator
	hooks     hookConfigurator
	userID    string
	execution *desktopExecutionWorker

	contextMu      sync.RWMutex
	ctx            context.Context
	emitEvent      func(string, ...any)
	seatMapMu      sync.Mutex
	seatMapCancel  context.CancelFunc
	seatMapWatchID uint64
	settingsMu     sync.Mutex
	updateNeeded   atomic.Bool
	validateEgress func(context.Context, egress.Config) error
}

func newDesktopApp(
	server *webui.Server,
	settings desktopSettingsRepository,
	egressConfigurator egressConfigurator,
	hookConfig ...hookConfigurator,
) *DesktopApp {
	app := &DesktopApp{
		server:         server,
		settings:       settings,
		egress:         egressConfigurator,
		validateEgress: egress.ValidateConfig,
	}
	if len(hookConfig) > 0 {
		app.hooks = hookConfig[0]
	}
	return app
}

func (app *DesktopApp) startup(ctx context.Context) {
	app.contextMu.Lock()
	app.ctx = ctx
	app.emitEvent = func(name string, args ...any) { runtime.EventsEmit(ctx, name, args...) }
	app.contextMu.Unlock()
	if updates, ok := app.settings.(interface{ UpdateRequired() <-chan struct{} }); ok {
		go func() {
			select {
			case <-updates.UpdateRequired():
				app.updateNeeded.Store(true)
				runtime.Quit(ctx)
			case <-ctx.Done():
			}
		}()
	}
	if changes, ok := app.settings.(interface{ ResourceChanged() <-chan struct{} }); ok {
		go func() {
			for {
				select {
				case <-changes.ResourceChanged():
					app.emit("data:changed")
					if app.server != nil {
						app.server.NotifyExecutionEvent()
					}
				case <-ctx.Done():
					return
				}
			}
		}()
	}
	if err := app.applySavedHookSettings(); err != nil {
		app.server.RecordLocalSystemEvent(desktopErrorEvent(app.activeUserID(), "hook.invalid", "저장된 외부 알림 설정을 적용하지 못했습니다. 설정을 확인하세요."))
	}
	app.server.Start(ctx)
	if app.execution != nil {
		go func() {
			if err := app.execution.Run(ctx); err != nil {
				app.server.RecordLocalSystemEvent(desktopErrorEvent(
					app.activeUserID(), "execution.supervisor_failed", "예매 실행 연결을 복구하지 못했습니다. 앱을 다시 시작하세요.",
				))
				runtime.Quit(ctx)
			}
		}()
	}
	if err := app.applySavedNetworkSettings(); err != nil {
		_, _ = fmt.Fprintf(os.Stderr, "cineko: apply network settings: %v\n", err)
		app.server.RecordSystemEvent(desktopErrorEvent(app.activeUserID(), "network.invalid", "저장된 프록시 설정을 적용하지 못했습니다. 설정을 확인하세요."))
		return
	}
	go app.checkSavedNetworkHealth(ctx)
}

func (app *DesktopApp) GetUserID() (string, error) {
	if app.userID == "" {
		return "", errors.New("central user is unavailable")
	}
	return app.userID, nil
}

func (app *DesktopApp) setUserID(userID string) {
	app.userID = strings.TrimSpace(userID)
}

func (app *DesktopApp) activeUserID() string {
	return app.userID
}

func (app *DesktopApp) Exit() {
	app.StopSeatMapWatch()
	if appContext := app.context(); appContext != nil {
		runtime.Quit(appContext)
	}
}

// WatchSeatMap bridges Central's generated stream through Wails runtime
// events. Wails' virtual AssetServer response writer does not support the
// streaming semantics required by EventSource.
func (app *DesktopApp) WatchSeatMap(auditoriumID string) error {
	auditoriumID = strings.TrimSpace(auditoriumID)
	if auditoriumID == "" {
		return errors.New("auditorium ID is required")
	}
	watcher, supported := app.settings.(desktopSeatMapWatcher)
	if !supported {
		return errors.New("seat-map streaming is unavailable")
	}

	app.seatMapMu.Lock()
	if app.seatMapCancel != nil {
		app.seatMapCancel()
	}
	ctx, cancel := context.WithCancel(app.contextOrBackground())
	app.seatMapWatchID++
	watchID := app.seatMapWatchID
	app.seatMapCancel = cancel
	app.seatMapMu.Unlock()

	go func() {
		err := watcher.WatchSeatMap(ctx, auditoriumID, func(resolution *seatmappb.Resolution) error {
			response := servicepb.WatchSeatMapResponse_builder{Resolution: resolution}.Build()
			if err := protovalidate.Validate(response); err != nil {
				return fmt.Errorf("validate seat-map stream response: %w", err)
			}
			payload, err := (protojson.MarshalOptions{UseProtoNames: false}).Marshal(response)
			if err != nil {
				return fmt.Errorf("encode seat-map stream response: %w", err)
			}
			if !app.ownsSeatMapWatch(watchID) || ctx.Err() != nil {
				return context.Canceled
			}
			app.emit(desktopSeatMapEvent, string(payload))
			return nil
		})
		if err != nil && !errors.Is(err, context.Canceled) && app.ownsSeatMapWatch(watchID) {
			app.emit(desktopSeatMapErrorEvent)
		}
	}()
	return nil
}

func (app *DesktopApp) StopSeatMapWatch() {
	app.seatMapMu.Lock()
	app.seatMapWatchID++
	if app.seatMapCancel != nil {
		app.seatMapCancel()
		app.seatMapCancel = nil
	}
	app.seatMapMu.Unlock()
}

func (app *DesktopApp) ownsSeatMapWatch(watchID uint64) bool {
	app.seatMapMu.Lock()
	defer app.seatMapMu.Unlock()
	return app.seatMapWatchID == watchID && app.seatMapCancel != nil
}

func (app *DesktopApp) readSettings() (*clientpb.Settings, error) {
	app.settingsMu.Lock()
	defer app.settingsMu.Unlock()
	return app.readSettingsRemote()
}

func (app *DesktopApp) readSettingsRemote() (*clientpb.Settings, error) {
	if app.settings == nil {
		return nil, errors.New("central settings are unavailable")
	}
	settings := &clientpb.Settings{}
	if _, err := app.settings.GetSettings(app.contextOrBackground(), settings); err != nil {
		return nil, err
	}
	return settings, nil
}

func (app *DesktopApp) updateSettings(
	update func(*clientpb.Settings) error,
) error {
	app.settingsMu.Lock()
	defer app.settingsMu.Unlock()
	if app.settings == nil {
		return errors.New("central settings are unavailable")
	}
	for range 3 {
		settings := &clientpb.Settings{}
		revision, err := app.settings.GetSettings(app.contextOrBackground(), settings)
		if errors.Is(err, application.ErrNotFound) {
			settings = &clientpb.Settings{}
			revision = 0
		} else if err != nil {
			return err
		}
		if err := update(settings); err != nil {
			return err
		}
		if err := app.settings.PutSettings(app.contextOrBackground(), settings, revision); err == nil {
			return nil
		} else if !errors.Is(err, application.ErrConflict) {
			return err
		}
	}
	return application.ErrConflict
}

func (app *DesktopApp) context() context.Context {
	app.contextMu.RLock()
	defer app.contextMu.RUnlock()
	return app.ctx
}

func (app *DesktopApp) emit(name string, args ...any) {
	app.contextMu.RLock()
	emitter := app.emitEvent
	app.contextMu.RUnlock()
	if emitter != nil {
		emitter(name, args...)
	}
}
