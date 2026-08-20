package main

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/cineko-org/client/internal/adapters/egress"
	"github.com/cineko-org/client/internal/application"
	"github.com/cineko-org/client/internal/domain"
)

const defaultSoxyLeaseTTL = 30 * time.Minute

type desktopNetworkSettings struct {
	Mode           string   `json:"mode,omitempty"`
	ProxyURLs      []string `json:"proxyUrls,omitempty"`
	ProxyUsername  string   `json:"proxyUsername,omitempty"`
	ProxyPassword  string   `json:"proxyPassword,omitempty"`
	SoxyURL        string   `json:"soxyUrl"`
	SoxyAPIToken   string   `json:"soxyApiToken,omitempty"`
	SoxySessionTTL string   `json:"soxySessionTtl"`
}

type NetworkSettings struct {
	Mode             string   `json:"mode"`
	ProxyURLs        []string `json:"proxyUrls,omitempty"`
	ProxyUsername    string   `json:"proxyUsername,omitempty"`
	HasProxyPassword bool     `json:"hasProxyPassword"`
	SoxyURL          string   `json:"soxyUrl"`
	SoxySessionTTL   string   `json:"soxySessionTtl"`
	HasAPIToken      bool     `json:"hasApiToken"`
	Source           string   `json:"source"`
}

type NetworkSettingsInput struct {
	Mode           string   `json:"mode"`
	ProxyURLs      []string `json:"proxyUrls"`
	ProxyUsername  string   `json:"proxyUsername"`
	ProxyPassword  string   `json:"proxyPassword"`
	SoxyURL        string   `json:"soxyUrl"`
	SoxyAPIToken   string   `json:"soxyApiToken"`
	SoxySessionTTL string   `json:"soxySessionTtl"`
}

type egressConfigurator interface {
	ConfigureEgress(egress.Config) error
}

func (app *DesktopApp) GetNetworkSettings() (NetworkSettings, error) {
	settings, err := app.readSettings()
	if err != nil && !errors.Is(err, application.ErrNotFound) {
		return NetworkSettings{}, err
	}
	environment, err := egress.ConfigFromEnvironment()
	if err != nil {
		return NetworkSettings{}, err
	}
	if settings.Network == nil {
		if _, err := egress.New(environment); err != nil {
			return NetworkSettings{}, err
		}
		return networkSettingsState(environment, "environment"), nil
	}
	config, err := resolveDesktopNetwork(*settings.Network, environment)
	if err != nil {
		return NetworkSettings{}, err
	}
	return networkSettingsState(config, "settings"), nil
}

func (app *DesktopApp) SaveNetworkSettings(input NetworkSettingsInput) (NetworkSettings, error) {
	environment, err := egress.ConfigFromEnvironment()
	if err != nil {
		return NetworkSettings{}, err
	}
	settings, readErr := app.readSettings()
	if readErr != nil && !errors.Is(readErr, application.ErrNotFound) {
		return NetworkSettings{}, readErr
	}
	stored := prepareDesktopNetworkSettings(input, settings.Network)
	nextConfig, err := resolveDesktopNetwork(stored, environment)
	if err != nil {
		return NetworkSettings{}, err
	}
	previousConfig := environment
	if settings.Network != nil {
		previousConfig, err = resolveDesktopNetwork(*settings.Network, environment)
		if err != nil {
			return NetworkSettings{}, fmt.Errorf("기존 프록시 설정을 확인할 수 없습니다: %w", err)
		}
	}
	validationContext, cancel := context.WithTimeout(app.contextOrBackground(), 15*time.Second)
	err = app.validateEgress(validationContext, nextConfig)
	cancel()
	if err != nil {
		return NetworkSettings{}, fmt.Errorf("프록시 연결 확인 실패: %w", err)
	}
	if app.egress == nil {
		return NetworkSettings{}, errors.New("network configuration is unavailable")
	}
	if err := app.egress.ConfigureEgress(nextConfig); err != nil {
		return NetworkSettings{}, err
	}
	stored.SoxySessionTTL = formatSessionTTL(nextConfig.SessionTTL)
	if err := app.updateSettings(func(settings *desktopSettings) error {
		settings.Network = &stored
		return nil
	}); err != nil {
		return NetworkSettings{}, errors.Join(err, app.egress.ConfigureEgress(previousConfig))
	}
	return networkSettingsState(nextConfig, "settings"), nil
}

func prepareDesktopNetworkSettings(input NetworkSettingsInput, previous *desktopNetworkSettings) desktopNetworkSettings {
	stored := desktopNetworkSettings{
		Mode: strings.TrimSpace(input.Mode), ProxyURLs: cleanStrings(input.ProxyURLs),
		ProxyUsername: strings.TrimSpace(input.ProxyUsername), ProxyPassword: input.ProxyPassword,
		SoxyURL: strings.TrimSpace(input.SoxyURL), SoxyAPIToken: strings.TrimSpace(input.SoxyAPIToken),
		SoxySessionTTL: strings.TrimSpace(input.SoxySessionTTL),
	}
	stored.Mode = desktopNetworkMode(stored)
	if stored.SoxySessionTTL == "" {
		stored.SoxySessionTTL = defaultSoxyLeaseTTL.String()
	}
	if previous == nil {
		return stored
	}
	if stored.Mode == "proxy" && stored.ProxyPassword == "" {
		stored.ProxyPassword = previous.ProxyPassword
	}
	if stored.Mode == "soxy" && stored.SoxyAPIToken == "" {
		stored.SoxyAPIToken = previous.SoxyAPIToken
	}
	return stored
}

