package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime/debug"
	"strings"
	"time"

	"github.com/cineko-org/client/internal/adapters/browserfactory"
	"github.com/cineko-org/client/internal/adapters/cgv"
	"github.com/cineko-org/client/internal/adapters/egress"
	"github.com/cineko-org/client/internal/adapters/eventhook"
	localstore "github.com/cineko-org/client/internal/adapters/storage/local"
	"github.com/cineko-org/client/internal/booking"
	"github.com/cineko-org/client/internal/interfaces/webui"
	"github.com/cineko-org/client/internal/logging"
	"github.com/cineko-org/client/internal/platform"
	clientpb "github.com/cineko-org/contracts/v3/gen/go/cineko/client"
	"github.com/cineko-org/probe/v2/networkcapture"

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
	if err := configureDesktopRuntimePaths(dataDir); err != nil {
		return err
	}
	debugMode := desktopDebugMode()
	restoreDebug := logging.SetDebug(debugMode)
	defer restoreDebug()
	logJournal, closeLog, err := logging.OpenPersistentJournal(dataDir)
	if err != nil {
		return err
	}
	defer finishDesktopRun(&runErr, closeLog)
	logging.Info(context.Background(), "Client startup", "event", "client.startup", "data_dir", dataDir, "version", desktopVersion, "debug", debugMode)
	networkCapture, err := networkcapture.NewStore(filepath.Join(dataDir, "artifacts", "network"), logging.Logger(), networkcapture.WithDebug(debugMode))
	if err != nil {
		return err
	}
	stopNetworkSummary := logging.StartNetworkSummary(networkCapture, 5*time.Minute)
	defer stopNetworkSummary()
	restoreNetworkCapture := logging.SetNetworkCapture(networkCapture)
	defer restoreNetworkCapture()
	store, launchContext, startupReadyNonce, err := openDesktopStore(context.Background(), dataDir, os.Stdin)
	if err != nil {
		return err
	}
	defer func() { runErr = errors.Join(runErr, store.Close()) }()
	if err := prepareDesktopState(context.Background(), store, launchContext, dataDir); err != nil {
		return err
	}
	browsers, err := browserfactory.NewFromEnvironment(dataDir, networkCapture)
	if err != nil {
		return err
	}
	defer browsers.Close()
	warmPool, err := newWarmBookingPool(context.Background(), browsers, store.UserID())
	if err != nil {
		return err
	}
	defer func() { runErr = errors.Join(runErr, warmPool.Close()) }()
	scheduleChanged := make(chan struct{}, 1)
	monitoringSummary := newMonitoringSummary(time.Now())
	stopMonitoringSummary := monitoringSummary.start(5 * time.Minute)
	defer stopMonitoringSummary()
	embeddedProbe, err := startEmbeddedProbe(context.Background(), store, dataDir, scheduleChanged, networkCapture, monitoringSummary)
	if err != nil {
		return err
	}
	defer func() { runErr = errors.Join(runErr, embeddedProbe.Close()) }()
	bookingHost := newBookingAutomationHost(warmPool, embeddedProbe)
	defer bookingHost.Close()
	hooks := eventhook.New(nil)
	defer hooks.Close()

	server, err := webui.New(webui.Dependencies{
		ScannerStatus: embeddedProbe.ScanStatus,
		Repository:    store,
		Factory:       newAutomationFactory(browsers, bookingHost, embeddedProbe, store.UserID()),
		IDs:           platform.IDGenerator{}, Clock: platform.Clock{}, Waiter: platform.Waiter{}, Events: hooks,
		UserID: store.UserID(), PosterCacheDir: filepath.Join(dataDir, "posters"),
		LogPath:           filepath.Join(dataDir, "client.log"),
		NetworkCaptureDir: networkCapture.Root(),
		NetworkStatistics: networkCapture.SessionStatistics,
		ClearLogs: func(context.Context) error {
			if err := networkCapture.Clear(); err != nil {
				return err
			}
			return logJournal.Clear()
		},
		BookingDemandChanged: func(active bool) {
			bookingHost.SetDemand(active)
		},
		BookingCapacityAvailable: bookingHost.CanAccept,
		MonitoringChanged:        embeddedProbe.CancelMonitorScan,
		MonitoringBlockReason: func() string {
			return providerMonitoringBlockReason(networkCapture)
		},
	})
	if err != nil {
		return err
	}
	warmPool.SetReadyNotifier(server.NotifyBookingCapacityChanged)
	warmPool.SetStartupFailureNotifier(func(err error) {
		server.NotifyBookingPreparationFailed(err, errors.Is(err, cgv.ErrAuthenticationRequired) || errors.Is(err, cgv.ErrAuthenticationUnverified))
	})
	hooks.SetFailureHandler(func(failure eventhook.Failure) {
		server.RecordLocalSystemEvent(desktopErrorEvent(
			store.UserID(), "hook.delivery_failed",
			fmt.Sprintf("%s 알림을 보내지 못했습니다. 외부 알림 설정을 확인하세요.", failure.Target.GetName()),
		))
	})
	app := newDesktopApp(server, store, browsers, hooks)
	app.scanner = embeddedProbe.scanner
	app.setUserID(store.UserID())
	app.monitor = &desktopMonitorWorker{store: store, server: server, scheduleChanged: scheduleChanged, summary: monitoringSummary}
	err = runDesktopWindow(app, server, store, embeddedProbe, dataDir, startupReadyNonce)
	if app.updateNeeded.Load() {
		return errors.Join(err, errUpdateRequired)
	}
	return err
}

