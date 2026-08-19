package main

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/cineko-org/client/internal/adapters/configbundle"
	"github.com/cineko-org/client/internal/adapters/egress"
	"github.com/cineko-org/client/internal/adapters/eventhook"
	"github.com/cineko-org/client/internal/application"
)

type recordingEgressConfigurator struct {
	configs []egress.Config
	err     error
}

type recordingBundles struct {
	imported string
	report   configbundle.Report
	err      error
}

type recordingHookConfigurator struct {
	targets []eventhook.Target
	err     error
}

type memoryDesktopSettings struct {
	mu        sync.Mutex
	settings  *desktopSettings
	revision  int64
	getErr    error
	putErr    error
	conflicts int
	putCalls  int
}

func (repository *memoryDesktopSettings) GetSettings(_ context.Context, output any) (int64, error) {
	repository.mu.Lock()
	defer repository.mu.Unlock()
	if repository.getErr != nil {
		return 0, repository.getErr
	}
	if repository.settings == nil {
		return 0, application.ErrNotFound
	}
	data, _ := json.Marshal(repository.settings)
	return repository.revision, json.Unmarshal(data, output)
}

func (repository *memoryDesktopSettings) PutSettings(_ context.Context, input any, expectedRevision int64) error {
	repository.mu.Lock()
	defer repository.mu.Unlock()
	repository.putCalls++
	if repository.putErr != nil {
		return repository.putErr
	}
	if repository.conflicts > 0 {
		repository.conflicts--
		repository.revision++
		if repository.settings == nil {
			repository.settings = &desktopSettings{}
		}
		repository.settings.Hooks = []desktopHookSettings{{ID: "concurrent", Name: "Concurrent"}}
		return application.ErrConflict
	}
	if expectedRevision != repository.revision {
		return application.ErrConflict
	}
	data, _ := json.Marshal(input)
	var settings desktopSettings
	if err := json.Unmarshal(data, &settings); err != nil {
		return err
	}
	repository.settings = &settings
	repository.revision++
	return nil
}

func TestDesktopSettingsRetryConflictWithoutLosingConcurrentFields(t *testing.T) {
	clearNetworkEnvironment(t)
	repository := &memoryDesktopSettings{conflicts: 1}
	app := newDesktopApp(nil, nil, repository, &recordingEgressConfigurator{}, nil)
	skipEgressHealth(app)
	if _, err := app.SaveNetworkSettings(NetworkSettingsInput{SoxySessionTTL: "30m"}); err != nil {
		t.Fatal(err)
	}
	if repository.putCalls != 2 || repository.settings == nil || repository.settings.Network == nil ||
		len(repository.settings.Hooks) != 1 || repository.settings.Hooks[0].ID != "concurrent" {
		t.Fatalf("conflict retry lost Central settings: %+v after %d writes", repository.settings, repository.putCalls)
	}
}

func (configurator *recordingHookConfigurator) Configure(targets []eventhook.Target) error {
	if configurator.err != nil {
		return configurator.err
	}
	configurator.targets = append([]eventhook.Target(nil), targets...)
	return nil
}

func skipEgressHealth(app *DesktopApp) {
	app.validateEgress = func(context.Context, egress.Config) error { return nil }
}

func (bundles *recordingBundles) Export(context.Context, string, string) (configbundle.Report, error) {
	return bundles.report, bundles.err
}

func (bundles *recordingBundles) Import(_ context.Context, source string, userID string) (configbundle.Report, error) {
	bundles.imported = source
	if bundles.report.UserID != userID {
		return configbundle.Report{}, errors.New("backup user mismatch")
	}
	return bundles.report, bundles.err
}

func (configurator *recordingEgressConfigurator) ConfigureEgress(config egress.Config) error {
	if configurator.err != nil {
		return configurator.err
	}
	configurator.configs = append(configurator.configs, config)
	return nil
}

