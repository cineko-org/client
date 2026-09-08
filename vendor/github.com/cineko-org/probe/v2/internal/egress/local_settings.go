package egress

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
)

func ValidateSoxySettings(ctx context.Context, config Config) error {
	if strings.TrimSpace(config.SoxyURL) == "" || strings.TrimSpace(config.SoxyToken) == "" {
		return errors.New("SOXY 주소와 API 토큰을 입력하세요")
	}
	manager, err := New(config)
	if err != nil {
		return err
	}
	_, err = manager.client.availableSlots(ctx)
	// A full server still proves authentication; scanner capacity is checked
	// when acquiring a real lease, not by taking a slot from settings UI.
	if errors.Is(err, ErrNoProxyCapacity) {
		return nil
	}
	return err
}

func SaveLocalScanConfig(path string, config Config) error {
	if path == "" {
		return errors.New("scanner settings path is unavailable")
	}
	data, err := json.Marshal(struct {
		URL   string `json:"soxy_url"`
		Token string `json:"token"`
	}{config.SoxyURL, config.SoxyToken})
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return err
	}
	file, err := os.CreateTemp(filepath.Dir(path), ".egress-*")
	if err != nil {
		return err
	}
	defer func() { _ = os.Remove(file.Name()) }()
	defer func() { _ = file.Close() }()
	if _, err := file.Write(data); err != nil {
		return err
	}
	if err := file.Sync(); err != nil {
		return err
	}
	if err := file.Close(); err != nil {
		return err
	}
	return os.Rename(file.Name(), path)
}
