package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/cineko-org/client/internal/platform"
)

type desktopIdentity struct {
	InstallationID string `json:"installationId"`
	DeviceID       string `json:"deviceId"`
}

func loadOrCreateDesktopIdentity(dataDir string) (desktopIdentity, error) {
	path := filepath.Join(dataDir, "installation.json")
	contents, err := os.ReadFile(path) // #nosec G304 -- path is inside the configured Cineko data directory.
	if err == nil {
		var identity desktopIdentity
		decoder := json.NewDecoder(strings.NewReader(string(contents)))
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(&identity); err != nil {
			return desktopIdentity{}, fmt.Errorf("decode desktop installation identity: %w", err)
		}
		if identity.InstallationID == "" || identity.DeviceID == "" {
			return desktopIdentity{}, errors.New("desktop installation identity is incomplete")
		}
		return identity, nil
	}
	if !errors.Is(err, os.ErrNotExist) {
		return desktopIdentity{}, fmt.Errorf("read desktop installation identity: %w", err)
	}
	identity := desktopIdentity{
		InstallationID: "install_" + platform.IDGenerator{}.NewID(),
		DeviceID:       "device_" + platform.IDGenerator{}.NewID(),
	}
	if err := writeDesktopIdentity(path, identity); err != nil {
		return desktopIdentity{}, err
	}
	return identity, nil
}

func writeDesktopIdentity(path string, identity desktopIdentity) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return fmt.Errorf("create desktop identity directory: %w", err)
	}
	contents, err := json.MarshalIndent(identity, "", "  ")
	if err != nil {
		return fmt.Errorf("encode desktop installation identity: %w", err)
	}
	temporary, err := os.CreateTemp(filepath.Dir(path), ".installation-*.json")
	if err != nil {
		return fmt.Errorf("create desktop identity file: %w", err)
	}
	temporaryPath := temporary.Name()
	defer func() { _ = os.Remove(temporaryPath) }()
	if err := temporary.Chmod(0o600); err != nil {
		_ = temporary.Close()
		return fmt.Errorf("protect desktop identity file: %w", err)
	}
	if _, err := temporary.Write(contents); err != nil {
		_ = temporary.Close()
		return fmt.Errorf("write desktop identity file: %w", err)
	}
	if err := temporary.Sync(); err != nil {
		_ = temporary.Close()
		return fmt.Errorf("sync desktop identity file: %w", err)
	}
	if err := temporary.Close(); err != nil {
		return fmt.Errorf("close desktop identity file: %w", err)
	}
	if err := os.Rename(temporaryPath, path); err != nil {
		return fmt.Errorf("replace desktop identity file: %w", err)
	}
	return nil
}
