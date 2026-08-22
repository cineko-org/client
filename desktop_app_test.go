package main

import (
	"context"
	"errors"
	"sync"
	"testing"

	"github.com/cineko-org/client/internal/adapters/egress"
	"github.com/cineko-org/client/internal/application"
	clientpb "github.com/cineko-org/contracts/gen/go/cineko/client"
	"google.golang.org/protobuf/proto"
)

type recordingEgressConfigurator struct {
	configs []egress.Config
	err     error
}

type recordingHookConfigurator struct {
	targets []*clientpb.WebhookTarget
	err     error
}

type memoryDesktopSettings struct {
	mu        sync.Mutex
	settings  *clientpb.Settings
	revision  int64
	getErr    error
	putErr    error
	conflicts int
	putCalls  int
}

func (repository *memoryDesktopSettings) GetSettings(_ context.Context, output *clientpb.Settings) (int64, error) {
	repository.mu.Lock()
	defer repository.mu.Unlock()
	if repository.getErr != nil {
		return 0, repository.getErr
	}
	if repository.settings == nil {
		return 0, application.ErrNotFound
	}
	proto.Reset(output)
	proto.Merge(output, repository.settings)
	return repository.revision, nil
}

func (repository *memoryDesktopSettings) PutSettings(_ context.Context, input *clientpb.Settings, expectedRevision int64) error {
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
			repository.settings = &clientpb.Settings{}
		}
		id, name, enabled := "concurrent", "Concurrent", true
		repository.settings.SetWebhooks([]*clientpb.WebhookTarget{clientpb.WebhookTarget_builder{Id: &id, Name: &name, Enabled: &enabled}.Build()})
		return application.ErrConflict
	}
	if expectedRevision != repository.revision {
		return application.ErrConflict
	}
	repository.settings = proto.CloneOf(input)
	repository.revision++
	return nil
}

func directSettings() *clientpb.Settings {
	return clientpb.Settings_builder{Network: clientpb.NetworkSettings_builder{Direct: clientpb.DirectNetwork_builder{}.Build()}.Build()}.Build()
}

func directNetworkSettings() *clientpb.NetworkSettings {
	return clientpb.NetworkSettings_builder{Direct: clientpb.DirectNetwork_builder{}.Build()}.Build()
}

func proxyNetworkSettings(urls []string, username, password string) *clientpb.NetworkSettings {
	return clientpb.NetworkSettings_builder{Proxy: clientpb.ProxyNetwork_builder{
		Urls: urls, Username: &username, Password: &password,
	}.Build()}.Build()
}

func webhookTarget(id, name, rawURL, secret string, enabled bool) *clientpb.WebhookTarget {
	return clientpb.WebhookTarget_builder{
		Id: &id, Name: &name, Url: &rawURL, Secret: &secret, Enabled: &enabled,
	}.Build()
}

func TestDesktopSettingsRetryConflictWithoutLosingConcurrentFields(t *testing.T) {
	clearNetworkEnvironment(t)
	repository := &memoryDesktopSettings{conflicts: 1}
	app := newDesktopApp(nil, repository, &recordingEgressConfigurator{})
	skipEgressHealth(app)
	if _, err := app.saveNetworkSettings(directNetworkSettings()); err != nil {
		t.Fatal(err)
	}
	if repository.putCalls != 2 || repository.settings == nil || repository.settings.GetNetwork() == nil ||
		len(repository.settings.GetWebhooks()) != 1 || repository.settings.GetWebhooks()[0].GetId() != "concurrent" {
		t.Fatalf("conflict retry lost Central settings: %s after %d writes", repository.settings, repository.putCalls)
	}
}