func TestDesktopNetworkSettingsSwitchBetweenDirectAndSoxy(t *testing.T) {
	clearNetworkEnvironment(t)
	settings := &memoryDesktopSettings{}
	configurator := &recordingEgressConfigurator{}
	app := newDesktopApp(nil, nil, settings, configurator, nil)
	skipEgressHealth(app)

	if _, err := app.SaveNetworkSettings(NetworkSettingsInput{
		SoxyURL: "https://soxy.example.test", SoxySessionTTL: "30m",
	}); err == nil {
		t.Fatal("SaveNetworkSettings() without token error = nil")
	}
	state, err := app.SaveNetworkSettings(NetworkSettingsInput{
		SoxyURL: " https://soxy.example.test ", SoxyAPIToken: " local-value ", SoxySessionTTL: "1h",
	})
	if err != nil {
		t.Fatalf("SaveNetworkSettings(Soxy) error = %v", err)
	}
	if state.Mode != "soxy" || state.SoxyURL != "https://soxy.example.test" ||
		state.SoxySessionTTL != "1h" || !state.HasAPIToken || len(configurator.configs) != 1 {
		t.Fatalf("Soxy state = %+v, configurations = %+v", state, configurator.configs)
	}

	state, err = app.SaveNetworkSettings(NetworkSettingsInput{
		SoxyURL: "https://soxy.example.test", SoxySessionTTL: "1h",
	})
	if err != nil || !state.HasAPIToken || len(configurator.configs) != 2 {
		t.Fatalf("preserve token state = %+v, error = %v", state, err)
	}

	state, err = app.SaveNetworkSettings(NetworkSettingsInput{SoxySessionTTL: "30m"})
	if err != nil {
		t.Fatalf("SaveNetworkSettings(Direct) error = %v", err)
	}
	if state.Mode != "direct" || state.HasAPIToken || configurator.configs[2].SoxyURL != "" {
		t.Fatalf("Direct state = %+v, configuration = %+v", state, configurator.configs[2])
	}

	if settings.settings == nil || settings.settings.Network == nil || settings.settings.Network.SoxyAPIToken != "" {
		t.Fatalf("disabled Soxy credential remained in Central settings: %+v", settings.settings)
	}
}

func TestDesktopStandardProxyValidationPrecedesPersistence(t *testing.T) {
	clearNetworkEnvironment(t)
	settings := &memoryDesktopSettings{}
	configurator := &recordingEgressConfigurator{}
	app := newDesktopApp(nil, nil, settings, configurator, nil)
	validationErr := errors.New("proxy offline")
	app.validateEgress = func(_ context.Context, config egress.Config) error {
		if len(config.Proxies) != 2 || config.Proxies[0].Username != "user" || config.Proxies[0].Password != "password" {
			t.Fatalf("validation config = %+v", config)
		}
		return validationErr
	}
	input := NetworkSettingsInput{
		Mode: "proxy", ProxyURLs: []string{" socks5://127.0.0.1:1080 ", "https://127.0.0.1:8443"},
		ProxyUsername: " user ", ProxyPassword: "password", SoxySessionTTL: "30m",
	}
	if _, err := app.SaveNetworkSettings(input); !errors.Is(err, validationErr) {
		t.Fatalf("SaveNetworkSettings(invalid) error = %v", err)
	}
	if settings.settings != nil {
		t.Fatalf("invalid settings reached Central: %+v", settings.settings)
	}
	app.validateEgress = func(context.Context, egress.Config) error { return nil }
	state, err := app.SaveNetworkSettings(input)
	if err != nil || state.Mode != "proxy" || len(state.ProxyURLs) != 2 || !state.HasProxyPassword {
		t.Fatalf("state = %+v, error = %v", state, err)
	}
	state, err = app.SaveNetworkSettings(NetworkSettingsInput{
		Mode: "proxy", ProxyURLs: input.ProxyURLs, ProxyUsername: "user", SoxySessionTTL: "30m",
	})
	if err != nil || !state.HasProxyPassword {
		t.Fatalf("preserved password state = %+v, error = %v", state, err)
	}
}

