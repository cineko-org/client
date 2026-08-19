package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/cineko-org/client/internal/adapters/configbundle"
	"github.com/cineko-org/client/internal/adapters/egress"
	"github.com/cineko-org/client/internal/application"
	"github.com/cineko-org/client/internal/domain"
	"github.com/cineko-org/client/internal/interfaces/webui"

	"github.com/wailsapp/wails/v2/pkg/options"
	"github.com/wailsapp/wails/v2/pkg/runtime"
)

type desktopSettings struct {
	Network *desktopNetworkSettings `json:"network,omitempty"`
	Hooks   []desktopHookSettings   `json:"hooks,omitempty"`
}

type desktopSettingsRepository interface {
	GetSettings(context.Context, any) (int64, error)
	PutSettings(context.Context, any, int64) error
}

type configurationBundles interface {
	Export(context.Context, string, string) (configbundle.Report, error)
	Import(context.Context, string, string) (configbundle.Report, error)
}

type DesktopApp struct {
	server       *webui.Server
	bundles      configurationBundles
	settings     desktopSettingsRepository
	egress       egressConfigurator
	hooks        hookConfigurator
	initialFiles []string
	userID       string
	execution    *desktopExecutionWorker

	contextMu      sync.RWMutex
	ctx            context.Context
	emitEvent      func(string, ...any)
	settingsMu     sync.Mutex
	updateNeeded   atomic.Bool
	validateEgress func(context.Context, egress.Config) error
}

func newDesktopApp(
	server *webui.Server,
	bundles configurationBundles,
	settings desktopSettingsRepository,
	egressConfigurator egressConfigurator,
	initialFiles []string,
	hookConfig ...hookConfigurator,
) *DesktopApp {
	app := &DesktopApp{
		server:         server,
		bundles:        bundles,
		settings:       settings,
		egress:         egressConfigurator,
		initialFiles:   append([]string(nil), initialFiles...),
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
	app.emitEvent = func(name string, data ...any) { runtime.EventsEmit(ctx, name, data...) }
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
				case <-ctx.Done():
					return
				}
			}
		}()
	}
	if err := app.applySavedHookSettings(); err != nil {
		app.server.RecordLocalSystemEvent(app.activeUserID(), "hook.invalid", domain.EventError, "저장된 알림 훅 설정을 적용하지 못했습니다: "+err.Error())
	}
	app.server.Start(ctx)
	if app.execution != nil {
		go app.execution.Run(ctx)
	}
	if err := app.applySavedNetworkSettings(); err != nil {
		_, _ = fmt.Fprintf(os.Stderr, "cineko: apply network settings: %v\n", err)
		app.server.RecordSystemEvent(app.activeUserID(), "network.invalid", domain.EventError, "저장된 프록시 설정을 적용하지 못했습니다: "+err.Error())
		return
	}
	go app.checkSavedNetworkHealth(ctx)
}