func (app *DesktopApp) applySavedNetworkSettings() error {
	settings, err := app.readSettings()
	if errors.Is(err, application.ErrNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	if settings.Network == nil {
		return nil
	}
	environment, err := egress.ConfigFromEnvironment()
	if err != nil {
		return err
	}
	config, err := resolveDesktopNetwork(*settings.Network, environment)
	if err != nil {
		return err
	}
	if app.egress == nil {
		return errors.New("network configuration is unavailable")
	}
	return app.egress.ConfigureEgress(config)
}

func resolveDesktopNetwork(stored desktopNetworkSettings, environment egress.Config) (egress.Config, error) {
	ttlText := strings.TrimSpace(stored.SoxySessionTTL)
	if ttlText == "" {
		ttlText = defaultSoxyLeaseTTL.String()
	}
	ttl, err := time.ParseDuration(ttlText)
	if err != nil {
		return egress.Config{}, fmt.Errorf("세션 유지 시간을 확인하세요: %w", err)
	}
	config := egress.Config{SessionTTL: ttl}
	mode := desktopNetworkMode(stored)
	switch mode {
	case "direct":
	case "proxy":
		config.Proxies, err = parseDesktopProxies(stored)
		if err != nil {
			return egress.Config{}, err
		}
	case "soxy":
		config.SoxyURL = strings.TrimSpace(stored.SoxyURL)
		config.SoxyToken = strings.TrimSpace(stored.SoxyAPIToken)
		if config.SoxyToken == "" {
			config.SoxyToken = environment.SoxyToken
		}
	default:
		return egress.Config{}, fmt.Errorf("지원하지 않는 프록시 모드 %q", mode)
	}
	if _, err := egress.New(config); err != nil {
		return egress.Config{}, err
	}
	return config, nil
}

func desktopNetworkMode(stored desktopNetworkSettings) string {
	if mode := strings.TrimSpace(stored.Mode); mode != "" {
		return mode
	}
	switch {
	case strings.TrimSpace(stored.SoxyURL) != "":
		return "soxy"
	case len(stored.ProxyURLs) > 0:
		return "proxy"
	default:
		return "direct"
	}
}

func parseDesktopProxies(stored desktopNetworkSettings) ([]egress.Proxy, error) {
	values := cleanStrings(stored.ProxyURLs)
	if len(values) == 0 {
		return nil, errors.New("표준 프록시 주소를 하나 이상 입력하세요")
	}
	proxies := make([]egress.Proxy, 0, len(values))
	for _, rawURL := range values {
		proxy, err := egress.ParseProxy(rawURL)
		if err != nil {
			return nil, err
		}
		if stored.ProxyUsername != "" {
			proxy.Username, proxy.Password = stored.ProxyUsername, stored.ProxyPassword
		}
		proxies = append(proxies, proxy)
	}
	return proxies, nil
}

func networkSettingsState(config egress.Config, source string) NetworkSettings {
	mode := "direct"
	if strings.TrimSpace(config.SoxyURL) != "" {
		mode = "soxy"
	} else if len(config.Proxies)+len(config.ScanProxies) > 0 {
		mode = "proxy"
	}
	state := NetworkSettings{
		Mode: mode, SoxyURL: strings.TrimSpace(config.SoxyURL),
		SoxySessionTTL: formatSessionTTL(config.SessionTTL), HasAPIToken: config.SoxyToken != "", Source: source,
	}
	proxies := append(append([]egress.Proxy(nil), config.Proxies...), config.ScanProxies...)
	for _, proxy := range proxies {
		state.ProxyURLs = append(state.ProxyURLs, proxy.Server)
		if state.ProxyUsername == "" {
			state.ProxyUsername = proxy.Username
		}
		state.HasProxyPassword = state.HasProxyPassword || proxy.Password != ""
	}
	return state
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
		app.server.RecordSystemEvent(app.activeUserID(), "network.health_failed", domain.EventError, "프록시 설정을 읽지 못했습니다. 설정을 확인하세요.")
		return
	}
	if settings.Network == nil {
		return
	}
	environment, err := egress.ConfigFromEnvironment()
	if err == nil {
		var config egress.Config
		config, err = resolveDesktopNetwork(*settings.Network, environment)
		if err == nil {
			healthContext, cancel := context.WithTimeout(ctx, 15*time.Second)
			err = app.validateEgress(healthContext, config)
			cancel()
		}
	}
	if err != nil {
		app.server.RecordSystemEvent(app.activeUserID(), "network.health_failed", domain.EventError, "저장된 프록시가 동작하지 않습니다. 설정을 확인하세요.")
	}
}

func formatSessionTTL(ttl time.Duration) string {
	if ttl > 0 && ttl%time.Hour == 0 {
		return fmt.Sprintf("%dh", ttl/time.Hour)
	}
	if ttl > 0 && ttl%time.Minute == 0 {
		return fmt.Sprintf("%dm", ttl/time.Minute)
	}
	return ttl.String()
}
