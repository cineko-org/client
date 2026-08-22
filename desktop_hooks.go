package main

import (
	"errors"
	"fmt"
	"strings"

	"buf.build/go/protovalidate"
	"github.com/cineko-org/client/internal/application"
	clientpb "github.com/cineko-org/contracts/gen/go/cineko/client"
	"google.golang.org/protobuf/proto"
)

type hookConfigurator interface {
	Configure([]*clientpb.WebhookTarget) error
}

func (app *DesktopApp) GetHookSettings() (string, error) {
	targets, err := app.getHookSettings()
	if err != nil {
		return "", err
	}
	return marshalDesktopProtoJSON(clientpb.Settings_builder{Webhooks: redactDesktopWebhookTargets(targets)}.Build())
}

func (app *DesktopApp) getHookSettings() ([]*clientpb.WebhookTarget, error) {
	settings, err := app.readSettings()
	if errors.Is(err, application.ErrNotFound) {
		return []*clientpb.WebhookTarget{}, nil
	}
	if err != nil {
		return nil, err
	}
	if settings == nil {
		return []*clientpb.WebhookTarget{}, nil
	}
	return cloneWebhookTargets(settings.GetWebhooks()), nil
}

func (app *DesktopApp) SaveHookSettings(input string) (string, error) {
	settings := &clientpb.Settings{}
	if err := unmarshalDesktopProtoJSON(input, settings); err != nil {
		return "", err
	}
	targets, err := app.saveHookSettings(settings.GetWebhooks())
	if err != nil {
		return "", err
	}
	return marshalDesktopProtoJSON(clientpb.Settings_builder{Webhooks: redactDesktopWebhookTargets(targets)}.Build())
}

// redactDesktopWebhookTargets preserves secret presence without exposing
// stored webhook signing material to the untrusted renderer process.
func redactDesktopWebhookTargets(targets []*clientpb.WebhookTarget) []*clientpb.WebhookTarget {
	redacted := cloneWebhookTargets(targets)
	for _, target := range redacted {
		hasSecret := target.GetSecret() != ""
		target.SetSecret("")
		target.SetHasSecret(hasSecret)
	}
	return redacted
}

func (app *DesktopApp) saveHookSettings(input []*clientpb.WebhookTarget) ([]*clientpb.WebhookTarget, error) {
	settings, err := app.readSettings()
	if err != nil && !errors.Is(err, application.ErrNotFound) {
		return nil, err
	}
	if settings == nil {
		settings = &clientpb.Settings{}
	}
	previous := settings.GetWebhooks()
	previousSecrets := make(map[string]string, len(previous))
	for _, target := range previous {
		if target != nil {
			previousSecrets[target.GetId()] = target.GetSecret()
		}
	}
	stored := make([]*clientpb.WebhookTarget, 0, len(input))
	for _, inputTarget := range input {
		if inputTarget == nil {
			continue
		}
		if err := protovalidate.Validate(inputTarget); err != nil {
			return nil, fmt.Errorf("webhook target violates the contract: %w", err)
		}
		target := proto.CloneOf(inputTarget)
		target.SetId(strings.TrimSpace(target.GetId()))
		target.SetName(strings.TrimSpace(target.GetName()))
		target.SetUrl(strings.TrimSpace(target.GetUrl()))
		target.SetEventKinds(cleanStrings(target.GetEventKinds()))
		if target.GetSecret() == "" {
			target.SetSecret(previousSecrets[target.GetId()])
		}
		stored = append(stored, target)
	}
	if app.hooks == nil {
		return nil, errors.New("hook configuration is unavailable")
	}
	if err := app.hooks.Configure(stored); err != nil {
		return nil, err
	}
	if err := app.updateSettings(func(settings *clientpb.Settings) error {
		settings.SetWebhooks(stored)
		return nil
	}); err != nil {
		_ = app.hooks.Configure(previous)
		return nil, err
	}
	return cloneWebhookTargets(stored), nil
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
		if settings == nil || len(settings.GetWebhooks()) == 0 {
			return nil
		}
		return errors.New("hook configuration is unavailable")
	}
	if settings == nil {
		return nil
	}
	return app.hooks.Configure(settings.GetWebhooks())
}

func cloneWebhookTargets(settings []*clientpb.WebhookTarget) []*clientpb.WebhookTarget {
	result := make([]*clientpb.WebhookTarget, 0, len(settings))
	for _, target := range settings {
		if target != nil {
			result = append(result, proto.CloneOf(target))
		}
	}
	return result
}
