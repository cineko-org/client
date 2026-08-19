package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strings"

	"github.com/cineko-org/client/internal/application"
)

const maximumLegacySettingsSize = 1 << 20

type legacyDesktopSettings struct {
	LastProject string                  `json:"lastProject,omitempty"`
	UserID      string                  `json:"userId,omitempty"`
	Network     *desktopNetworkSettings `json:"network,omitempty"`
	Hooks       []desktopHookSettings   `json:"hooks,omitempty"`
}

// migrateLegacyDesktopSettings is the only path from the retired local
// settings file to Central. The authenticated Central write is CAS-protected,
// read back for verification, and restart-safe before local credentials are
// removed.
func migrateLegacyDesktopSettings(
	ctx context.Context,
	repository desktopSettingsRepository,
	dataDir string,
) error {
	primary := filepath.Join(dataDir, "settings.json")
	backup := filepath.Join(dataDir, "settings.json.migrating")
	primaryExists, err := regularFileExists(primary)
	if err != nil {
		return err
	}
	backupExists, err := regularFileExists(backup)
	if err != nil {
		return err
	}
	if primaryExists && backupExists {
		return errors.New("both the local settings file and its migration backup exist")
	}
	source := primary
	if !primaryExists {
		if !backupExists {
			return nil
		}
		source = backup
	}
	legacy, err := readLegacyDesktopSettings(source)
	if err != nil {
		return err
	}
	if err := mergeLegacySettingsIntoCentral(ctx, repository, legacy); err != nil {
		return err
	}
	if source == primary {
		if err := os.Rename(primary, backup); err != nil {
			return fmt.Errorf("back up migrated local settings: %w", err)
		}
	}
	if err := os.Remove(backup); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("remove migrated local settings backup: %w", err)
	}
	return nil
}

func regularFileExists(path string) (bool, error) {
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if !info.Mode().IsRegular() {
		return false, fmt.Errorf("legacy settings %s is not a regular file", filepath.Base(path))
	}
	return true, nil
}

func readLegacyDesktopSettings(path string) (legacyDesktopSettings, error) {
	file, err := os.Open(path) // #nosec G304 -- fixed file inside the application data directory.
	if err != nil {
		return legacyDesktopSettings{}, err
	}
	defer func() { _ = file.Close() }()
	contents, err := io.ReadAll(io.LimitReader(file, maximumLegacySettingsSize+1))
	if err != nil {
		return legacyDesktopSettings{}, err
	}
	if len(contents) > maximumLegacySettingsSize {
		return legacyDesktopSettings{}, errors.New("legacy settings exceed the size limit")
	}
	var settings legacyDesktopSettings
	decoder := json.NewDecoder(bytes.NewReader(contents))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&settings); err != nil {
		return legacyDesktopSettings{}, fmt.Errorf("decode legacy settings: %w", err)
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return legacyDesktopSettings{}, errors.New("legacy settings contain trailing data")
	}
	return settings, nil
}

func mergeLegacySettingsIntoCentral(
	ctx context.Context,
	repository desktopSettingsRepository,
	legacy legacyDesktopSettings,
) error {
	if repository == nil {
		return errors.New("central settings are unavailable")
	}
	for range 3 {
		var current desktopSettings
		revision, err := repository.GetSettings(ctx, &current)
		if errors.Is(err, application.ErrNotFound) {
			current, revision = desktopSettings{}, 0
		} else if err != nil {
			return err
		}
		merged, err := mergeLegacyDesktopSettings(current, legacy)
		if err != nil {
			return err
		}
		if !reflect.DeepEqual(current, merged) {
			if err := repository.PutSettings(ctx, merged, revision); err != nil {
				if errors.Is(err, application.ErrConflict) {
					continue
				}
				return err
			}
		}
		var verified desktopSettings
		if _, err := repository.GetSettings(ctx, &verified); errors.Is(err, application.ErrNotFound) {
			verified = desktopSettings{}
		} else if err != nil {
			return fmt.Errorf("verify migrated Central settings: %w", err)
		}
		verification, err := mergeLegacyDesktopSettings(verified, legacy)
		if err != nil || !reflect.DeepEqual(verified, verification) {
			return errors.New("central did not retain the migrated settings")
		}
		return nil
	}
	return application.ErrConflict
}

func mergeLegacyDesktopSettings(current desktopSettings, legacy legacyDesktopSettings) (desktopSettings, error) {
	merged := cloneDesktopSettings(current)
	if err := mergeLegacyNetwork(&merged, legacy.Network); err != nil {
		return desktopSettings{}, err
	}
	if err := mergeLegacyHooks(&merged, legacy.Hooks); err != nil {
		return desktopSettings{}, err
	}
	return merged, nil
}