func (configurator *recordingHookConfigurator) Configure(targets []*clientpb.WebhookTarget) error {
	if configurator.err != nil {
		return configurator.err
	}
	configurator.targets = make([]*clientpb.WebhookTarget, 0, len(targets))
	for _, target := range targets {
		configurator.targets = append(configurator.targets, proto.CloneOf(target))
	}
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

func TestDesktopNetworkSettingsRequireGeneratedNetwork(t *testing.T) {
	clearNetworkEnvironment(t)
	app := newDesktopApp(nil, &memoryDesktopSettings{}, &recordingEgressConfigurator{})
	if _, err := app.saveNetworkSettings(&clientpb.NetworkSettings{}); err == nil {
		t.Fatal("SaveNetworkSettings(empty) error = nil")
	}
}

func TestDesktopNetworkSettingsBridgeUsesStrictProtoJSON(t *testing.T) {
	clearNetworkEnvironment(t)
	app := newDesktopApp(nil, &memoryDesktopSettings{}, &recordingEgressConfigurator{})
	skipEgressHealth(app)
	input, err := marshalDesktopProtoJSON(proxyNetworkSettings([]string{"http://127.0.0.1:8080"}, "user", "password"))
	if err != nil {
		t.Fatal(err)
	}
	payload, err := app.SaveNetworkSettings(input)
	if err != nil {
		t.Fatal(err)
	}
	saved := &clientpb.NetworkSettings{}
	if err := unmarshalDesktopProtoJSON(payload, saved); err != nil || saved.GetProxy() == nil ||
		saved.GetProxy().GetPassword() != "" || !saved.GetProxy().GetHasPassword() {
		t.Fatalf("saved network settings = %s, error = %v", saved, err)
	}
	payload, err = app.GetNetworkSettings()
	if err != nil {
		t.Fatal(err)
	}
	reloaded := &clientpb.NetworkSettings{}
	if err := unmarshalDesktopProtoJSON(payload, reloaded); err != nil || reloaded.GetProxy() == nil ||
		reloaded.GetProxy().GetPassword() != "" || !reloaded.GetProxy().GetHasPassword() {
		t.Fatalf("reloaded network settings = %s, error = %v", reloaded, err)
	}
	if _, err := app.SaveNetworkSettings(`{"unknownField":true}`); err == nil {
		t.Fatal("SaveNetworkSettings(unknown field) error = nil")
	}
}

func TestDesktopHookSettingsBridgeUsesStrictProtoJSON(t *testing.T) {
	settings := &memoryDesktopSettings{}
	app := newDesktopApp(nil, settings, &recordingEgressConfigurator{}, &recordingHookConfigurator{})
	inputMessage := clientpb.Settings_builder{Webhooks: []*clientpb.WebhookTarget{
		webhookTarget("discord", "Discord", "https://discord.com/api/webhooks/1/token", "secret", true),
	}}.Build()
	input, err := marshalDesktopProtoJSON(inputMessage)
	if err != nil {
		t.Fatal(err)
	}
	payload, err := app.SaveHookSettings(input)
	if err != nil {
		t.Fatal(err)
	}
	saved := &clientpb.Settings{}
	if err := unmarshalDesktopProtoJSON(payload, saved); err != nil || len(saved.GetWebhooks()) != 1 ||
		saved.GetWebhooks()[0].GetSecret() != "" || !saved.GetWebhooks()[0].GetHasSecret() {
		t.Fatalf("saved hook settings = %s, error = %v", saved, err)
	}
	payload, err = app.GetHookSettings()
	if err != nil {
		t.Fatal(err)
	}
	reloaded := &clientpb.Settings{}
	if err := unmarshalDesktopProtoJSON(payload, reloaded); err != nil || len(reloaded.GetWebhooks()) != 1 ||
		reloaded.GetWebhooks()[0].GetSecret() != "" || !reloaded.GetWebhooks()[0].GetHasSecret() {
		t.Fatalf("reloaded hook settings = %s, error = %v", reloaded, err)
	}
	if _, err := app.SaveHookSettings(`{"unknownField":true}`); err == nil {
		t.Fatal("SaveHookSettings(unknown field) error = nil")
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
	input := proxyNetworkSettings([]string{" socks5://127.0.0.1:1080 ", "https://127.0.0.1:8443"}, " user ", "password")
	if _, err := app.saveNetworkSettings(input); !errors.Is(err, validationErr) {
		t.Fatalf("SaveNetworkSettings(invalid) error = %v", err)
	}
	if settings.settings != nil {
		t.Fatalf("invalid settings reached Central: %s", settings.settings)
	}
	app.validateEgress = func(context.Context, egress.Config) error { return nil }
	state, err := app.saveNetworkSettings(input)
	if err != nil || state.GetProxy() == nil || len(state.GetProxy().GetUrls()) != 2 {
		t.Fatalf("state = %s, error = %v", state, err)
	}
	state, err = app.saveNetworkSettings(proxyNetworkSettings(input.GetProxy().GetUrls(), "user", ""))
	if err != nil || state.GetProxy().GetPassword() != "password" {
		t.Fatalf("preserved password state = %s, error = %v", state, err)
	}
}

func TestDesktopHookSettingsPreserveSecretsAndRejectInvalidConfiguration(t *testing.T) {
	settings := &memoryDesktopSettings{}
	hooks := &recordingHookConfigurator{}
	app := newDesktopApp(nil, settings, &recordingEgressConfigurator{}, hooks)
	input := []*clientpb.WebhookTarget{webhookTarget("discord", "Discord", "https://discord.com/api/webhooks/1/token", "secret", true)}
	state, err := app.saveHookSettings(input)
	if err != nil || len(state) != 1 || state[0].GetSecret() != "secret" || len(hooks.targets) != 1 {
		t.Fatalf("state = %v, targets = %+v, error = %v", state, hooks.targets, err)
	}
	input[0].SetSecret("")
	state, err = app.saveHookSettings(input)
	if err != nil || state[0].GetSecret() != "secret" || hooks.targets[0].GetSecret() != "secret" {
		t.Fatalf("preserved state = %v, targets = %+v, error = %v", state, hooks.targets, err)
	}
	hooks.err = errors.New("invalid hook")
	input[0].SetName("Changed")
	if _, err := app.saveHookSettings(input); !errors.Is(err, hooks.err) {
		t.Fatalf("SaveHookSettings(invalid) error = %v", err)
	}
	hooks.err = nil
	reloaded, err := app.getHookSettings()
	if err != nil || reloaded[0].GetName() != "Discord" {
		t.Fatalf("reloaded = %v, error = %v", reloaded, err)
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
	state, err := app.saveNetworkSettings(directNetworkSettings())
	if err != nil || state.GetDirect() == nil || len(configurator.configs[0].Proxies) != 0 {
		t.Fatalf("state = %s, configuration = %+v", state, configurator.configs)
	}
	reloaded := newDesktopApp(nil, settings, &recordingEgressConfigurator{})
	reloadedState, err := reloaded.getNetworkSettings()
	if err != nil || reloadedState.GetDirect() == nil {
		t.Fatalf("reloaded state = %s, error = %v", reloadedState, err)
	}
}

func TestDesktopSettingsBelongToAuthenticatedCentralUser(t *testing.T) {
	clearNetworkEnvironment(t)
	settings := &memoryDesktopSettings{}
	app := newDesktopApp(nil, settings, &recordingEgressConfigurator{})
	app.setUserID("user-1")
	skipEgressHealth(app)
	if _, err := app.saveNetworkSettings(proxyNetworkSettings([]string{"http://127.0.0.1:8080"}, "", "")); err != nil {
		t.Fatal(err)
	}
	stored, err := app.readSettings()
	if err != nil {
		t.Fatal(err)
	}
	if stored.GetNetwork() == nil || stored.GetNetwork().GetProxy() == nil || len(stored.GetNetwork().GetProxy().GetUrls()) != 1 {
		t.Fatalf("settings = %s", stored)
	}
	userID, err := app.GetUserID()
	if err != nil || userID != "user-1" {
		t.Fatalf("GetUserID() = %q, %v", userID, err)
	}
}

func TestApplySavedNetworkSettingsAndValidationFailures(t *testing.T) {
	clearNetworkEnvironment(t)
	settings := clientpb.Settings_builder{Network: &clientpb.NetworkSettings{}}.Build()
	repository := &memoryDesktopSettings{settings: settings}
	configurator := &recordingEgressConfigurator{}
	app := newDesktopApp(nil, repository, configurator)
	if err := app.applySavedNetworkSettings(); err == nil {
		t.Fatal("applySavedNetworkSettings(invalid mode) error = nil")
	}
	settings.SetNetwork(directSettings().GetNetwork())
	configurator.err = errors.New("closed")
	if err := app.applySavedNetworkSettings(); !errors.Is(err, configurator.err) {
		t.Fatalf("applySavedNetworkSettings(configurator failure) error = %v", err)
	}
}

func TestGetNetworkSettingsDefaultsToEnvironmentOrDirect(t *testing.T) {
	clearNetworkEnvironment(t)
	app := newDesktopApp(nil, &memoryDesktopSettings{}, &recordingEgressConfigurator{})
	state, err := app.getNetworkSettings()
	if err != nil || state.GetDirect() == nil {
		t.Fatalf("direct state = %s, error = %v", state, err)
	}
	t.Setenv("CINEKO_SOXY_URL", "https://soxy.example.test")
	t.Setenv("CINEKO_SOXY_API_TOKEN", "environment-value")
	state, err = app.getNetworkSettings()
	if err != nil || state.GetDirect() == nil {
		t.Fatalf("environment state = %s, error = %v", state, err)
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
