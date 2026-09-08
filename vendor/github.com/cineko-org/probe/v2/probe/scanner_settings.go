package probe

import (
	"context"
	"errors"
	"os"
	"strings"

	"github.com/cineko-org/probe/v2/internal/egress"
)

// ScannerSettings is redacted; the token is accepted only as a write argument.
type ScannerSettings struct {
	URL      string `json:"url"`
	HasToken bool   `json:"hasToken"`
}

func (scanner *LocalScanner) GetSoxySettings() (ScannerSettings, error) {
	scanner.settingsMu.Lock()
	defer scanner.settingsMu.Unlock()
	config, err := egress.LocalScanConfig(scanner.egressPath)
	return ScannerSettings{URL: config.SoxyURL, HasToken: strings.TrimSpace(config.SoxyToken) != ""}, err
}

func (scanner *LocalScanner) SaveSoxySettings(ctx context.Context, url, token string) (ScannerSettings, error) {
	scanner.settingsMu.Lock()
	defer scanner.settingsMu.Unlock()
	url, token = strings.TrimRight(strings.TrimSpace(url), "/"), strings.TrimSpace(token)
	previous, previousErr := egress.LocalScanConfig(scanner.egressPath)
	if token == "" {
		if previousErr != nil || strings.TrimRight(previous.SoxyURL, "/") != url {
			return ScannerSettings{}, errors.New("SOXY 주소를 변경할 때는 API 토큰도 입력하세요")
		}
		token = strings.TrimSpace(previous.SoxyToken)
	}
	config := egress.Config{SoxyURL: url, SoxyToken: token, RequireProxy: true, Logger: scanner.logger, NetworkCapture: scanner.networkCapture}
	if err := egress.ValidateSoxySettings(ctx, config); err != nil {
		return ScannerSettings{}, err
	}
	saved := ScannerSettings{URL: url, HasToken: true}
	_, persistedErr := os.Stat(scanner.egressPath)
	if persistedErr == nil && previousErr == nil && previous.SoxyURL == url && strings.TrimSpace(previous.SoxyToken) == token {
		return saved, nil
	}
	scanner.scheduleMu.Lock()
	defer scanner.scheduleMu.Unlock()
	if scanner.closed {
		return ScannerSettings{}, errors.New("scanner is closed")
	}
	if err := ctx.Err(); err != nil {
		return ScannerSettings{}, err
	}
	if err := scanner.factory.ConfigureEgressAndCommit(config, func() error { return egress.SaveLocalScanConfig(scanner.egressPath, config) }); err != nil {
		return ScannerSettings{}, err
	}
	// Only the anonymous scanner is replaced; the booking browser is untouched.
	if scanner.scheduleSession != nil {
		scanner.scheduleSession.Close()
		scanner.scheduleSession = nil
	}
	return saved, nil
}
