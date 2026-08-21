package main

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"buf.build/go/protovalidate"
	"github.com/cineko-org/client/internal/adapters/egress"
	"github.com/cineko-org/client/internal/application"
	clientpb "github.com/cineko-org/contracts/gen/go/cineko/client"
	"google.golang.org/protobuf/proto"
)

type egressConfigurator interface {
	ConfigureEgress(egress.Config) error
}

func (app *DesktopApp) GetNetworkSettings() (string, error) {
	settings, err := app.getNetworkSettings()
	if err != nil {
		return "", err
	}
	return marshalDesktopProtoJSON(redactDesktopNetworkSettings(settings))
}

func (app *DesktopApp) getNetworkSettings() (*clientpb.NetworkSettings, error) {
	settings, err := app.readSettings()
	if err != nil && !errors.Is(err, application.ErrNotFound) {
		return nil, err
	}
	if settings == nil {
		settings = &clientpb.Settings{}
	}
	environment, err := egress.ConfigFromEnvironment()
	if err != nil {
		return nil, err
	}
	if settings.GetNetwork() == nil {
		if _, err := egress.New(environment); err != nil {
			return nil, err
		}
		return networkSettingsFromConfig(environment), nil
	}
	if _, err := resolveDesktopNetwork(settings.GetNetwork()); err != nil {
		return nil, err
	}
	return proto.CloneOf(settings.GetNetwork()), nil
}

func (app *DesktopApp) SaveNetworkSettings(input string) (string, error) {
	settings := &clientpb.NetworkSettings{}
	if err := unmarshalDesktopProtoJSON(input, settings); err != nil {
		return "", err
	}
	saved, err := app.saveNetworkSettings(settings)
	if err != nil {
		return "", err
	}
	return marshalDesktopProtoJSON(redactDesktopNetworkSettings(saved))
}

// redactDesktopNetworkSettings preserves whether a password exists without
// exposing the stored credential to the untrusted renderer process.
func redactDesktopNetworkSettings(settings *clientpb.NetworkSettings) *clientpb.NetworkSettings {
	redacted := proto.CloneOf(settings)
	if redacted == nil || redacted.GetProxy() == nil {
		return redacted
	}
	hasPassword := redacted.GetProxy().GetPassword() != ""
	redacted.GetProxy().SetPassword("")
	redacted.GetProxy().SetHasPassword(hasPassword)
	return redacted
}

func (app *DesktopApp) saveNetworkSettings(input *clientpb.NetworkSettings) (*clientpb.NetworkSettings, error) {
	if input == nil {
		return nil, errors.New("network settings are required")
	}
	if err := protovalidate.Validate(input); err != nil {
		return nil, fmt.Errorf("network settings violate the contract: %w", err)
	}
	environment, err := egress.ConfigFromEnvironment()
	if err != nil {
		return nil, err
	}
	settings, readErr := app.readSettings()
	if readErr != nil && !errors.Is(readErr, application.ErrNotFound) {
		return nil, readErr
	}
	if settings == nil {
		settings = &clientpb.Settings{}
	}
	stored := prepareDesktopNetworkSettings(input, settings.GetNetwork())
	nextConfig, err := resolveDesktopNetwork(stored)
	if err != nil {
		return nil, err
	}
	previousConfig := environment
	if settings.GetNetwork() != nil {
		previousConfig, err = resolveDesktopNetwork(settings.GetNetwork())
		if err != nil {
			return nil, fmt.Errorf("기존 프록시 설정을 확인할 수 없습니다: %w", err)
		}
	}
	validationContext, cancel := context.WithTimeout(app.contextOrBackground(), 15*time.Second)
	err = app.validateEgress(validationContext, nextConfig)
	cancel()
	if err != nil {
		return nil, fmt.Errorf("프록시 연결 확인 실패: %w", err)
	}
	if app.egress == nil {
		return nil, errors.New("network configuration is unavailable")
	}
	if err := app.egress.ConfigureEgress(nextConfig); err != nil {
		return nil, err
	}
	if err := app.updateSettings(func(settings *clientpb.Settings) error {
		settings.SetNetwork(stored)
		return nil
	}); err != nil {
		return nil, errors.Join(err, app.egress.ConfigureEgress(previousConfig))
	}
	return proto.CloneOf(stored), nil
}

func prepareDesktopNetworkSettings(input, previous *clientpb.NetworkSettings) *clientpb.NetworkSettings {
	if input == nil {
		return nil
	}
	if input.GetProxy() == nil {
		return clientpb.NetworkSettings_builder{Direct: clientpb.DirectNetwork_builder{}.Build()}.Build()
	}
	proxy := proto.CloneOf(input.GetProxy())
	proxy.SetUrls(cleanStrings(proxy.GetUrls()))
	proxy.SetUsername(strings.TrimSpace(proxy.GetUsername()))
	if proxy.GetPassword() == "" && previous != nil && previous.GetProxy() != nil {
		proxy.SetPassword(previous.GetProxy().GetPassword())
	}
	return clientpb.NetworkSettings_builder{Proxy: proxy}.Build()
}