func TestDesktopHookSettingsPreserveSecretsAndRejectInvalidConfiguration(t *testing.T) {
	settings := &memoryDesktopSettings{}
	hooks := &recordingHookConfigurator{}
	app := newDesktopApp(nil, nil, settings, &recordingEgressConfigurator{}, nil, hooks)
	input := HookSettingsInput{Targets: []HookTargetInput{{
		ID: "discord", Name: "Discord", Kind: eventhook.KindDiscord,
		URL: "https://discord.com/api/webhooks/1/token", Secret: "secret", Enabled: true,
	}}}
	state, err := app.SaveHookSettings(input)
	if err != nil || len(state.Targets) != 1 || !state.Targets[0].HasSecret || len(hooks.targets) != 1 {
		t.Fatalf("state = %+v, targets = %+v, error = %v", state, hooks.targets, err)
	}
	input.Targets[0].Secret = ""
	state, err = app.SaveHookSettings(input)
	if err != nil || !state.Targets[0].HasSecret || hooks.targets[0].Secret != "secret" {
		t.Fatalf("preserved state = %+v, targets = %+v, error = %v", state, hooks.targets, err)
	}
	hooks.err = errors.New("invalid hook")
	input.Targets[0].Name = "Changed"
	if _, err := app.SaveHookSettings(input); !errors.Is(err, hooks.err) {
		t.Fatalf("SaveHookSettings(invalid) error = %v", err)
	}
	hooks.err = nil
	reloaded, err := app.GetHookSettings()
	if err != nil || reloaded.Targets[0].Name != "Discord" {
		t.Fatalf("reloaded = %+v, error = %v", reloaded, err)
	}
}

func TestDesktopNetworkSettingsUseEnvironmentTokenWithoutPersistingIt(t *testing.T) {
	clearNetworkEnvironment(t)
	t.Setenv("CINEKO_SOXY_URL", "https://environment.example.test")
	t.Setenv("CINEKO_SOXY_API_TOKEN", "environment-value")
	settings := &memoryDesktopSettings{}
	configurator := &recordingEgressConfigurator{}
	app := newDesktopApp(nil, nil, settings, configurator, nil)
	skipEgressHealth(app)

	state, err := app.SaveNetworkSettings(NetworkSettingsInput{
		SoxyURL: "https://app.example.test", SoxySessionTTL: "30m",
	})
	if err != nil {
		t.Fatalf("SaveNetworkSettings() error = %v", err)
	}
	if state.Mode != "soxy" || !state.HasAPIToken || configurator.configs[0].SoxyToken != "environment-value" {
		t.Fatalf("state = %+v, configuration = %+v", state, configurator.configs[0])
	}
	if settings.settings.Network.SoxyAPIToken != "" {
		t.Fatal("environment token was persisted")
	}

	reloaded := newDesktopApp(nil, nil, settings, &recordingEgressConfigurator{}, nil)
	reloadedState, err := reloaded.GetNetworkSettings()
	if err != nil || reloadedState.Mode != "soxy" || !reloadedState.HasAPIToken || reloadedState.Source != "settings" {
		t.Fatalf("reloaded state = %+v, error = %v", reloadedState, err)
	}
}

func TestDesktopSettingsBelongToAuthenticatedCentralUser(t *testing.T) {
	clearNetworkEnvironment(t)
	settings := &memoryDesktopSettings{}
	app := newDesktopApp(nil, nil, settings, &recordingEgressConfigurator{}, nil)
	app.setUserID("user-1")
	skipEgressHealth(app)
	if _, err := app.SaveNetworkSettings(NetworkSettingsInput{
		SoxyURL: "https://soxy.example.test", SoxyAPIToken: "local-value", SoxySessionTTL: "30m",
	}); err != nil {
		t.Fatal(err)
	}
	stored, err := app.readSettings()
	if err != nil {
		t.Fatal(err)
	}
	if stored.Network == nil || stored.Network.SoxyAPIToken != "local-value" {
		t.Fatalf("settings = %+v", stored)
	}
	userID, err := app.GetUserID()
	if err != nil || userID != "user-1" {
		t.Fatalf("GetUserID() = %q, %v", userID, err)
	}
}

