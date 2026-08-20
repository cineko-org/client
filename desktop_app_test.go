package main

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"testing"

	"github.com/cineko-org/client/internal/adapters/egress"
	"github.com/cineko-org/client/internal/adapters/eventhook"
	"github.com/cineko-org/client/internal/application"
)

type recordingEgressConfigurator struct {
	configs []egress.Config
	err     error
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
	app := newDesktopApp(nil, repository, &recordingEgressConfigurator{})
	skipEgressHealth(app)
	if _, err := app.SaveNetworkSettings(NetworkSettingsInput{Mode: "direct"}); err != nil {
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

func (configurator *recordingEgressConfigurator) ConfigureEgress(config egress.Config) error {
	if configurator.err != nil {
		return configurator.err
	}
	configurator.configs = append(configurator.configs, config)
	return nil
}

func TestDesktopNetworkSettingsRejectsClientManagedSoxy(t *testing.T) {
	clearNetworkEnvironment(t)
	settings := &memoryDesktopSettings{}
	configurator := &recordingEgressConfigurator{}
	app := newDesktopApp(nil, settings, configurator)
	skipEgressHealth(app)

	if _, err := app.SaveNetworkSettings(NetworkSettingsInput{
		Mode: "soxy", SoxyURL: "https://soxy.example.test", SoxyAPIToken: "local-value",
	}); err == nil {
		t.Fatal("SaveNetworkSettings(Soxy) error = nil")
	}
	state, err := app.SaveNetworkSettings(NetworkSettingsInput{Mode: "direct"})
	if err != nil {
		t.Fatalf("SaveNetworkSettings(Direct) error = %v", err)
	}
	if state.Mode != "direct" || len(configurator.configs) != 1 || len(configurator.configs[0].Proxies) != 0 {
		t.Fatalf("Direct state = %+v, configurations = %+v", state, configurator.configs)
	}
	if settings.settings == nil || settings.settings.Network == nil || settings.settings.Network.SoxyAPIToken != "" {
		t.Fatalf("client settings retained a Soxy credential: %+v", settings.settings)
	}
}

func TestDesktopStandardProxyValidationPrecedesPersistence(t *testing.T) {
	clearNetworkEnvironment(t)
	settings := &memoryDesktopSettings{}
	configurator := &recordingEgressConfigurator{}
	app := newDesktopApp(nil, settings, configurator)
	validationErr := errors.New("proxy offline")
	app.validateEgress = func(_ context.Context, config egress.Config) error {
		if len(config.Proxies) != 2 || config.Proxies[0].Username != "user" || config.Proxies[0].Password != "password" {
			t.Fatalf("validation config = %+v", config)
		}
		return validationErr
	}
	input := NetworkSettingsInput{
		Mode: "proxy", ProxyURLs: []string{" socks5://127.0.0.1:1080 ", "https://127.0.0.1:8443"},
		ProxyUsername: " user ", ProxyPassword: "password",
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
		Mode: "proxy", ProxyURLs: input.ProxyURLs, ProxyUsername: "user",
	})
	if err != nil || !state.HasProxyPassword {
		t.Fatalf("preserved password state = %+v, error = %v", state, err)
	}
}

func TestDesktopHookSettingsPreserveSecretsAndRejectInvalidConfiguration(t *testing.T) {
	settings := &memoryDesktopSettings{}
	hooks := &recordingHookConfigurator{}
	app := newDesktopApp(nil, settings, &recordingEgressConfigurator{}, hooks)
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

func TestDesktopNetworkSettingsIgnoreSoxyEnvironment(t *testing.T) {
	clearNetworkEnvironment(t)
	t.Setenv("CINEKO_SOXY_URL", "https://environment.example.test")
	t.Setenv("CINEKO_SOXY_API_TOKEN", "environment-value")
	settings := &memoryDesktopSettings{}
	configurator := &recordingEgressConfigurator{}
	app := newDesktopApp(nil, settings, configurator)
	skipEgressHealth(app)

	state, err := app.SaveNetworkSettings(NetworkSettingsInput{Mode: "direct"})
	if err != nil {
		t.Fatalf("SaveNetworkSettings() error = %v", err)
	}
	if state.Mode != "direct" || len(configurator.configs[0].Proxies) != 0 {
		t.Fatalf("state = %+v, configuration = %+v", state, configurator.configs[0])
	}
	reloaded := newDesktopApp(nil, settings, &recordingEgressConfigurator{})
	reloadedState, err := reloaded.GetNetworkSettings()
	if err != nil || reloadedState.Mode != "direct" || reloadedState.Source != "settings" {
		t.Fatalf("reloaded state = %+v, error = %v", reloadedState, err)
	}
}

func TestDesktopSettingsBelongToAuthenticatedCentralUser(t *testing.T) {
	clearNetworkEnvironment(t)
	settings := &memoryDesktopSettings{}
	app := newDesktopApp(nil, settings, &recordingEgressConfigurator{})
	app.setUserID("user-1")
	skipEgressHealth(app)
	if _, err := app.SaveNetworkSettings(NetworkSettingsInput{
		Mode: "proxy", ProxyURLs: []string{"http://127.0.0.1:8080"},
	}); err != nil {
		t.Fatal(err)
	}
	stored, err := app.readSettings()
	if err != nil {
		t.Fatal(err)
	}
	if stored.Network == nil || stored.Network.Mode != "proxy" || len(stored.Network.ProxyURLs) != 1 {
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
	app := newDesktopApp(nil, repository, configurator)
	if err := app.applySavedNetworkSettings(); err != nil {
		t.Fatalf("applySavedNetworkSettings() error = %v", err)
	}
	if len(configurator.configs) != 1 || len(configurator.configs[0].Proxies) != 0 {
		t.Fatalf("configurations = %+v", configurator.configs)
	}

	settings.Network.Mode = "unsupported"
	repository.settings = &settings
	if err := app.applySavedNetworkSettings(); err == nil {
		t.Fatal("applySavedNetworkSettings(invalid mode) error = nil")
	}
	configurator.err = errors.New("closed")
	settings.Network.Mode = "direct"
	repository.settings = &settings
	if err := app.applySavedNetworkSettings(); !errors.Is(err, configurator.err) {
		t.Fatalf("applySavedNetworkSettings(configurator failure) error = %v", err)
	}
}

func TestGetNetworkSettingsDefaultsToEnvironmentOrDirect(t *testing.T) {
	clearNetworkEnvironment(t)
	app := newDesktopApp(nil, &memoryDesktopSettings{}, &recordingEgressConfigurator{})
	state, err := app.GetNetworkSettings()
	if err != nil || state.Mode != "direct" || state.Source != "environment" {
		t.Fatalf("direct state = %+v, error = %v", state, err)
	}
	t.Setenv("CINEKO_SOXY_URL", "https://soxy.example.test")
	t.Setenv("CINEKO_SOXY_API_TOKEN", "environment-value")
	state, err = app.GetNetworkSettings()
	if err != nil || state.Mode != "direct" {
		t.Fatalf("environment state = %+v, error = %v", state, err)
	}
}

func TestDesktopUserIdentity(t *testing.T) {
	app := newDesktopApp(nil, &memoryDesktopSettings{}, &recordingEgressConfigurator{})
	app.setUserID("user-1")
	userID, err := app.GetUserID()
	if err != nil || userID != "user-1" {
		t.Fatalf("GetUserID() = %q, %v", userID, err)
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