func providerMonitoringBlockReason(capture *networkcapture.Store) string {
	if blocked, decision := capture.RateLimit().Blocked("cgv.co.kr"); blocked {
		if time.Until(decision.BlockedUntil) > 0 {
			return fmt.Sprintf("CGV 요청 제한(429)으로 조회를 쉬고 있습니다. 재시도 가능 시각: %s", decision.BlockedUntil.Local().Format("01/02 15:04:05"))
		}
		return "CGV 요청 제한(429) 복구를 확인하고 있습니다. 추가 조회는 대기합니다."
	}
	return ""
}

func finishDesktopRun(runErr *error, closeLog func() error) {
	panicked := recover()
	if panicked != nil {
		*runErr = errors.Join(*runErr, fmt.Errorf("client panic: %v\n%s", panicked, debug.Stack()))
	}
	*runErr = logging.FinishPersistentRun(context.Background(), *runErr, closeLog)
	if panicked != nil {
		panic(panicked)
	}
}

func desktopDebugMode() bool {
	switch strings.ToLower(strings.TrimSpace(os.Getenv("CINEKO_DEBUG"))) {
	case "1", "true", "yes", "on":
		return true
	default:
		return false
	}
}

func prepareDesktopState(
	ctx context.Context,
	store *localstore.Store,
	launchContext *clientpb.LaunchContext,
	dataDir string,
) error {
	if launchContext == nil || launchContext.GetInstallationId() == "" {
		return errors.New("desktop launch context is required")
	}
	_ = ctx
	_ = store
	_ = dataDir
	return nil
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
				if errors.Is(err, cgv.ErrAuthenticationUnverified) {
					return errors.Join(booking.ErrPermanent, err)
				}
				return err
			}
			if !authenticated {
				return errors.Join(booking.ErrPermanent, cgv.ErrAuthenticationRequired)
			}
			return nil
		},
	)
}

func newAutomationFactory(
	factory *browserfactory.Factory,
	bookingHost *bookingAutomationHost,
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
		return bookingHost.Open(ctx)
	}
}

func desktopDataDir() (string, error) {
	return resolveDesktopDataDir(os.Getenv("CINEKO_DATA_DIR"), os.UserHomeDir)
}

func resolveDesktopDataDir(configured string, homeDir func() (string, error)) (string, error) {
	if value := strings.TrimSpace(configured); value != "" {
		if !filepath.IsAbs(value) {
			return "", errors.New("CINEKO_DATA_DIR must be an absolute path")
		}
		return filepath.Clean(value), nil
	}
	root, err := homeDir()
	if err != nil {
		return "", fmt.Errorf("find Cineko home directory: %w", err)
	}
	return filepath.Join(root, "cineko"), nil
}

func configureDesktopRuntimePaths(dataDir string) error {
	paths := []string{
		filepath.Join(dataDir, "runtime", "playwright", "driver"),
		filepath.Join(dataDir, "runtime", "playwright", "browsers"),
		filepath.Join(dataDir, "tmp"),
	}
	for _, path := range paths {
		if err := os.MkdirAll(path, 0o700); err != nil {
			return fmt.Errorf("create Cineko runtime directory %s: %w", path, err)
		}
	}
	if strings.TrimSpace(os.Getenv("CINEKO_PLAYWRIGHT_DRIVER_PATH")) == "" &&
		strings.TrimSpace(os.Getenv("PLAYWRIGHT_DRIVER_PATH")) == "" {
		if err := os.Setenv("PLAYWRIGHT_DRIVER_PATH", paths[0]); err != nil {
			return fmt.Errorf("configure Playwright driver directory: %w", err)
		}
	}
	if strings.TrimSpace(os.Getenv("PLAYWRIGHT_BROWSERS_PATH")) == "" {
		if err := os.Setenv("PLAYWRIGHT_BROWSERS_PATH", paths[1]); err != nil {
			return fmt.Errorf("configure Playwright browser directory: %w", err)
		}
	}
	for _, name := range []string{"TMPDIR", "TMP", "TEMP"} {
		if err := os.Setenv(name, paths[2]); err != nil {
			return fmt.Errorf("configure Cineko temporary directory: %w", err)
		}
	}
	return nil
}