func (app *DesktopApp) domReady(context.Context) {
	source := app.initialBundlePath()
	if source == "" {
		return
	}
	if _, err := app.importConfiguration(source); err != nil {
		app.emitTransferError(err)
	}
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

func (app *DesktopApp) ExportConfiguration(userID string) (configbundle.Report, error) {
	userID = strings.TrimSpace(userID)
	if userID == "" {
		return configbundle.Report{}, errors.New("사용자 ID가 필요합니다")
	}
	ctx := app.context()
	if ctx == nil {
		return configbundle.Report{}, errors.New("앱이 아직 준비되지 않았습니다")
	}
	if app.hasActiveTasks() {
		return configbundle.Report{}, errors.New("실행 중인 작업을 중지한 뒤 내보내세요")
	}
	target, err := runtime.SaveFileDialog(ctx, runtime.SaveDialogOptions{
		Title:                "Cineko 데이터 내보내기",
		DefaultFilename:      "cineko-backup.cnk",
		CanCreateDirectories: true,
		Filters: []runtime.FileFilter{{
			DisplayName: "Cineko 백업 (*.cnk)",
			Pattern:     "*.cnk",
		}},
	})
	if err != nil || target == "" {
		return configbundle.Report{}, err
	}
	return app.bundles.Export(ctx, userID, target)
}

func (app *DesktopApp) ImportConfiguration() (configbundle.Report, error) {
	ctx := app.context()
	if ctx == nil {
		return configbundle.Report{}, errors.New("앱이 아직 준비되지 않았습니다")
	}
	source, err := runtime.OpenFileDialog(ctx, runtime.OpenDialogOptions{
		Title: "Cineko 데이터 가져오기",
		Filters: []runtime.FileFilter{{
			DisplayName: "Cineko 백업 (*.cnk)",
			Pattern:     "*.cnk",
		}},
	})
	if err != nil || source == "" {
		return configbundle.Report{}, err
	}
	return app.importConfiguration(source)
}

func (app *DesktopApp) importConfiguration(source string) (configbundle.Report, error) {
	ctx := app.context()
	if ctx == nil {
		return configbundle.Report{}, errors.New("앱이 아직 준비되지 않았습니다")
	}
	if app.hasActiveTasks() {
		return configbundle.Report{}, errors.New("실행 중인 작업을 중지한 뒤 가져오세요")
	}
	absolutePath, err := filepath.Abs(source)
	if err != nil {
		return configbundle.Report{}, err
	}
	report, err := app.bundles.Import(ctx, absolutePath, app.activeUserID())
	if err != nil {
		return configbundle.Report{}, err
	}
	app.emit("data:changed", report)
	return report, nil
}

func (app *DesktopApp) openFile(source string) {
	if !strings.EqualFold(filepath.Ext(source), ".cnk") {
		return
	}
	if _, err := app.importConfiguration(source); err != nil {
		app.emitTransferError(err)
	}
}

func (app *DesktopApp) secondInstance(data options.SecondInstanceData) {
	for _, argument := range data.Args {
		if strings.EqualFold(filepath.Ext(argument), ".cnk") {
			app.openFile(argument)
			break
		}
	}
}

func (app *DesktopApp) emitTransferError(err error) {
	app.emit("transfer:error", err.Error())
}

func (app *DesktopApp) Exit() {
	if appContext := app.context(); appContext != nil {
		runtime.Quit(appContext)
	}
}

func (app *DesktopApp) initialBundlePath() string {
	for _, name := range app.initialFiles {
		if strings.EqualFold(filepath.Ext(name), ".cnk") {
			return name
		}
	}
	return ""
}

func (app *DesktopApp) readSettings() (desktopSettings, error) {
	app.settingsMu.Lock()
	defer app.settingsMu.Unlock()
	return app.readSettingsRemote()
}

func (app *DesktopApp) readSettingsRemote() (desktopSettings, error) {
	if app.settings == nil {
		return desktopSettings{}, errors.New("central settings are unavailable")
	}
	var settings desktopSettings
	if _, err := app.settings.GetSettings(app.contextOrBackground(), &settings); err != nil {
		return desktopSettings{}, err
	}
	return settings, nil
}

func (app *DesktopApp) updateSettings(
	update func(*desktopSettings) error,
) error {
	app.settingsMu.Lock()
	defer app.settingsMu.Unlock()
	if app.settings == nil {
		return errors.New("central settings are unavailable")
	}
	for range 3 {
		var settings desktopSettings
		revision, err := app.settings.GetSettings(app.contextOrBackground(), &settings)
		if errors.Is(err, application.ErrNotFound) {
			settings = desktopSettings{}
			revision = 0
		} else if err != nil {
			return err
		}
		if err := update(&settings); err != nil {
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

func (app *DesktopApp) emit(name string, data ...any) {
	app.contextMu.RLock()
	emitter := app.emitEvent
	app.contextMu.RUnlock()
	if emitter != nil {
		emitter(name, data...)
	}
}

func (app *DesktopApp) hasActiveTasks() bool {
	return app.server != nil && app.server.HasActiveTasks()
}
