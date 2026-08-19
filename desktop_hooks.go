package main

import (
	"errors"
	"strings"

	"github.com/cineko-org/client/internal/adapters/eventhook"
	"github.com/cineko-org/client/internal/application"
)

type desktopHookSettings struct {
	ID         string         `json:"id"`
	Name       string         `json:"name"`
	Kind       eventhook.Kind `json:"kind"`
	URL        string         `json:"url"`
	Secret     string         `json:"secret,omitempty"`
	EventKinds []string       `json:"eventKinds,omitempty"`
	Enabled    bool           `json:"enabled"`
}

type HookTargetSettings struct {
	ID         string         `json:"id"`
	Name       string         `json:"name"`
	Kind       eventhook.Kind `json:"kind"`
	URL        string         `json:"url"`
	EventKinds []string       `json:"eventKinds,omitempty"`
	Enabled    bool           `json:"enabled"`
	HasSecret  bool           `json:"hasSecret"`
}

type HookSettings struct {
	Targets []HookTargetSettings `json:"targets"`
}

type HookTargetInput struct {
	ID         string         `json:"id"`
	Name       string         `json:"name"`
	Kind       eventhook.Kind `json:"kind"`
	URL        string         `json:"url"`
	Secret     string         `json:"secret"`
	EventKinds []string       `json:"eventKinds"`
	Enabled    bool           `json:"enabled"`
}

type HookSettingsInput struct {
	Targets []HookTargetInput `json:"targets"`
}

type hookConfigurator interface {
	Configure([]eventhook.Target) error
}

func (app *DesktopApp) GetHookSettings() (HookSettings, error) {
	settings, err := app.readSettings()
	if errors.Is(err, application.ErrNotFound) {
		return HookSettings{Targets: []HookTargetSettings{}}, nil
	}
	if err != nil {
		return HookSettings{}, err
	}
	return hookSettingsState(settings.Hooks), nil
}

func (app *DesktopApp) SaveHookSettings(input HookSettingsInput) (HookSettings, error) {
	settings, err := app.readSettings()
	if err != nil && !errors.Is(err, application.ErrNotFound) {
		return HookSettings{}, err
	}
	stored := make([]desktopHookSettings, 0, len(input.Targets))
	previousSecrets := make(map[string]string, len(settings.Hooks))
	for _, target := range settings.Hooks {
		previousSecrets[target.ID] = target.Secret
	}
	for _, inputTarget := range input.Targets {
		target := desktopHookSettings{
			ID: strings.TrimSpace(inputTarget.ID), Name: strings.TrimSpace(inputTarget.Name),
			Kind: inputTarget.Kind, URL: strings.TrimSpace(inputTarget.URL), Secret: inputTarget.Secret,
			EventKinds: cleanStrings(inputTarget.EventKinds), Enabled: inputTarget.Enabled,
		}
		if target.Secret == "" {
			target.Secret = previousSecrets[target.ID]
		}
		stored = append(stored, target)
	}
	if app.hooks == nil {
		return HookSettings{}, errors.New("hook configuration is unavailable")
	}
	if err := app.hooks.Configure(hookTargets(stored)); err != nil {
		return HookSettings{}, err
	}
	if err := app.updateSettings(func(settings *desktopSettings) error {
		settings.Hooks = stored
		return nil
	}); err != nil {
		_ = app.hooks.Configure(hookTargets(settings.Hooks))
		return HookSettings{}, err
	}
	return hookSettingsState(stored), nil
}

func (app *DesktopApp) applySavedHookSettings() error {
	settings, err := app.readSettings()
	if errors.Is(err, application.ErrNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	if app.hooks == nil {
		if len(settings.Hooks) == 0 {
			return nil
		}
		return errors.New("hook configuration is unavailable")
	}
	return app.hooks.Configure(hookTargets(settings.Hooks))
}

func hookTargets(settings []desktopHookSettings) []eventhook.Target {
	targets := make([]eventhook.Target, 0, len(settings))
	for _, target := range settings {
		targets = append(targets, eventhook.Target{
			ID: target.ID, Name: target.Name, Kind: target.Kind, URL: target.URL,
			Secret: target.Secret, EventKinds: append([]string(nil), target.EventKinds...), Enabled: target.Enabled,
		})
	}
	return targets
}

func hookSettingsState(settings []desktopHookSettings) HookSettings {
	state := HookSettings{Targets: make([]HookTargetSettings, 0, len(settings))}
	for _, target := range settings {
		state.Targets = append(state.Targets, HookTargetSettings{
			ID: target.ID, Name: target.Name, Kind: target.Kind, URL: target.URL,
			EventKinds: append([]string(nil), target.EventKinds...), Enabled: target.Enabled,
			HasSecret: target.Secret != "",
		})
	}
	return state
}
