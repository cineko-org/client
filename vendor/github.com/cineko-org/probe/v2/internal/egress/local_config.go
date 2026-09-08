package egress

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

// LocalScanConfig persists scanner-only routing independently of launcher
// environment and authenticated browser settings. Credentials stay in a file.
func LocalScanConfig(path string) (Config, error) {
	data, err := os.ReadFile(filepath.Clean(path))
	if errors.Is(err, os.ErrNotExist) || path == "" {
		config, err := ConfigFromEnvironment()
		config.RequireProxy = true
		return config, err
	}
	if err != nil {
		return Config{}, fmt.Errorf("read scanner egress configuration: %w", err)
	}
	var settings struct {
		SoxyURL   string `json:"soxy_url"`
		TokenFile string `json:"token_file"`
		Token     string `json:"token"`
	}
	if err := json.Unmarshal(data, &settings); err != nil {
		return Config{}, errors.New("invalid scanner egress configuration JSON")
	}
	if settings.SoxyURL == "" || (settings.TokenFile == "" && settings.Token == "") {
		return Config{}, errors.New("scanner egress requires soxy_url and token_file")
	}
	if settings.Token != "" {
		return Config{SoxyURL: settings.SoxyURL, SoxyToken: settings.Token, RequireProxy: true}, nil
	}
	tokenPath := settings.TokenFile
	if !filepath.IsAbs(tokenPath) {
		tokenPath = filepath.Join(filepath.Dir(path), tokenPath)
	}
	token, err := readSecretFile(tokenPath, maximumTokenBytes)
	if err != nil {
		return Config{}, fmt.Errorf("read scanner Soxy token: %w", err)
	}
	return Config{SoxyURL: settings.SoxyURL, SoxyToken: token, RequireProxy: true}, nil
}