func TestApplySavedNetworkSettingsAndValidationFailures(t *testing.T) {
	clearNetworkEnvironment(t)
	settings := desktopSettings{Network: &desktopNetworkSettings{
		SoxyURL: "https://soxy.example.test", SoxyAPIToken: "local-value", SoxySessionTTL: "30m",
	}}
	repository := &memoryDesktopSettings{settings: &settings}
	configurator := &recordingEgressConfigurator{}
	app := newDesktopApp(nil, nil, repository, configurator, nil)
	if err := app.applySavedNetworkSettings(); err != nil {
		t.Fatalf("applySavedNetworkSettings() error = %v", err)
	}
	if len(configurator.configs) != 1 || configurator.configs[0].SessionTTL != 30*time.Minute {
		t.Fatalf("configurations = %+v", configurator.configs)
	}

	settings.Network.SoxySessionTTL = "later"
	repository.settings = &settings
	if err := app.applySavedNetworkSettings(); err == nil {
		t.Fatal("applySavedNetworkSettings(invalid TTL) error = nil")
	}
	configurator.err = errors.New("closed")
	settings.Network.SoxySessionTTL = "30m"
	repository.settings = &settings
	if err := app.applySavedNetworkSettings(); !errors.Is(err, configurator.err) {
		t.Fatalf("applySavedNetworkSettings(configurator failure) error = %v", err)
	}
}

func TestGetNetworkSettingsDefaultsToEnvironmentOrDirect(t *testing.T) {
	clearNetworkEnvironment(t)
	app := newDesktopApp(nil, nil, &memoryDesktopSettings{}, &recordingEgressConfigurator{}, nil)
	state, err := app.GetNetworkSettings()
	if err != nil || state.Mode != "direct" || state.Source != "environment" || state.SoxySessionTTL != "30m" {
		t.Fatalf("direct state = %+v, error = %v", state, err)
	}
	t.Setenv("CINEKO_SOXY_URL", "https://soxy.example.test")
	t.Setenv("CINEKO_SOXY_API_TOKEN", "environment-value")
	state, err = app.GetNetworkSettings()
	if err != nil || state.Mode != "soxy" || !state.HasAPIToken {
		t.Fatalf("environment state = %+v, error = %v", state, err)
	}
}

func TestDesktopUserAndInitialBundleDefaults(t *testing.T) {
	app := newDesktopApp(nil, nil, &memoryDesktopSettings{}, &recordingEgressConfigurator{}, []string{
		"--ignored", "/tmp/backup.CNK", "/tmp/later.cnk",
	})
	app.setUserID("user-1")
	userID, err := app.GetUserID()
	if err != nil || userID != "user-1" {
		t.Fatalf("GetUserID() = %q, %v", userID, err)
	}
	if path := app.initialBundlePath(); path != "/tmp/backup.CNK" {
		t.Fatalf("initialBundlePath() = %q", path)
	}
}

func TestImportConfigurationDoesNotChangeAuthenticatedUserAndEmitsReload(t *testing.T) {
	source := filepath.Join(t.TempDir(), "backup.cnk")
	bundles := &recordingBundles{report: configbundle.Report{
		Path: source, UserID: "current-user", Presets: 2, Monitors: 3,
	}}
	app := newDesktopApp(nil, bundles, &memoryDesktopSettings{}, &recordingEgressConfigurator{}, nil)
	app.setUserID("current-user")
	app.ctx = context.Background()
	var eventName string
	var eventData []any
	app.emitEvent = func(name string, data ...any) {
		eventName, eventData = name, data
	}

	report, err := app.importConfiguration(source)
	if err != nil {
		t.Fatalf("importConfiguration() error = %v", err)
	}
	if report.UserID != "current-user" || bundles.imported != source {
		t.Fatalf("report = %+v, imported = %q", report, bundles.imported)
	}
	userID, err := app.GetUserID()
	if err != nil || userID != "current-user" {
		t.Fatalf("GetUserID() = %q, %v", userID, err)
	}
	if eventName != "data:changed" || len(eventData) != 1 {
		t.Fatalf("event = %q, data = %#v", eventName, eventData)
	}
}

func clearNetworkEnvironment(t *testing.T) {
	t.Helper()
	for _, key := range []string{
		"CINEKO_SOXY_URL", "CINEKO_SOXY_API_TOKEN", "CINEKO_SOXY_SESSION_TTL", "CINEKO_SCAN_PROXIES",
	} {
		t.Setenv(key, "")
	}
}