func (app *DesktopApp) applySavedNetworkSettings() error {
	settings, err := app.readSettings()
	if errors.Is(err, application.ErrNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	if settings == nil || settings.GetNetwork() == nil {
		return nil
	}
	config, err := resolveDesktopNetwork(settings.GetNetwork())
	if err != nil {
		return err
	}
	if app.egress == nil {
		return errors.New("network configuration is unavailable")
	}
	return app.egress.ConfigureEgress(config)
}

func resolveDesktopNetwork(stored *clientpb.NetworkSettings) (egress.Config, error) {
	if stored == nil {
		return egress.Config{}, errors.New("network settings are required")
	}
	config := egress.Config{}
	switch desktopNetworkMode(stored) {
	case "direct":
	case "proxy":
		var err error
		config.Proxies, err = parseDesktopProxies(stored)
		if err != nil {
			return egress.Config{}, err
		}
	default:
		return egress.Config{}, fmt.Errorf("지원하지 않는 프록시 모드")
	}
	if _, err := egress.New(config); err != nil {
		return egress.Config{}, err
	}
	return config, nil
}

func desktopNetworkMode(stored *clientpb.NetworkSettings) string {
	if stored == nil {
		return "direct"
	}
	switch {
	case stored.GetProxy() != nil:
		return "proxy"
	case stored.GetDirect() != nil:
		return "direct"
	default:
		return ""
	}
}

func parseDesktopProxies(stored *clientpb.NetworkSettings) ([]egress.Proxy, error) {
	if stored == nil || stored.GetProxy() == nil {
		return nil, errors.New("표준 프록시 주소를 하나 이상 입력하세요")
	}
	proxySettings := stored.GetProxy()
	values := cleanStrings(proxySettings.GetUrls())
	if len(values) == 0 {
		return nil, errors.New("표준 프록시 주소를 하나 이상 입력하세요")
	}
	proxies := make([]egress.Proxy, 0, len(values))
	for _, rawURL := range values {
		proxy, err := egress.ParseProxy(rawURL)
		if err != nil {
			return nil, err
		}
		if proxySettings.GetUsername() != "" {
			proxy.Username, proxy.Password = proxySettings.GetUsername(), proxySettings.GetPassword()
		}
		proxies = append(proxies, proxy)
	}
	return proxies, nil
}

func networkSettingsFromConfig(config egress.Config) *clientpb.NetworkSettings {
	proxies := append(append([]egress.Proxy(nil), config.Proxies...), config.ScanProxies...)
	if len(proxies) == 0 {
		return clientpb.NetworkSettings_builder{Direct: clientpb.DirectNetwork_builder{}.Build()}.Build()
	}
	urls := make([]string, 0, len(proxies))
	username, password := "", ""
	for _, proxy := range proxies {
		urls = append(urls, proxy.Server)
		if username == "" {
			username = proxy.Username
			password = proxy.Password
		}
	}
	proxy := clientpb.ProxyNetwork_builder{Urls: urls, Username: &username, Password: &password}.Build()
	return clientpb.NetworkSettings_builder{Proxy: proxy}.Build()
}

func cleanStrings(values []string) []string {
	result := make([]string, 0, len(values))
	seen := make(map[string]struct{}, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		if _, exists := seen[value]; exists {
			continue
		}
		seen[value] = struct{}{}
		result = append(result, value)
	}
	return result
}

func (app *DesktopApp) contextOrBackground() context.Context {
	if ctx := app.context(); ctx != nil {
		return ctx
	}
	return context.Background()
}

func (app *DesktopApp) checkSavedNetworkHealth(ctx context.Context) {
	settings, err := app.readSettings()
	if errors.Is(err, application.ErrNotFound) {
		return
	}
	if err != nil {
		app.server.RecordSystemEvent(desktopErrorEvent(app.activeUserID(), "network.health_failed", "저장된 프록시 설정을 읽지 못했습니다. 설정을 확인하세요."))
		return
	}
	if settings == nil || settings.GetNetwork() == nil {
		return
	}
	config, err := resolveDesktopNetwork(settings.GetNetwork())
	if err == nil {
		healthContext, cancel := context.WithTimeout(ctx, 15*time.Second)
		err = app.validateEgress(healthContext, config)
		cancel()
	}
	if err != nil {
		app.server.RecordSystemEvent(desktopErrorEvent(app.activeUserID(), "network.health_failed", "저장된 프록시가 동작하지 않습니다. 설정을 확인하세요."))
	}
}
