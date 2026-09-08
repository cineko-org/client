package main

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/cineko-org/client/internal/logging"
	"github.com/cineko-org/probe/v2/probe"
)

type scannerSettingsService interface {
	GetSoxySettings() (probe.ScannerSettings, error)
	SaveSoxySettings(context.Context, string, string) (probe.ScannerSettings, error)
}

func (app *DesktopApp) GetScannerSettings() (string, error) {
	if app.scanner == nil {
		return "", errors.New("신규 조회 설정을 사용할 수 없습니다")
	}
	settings, err := app.scanner.GetSoxySettings()
	if err != nil {
		return "", err
	}
	data, err := json.Marshal(settings)
	return string(data), err
}

func (app *DesktopApp) SaveScannerSettings(input string) (string, error) {
	if app.scanner == nil {
		return "", errors.New("신규 조회 설정을 사용할 수 없습니다")
	}
	var request struct {
		URL   string `json:"url"`
		Token string `json:"token"`
	}
	if err := json.Unmarshal([]byte(input), &request); err != nil {
		return "", errors.New("SOXY 설정 형식이 올바르지 않습니다")
	}
	ctx, cancel := context.WithTimeout(app.contextOrBackground(), 20*time.Second)
	defer cancel()
	settings, err := app.scanner.SaveSoxySettings(ctx, request.URL, request.Token)
	if err != nil {
		logging.WarnUnexpected(ctx, "scanner.settings.save.failed", "settings", "save_scanner_settings", "reachable Soxy and durable settings", "settings were not changed", "error", err.Error())
		return "", err
	}
	logging.Info(ctx, "scanner.settings.saved", "event", "scanner.settings.saved", "scenario", "settings", "operation", "save_scanner_settings")
	data, err := json.Marshal(settings)
	return string(data), err
}