func mergeLegacyNetwork(merged *desktopSettings, legacy *desktopNetworkSettings) error {
	switch {
	case legacy == nil:
	case merged.Network == nil:
		copy := *legacy
		copy.ProxyURLs = append([]string(nil), legacy.ProxyURLs...)
		merged.Network = &copy
	case !sameNetworkWithoutSecrets(*merged.Network, *legacy):
		return errors.New("local and Central proxy settings conflict")
	default:
		if merged.Network.ProxyPassword != "" && legacy.ProxyPassword != "" &&
			merged.Network.ProxyPassword != legacy.ProxyPassword {
			return errors.New("local and Central proxy password conflict")
		}
		if merged.Network.SoxyAPIToken != "" && legacy.SoxyAPIToken != "" &&
			merged.Network.SoxyAPIToken != legacy.SoxyAPIToken {
			return errors.New("local and Central Soxy token conflict")
		}
		if merged.Network.ProxyPassword == "" {
			merged.Network.ProxyPassword = legacy.ProxyPassword
		}
		if merged.Network.SoxyAPIToken == "" {
			merged.Network.SoxyAPIToken = legacy.SoxyAPIToken
		}
	}
	return nil
}

func mergeLegacyHooks(merged *desktopSettings, legacyHooks []desktopHookSettings) error {
	byID := make(map[string]int, len(merged.Hooks))
	for index, hook := range merged.Hooks {
		byID[hook.ID] = index
	}
	for _, legacyHook := range legacyHooks {
		index, exists := byID[legacyHook.ID]
		if !exists {
			legacyHook.EventKinds = append([]string(nil), legacyHook.EventKinds...)
			merged.Hooks = append(merged.Hooks, legacyHook)
			byID[legacyHook.ID] = len(merged.Hooks) - 1
			continue
		}
		if !sameHookWithoutSecret(merged.Hooks[index], legacyHook) {
			return fmt.Errorf("local and Central hook %q conflict", legacyHook.ID)
		}
		if merged.Hooks[index].Secret != "" && legacyHook.Secret != "" &&
			merged.Hooks[index].Secret != legacyHook.Secret {
			return fmt.Errorf("local and Central hook %q secret conflict", legacyHook.ID)
		}
		if merged.Hooks[index].Secret == "" {
			merged.Hooks[index].Secret = legacyHook.Secret
		}
	}
	return nil
}

func cloneDesktopSettings(settings desktopSettings) desktopSettings {
	clone := settings
	if settings.Network != nil {
		network := *settings.Network
		network.ProxyURLs = append([]string(nil), settings.Network.ProxyURLs...)
		clone.Network = &network
	}
	clone.Hooks = append([]desktopHookSettings(nil), settings.Hooks...)
	for index := range clone.Hooks {
		clone.Hooks[index].EventKinds = append([]string(nil), settings.Hooks[index].EventKinds...)
	}
	return clone
}

func sameNetworkWithoutSecrets(left, right desktopNetworkSettings) bool {
	left.Mode, right.Mode = desktopNetworkMode(left), desktopNetworkMode(right)
	left.ProxyURLs, right.ProxyURLs = cleanStrings(left.ProxyURLs), cleanStrings(right.ProxyURLs)
	left.ProxyUsername, right.ProxyUsername = strings.TrimSpace(left.ProxyUsername), strings.TrimSpace(right.ProxyUsername)
	left.SoxyURL, right.SoxyURL = strings.TrimSpace(left.SoxyURL), strings.TrimSpace(right.SoxyURL)
	left.SoxySessionTTL, right.SoxySessionTTL = strings.TrimSpace(left.SoxySessionTTL), strings.TrimSpace(right.SoxySessionTTL)
	left.ProxyPassword, right.ProxyPassword = "", ""
	left.SoxyAPIToken, right.SoxyAPIToken = "", ""
	return reflect.DeepEqual(left, right)
}

func sameHookWithoutSecret(left, right desktopHookSettings) bool {
	left.Secret, right.Secret = "", ""
	left.ID, right.ID = strings.TrimSpace(left.ID), strings.TrimSpace(right.ID)
	left.Name, right.Name = strings.TrimSpace(left.Name), strings.TrimSpace(right.Name)
	left.URL, right.URL = strings.TrimSpace(left.URL), strings.TrimSpace(right.URL)
	left.EventKinds, right.EventKinds = cleanStrings(left.EventKinds), cleanStrings(right.EventKinds)
	return reflect.DeepEqual(left, right)
}
