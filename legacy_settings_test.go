package main

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/cineko-org/client/internal/adapters/eventhook"
)

func TestLegacySettingsMigrationPersistsSecretsBeforeRemovingLocalFile(t *testing.T) {
	t.Parallel()
	directory := t.TempDir()
	legacy := legacyDesktopSettings{
		UserID: "retired-local-user",
		Network: &desktopNetworkSettings{
			Mode: "soxy", SoxyURL: "https://soxy.example.test",
			SoxyAPIToken: "soxy-secret", SoxySessionTTL: "30m",
		},
		Hooks: []desktopHookSettings{{
			ID: "discord", Name: "Discord", Kind: eventhook.KindDiscord,
			URL: "https://discord.com/api/webhooks/1/token", Secret: "hook-secret", Enabled: true,
		}},
	}
	writeLegacySettings(t, filepath.Join(directory, "settings.json"), legacy)
	repository := &memoryDesktopSettings{}
	if err := migrateLegacyDesktopSettings(t.Context(), repository, directory); err != nil {
		t.Fatal(err)
	}
	if repository.settings == nil || repository.settings.Network == nil ||
		repository.settings.Network.SoxyAPIToken != "soxy-secret" ||
		len(repository.settings.Hooks) != 1 || repository.settings.Hooks[0].Secret != "hook-secret" {
		t.Fatalf("migrated settings = %+v", repository.settings)
	}
	for _, name := range []string{"settings.json", "settings.json.migrating"} {
		if _, err := os.Stat(filepath.Join(directory, name)); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("local credential file %s remains: %v", name, err)
		}
	}
	putCalls := repository.putCalls
	if err := migrateLegacyDesktopSettings(t.Context(), repository, directory); err != nil {
		t.Fatalf("idempotent restart = %v", err)
	}
	if repository.putCalls != putCalls {
		t.Fatalf("idempotent restart made %d extra writes", repository.putCalls-putCalls)
	}
}

func TestLegacySettingsMigrationRecoversBackupAndRetriesCASConflict(t *testing.T) {
	t.Parallel()
	directory := t.TempDir()
	legacy := legacyDesktopSettings{Hooks: []desktopHookSettings{{
		ID: "custom", Name: "Custom", Kind: eventhook.KindWebhook,
		URL: "https://example.test/hook", Secret: "secret", Enabled: true,
	}}}
	writeLegacySettings(t, filepath.Join(directory, "settings.json.migrating"), legacy)
	repository := &memoryDesktopSettings{conflicts: 1}
	if err := migrateLegacyDesktopSettings(t.Context(), repository, directory); err != nil {
		t.Fatal(err)
	}
	if repository.putCalls != 2 || len(repository.settings.Hooks) != 2 {
		t.Fatalf("CAS migration writes = %d, settings = %+v", repository.putCalls, repository.settings)
	}
	if _, err := os.Stat(filepath.Join(directory, "settings.json.migrating")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("verified migration backup remains: %v", err)
	}
}

func TestLegacySettingsMigrationRetainsSourceOnFailureOrConflict(t *testing.T) {
	t.Parallel()
	for name, repository := range map[string]*memoryDesktopSettings{
		"write failure": {putErr: errors.New("central unavailable")},
		"conflicting settings": {settings: &desktopSettings{Network: &desktopNetworkSettings{
			Mode: "soxy", SoxyURL: "https://other.example.test", SoxyAPIToken: "other", SoxySessionTTL: "30m",
		}}, revision: 1},
	} {
		t.Run(name, func(t *testing.T) {
			directory := t.TempDir()
			path := filepath.Join(directory, "settings.json")
			writeLegacySettings(t, path, legacyDesktopSettings{Network: &desktopNetworkSettings{
				Mode: "soxy", SoxyURL: "https://soxy.example.test",
				SoxyAPIToken: "secret", SoxySessionTTL: "30m",
			}})
			if err := migrateLegacyDesktopSettings(t.Context(), repository, directory); err == nil {
				t.Fatal("failed migration returned nil")
			}
			if _, err := os.Stat(path); err != nil {
				t.Fatalf("source was removed after failed migration: %v", err)
			}
		})
	}
}

func TestLegacySettingsMigrationRejectsAmbiguousAndMalformedFiles(t *testing.T) {
	t.Parallel()
	directory := t.TempDir()
	primary := filepath.Join(directory, "settings.json")
	backup := filepath.Join(directory, "settings.json.migrating")
	writeLegacySettings(t, primary, legacyDesktopSettings{})
	writeLegacySettings(t, backup, legacyDesktopSettings{})
	if err := migrateLegacyDesktopSettings(t.Context(), &memoryDesktopSettings{}, directory); err == nil {
		t.Fatal("ambiguous migration files accepted")
	}
	if err := os.Remove(backup); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(primary, []byte(`{"unexpected":true}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := migrateLegacyDesktopSettings(t.Context(), &memoryDesktopSettings{}, directory); err == nil {
		t.Fatal("unknown legacy settings accepted")
	}
}

func TestLegacySettingsMigrationDiscardsOnlyRetiredIdentityAfterAuthenticatedRead(t *testing.T) {
	t.Parallel()
	directory := t.TempDir()
	path := filepath.Join(directory, "settings.json")
	writeLegacySettings(t, path, legacyDesktopSettings{UserID: "retired", LastProject: "/not/activated"})
	repository := &memoryDesktopSettings{}
	if err := migrateLegacyDesktopSettings(t.Context(), repository, directory); err != nil {
		t.Fatal(err)
	}
	if repository.putCalls != 0 {
		t.Fatalf("retired-only settings caused %d Central writes", repository.putCalls)
	}
	if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("retired-only settings remain: %v", err)
	}
}

func TestMergeLegacySettingsFillsOnlyMatchingSecretSlots(t *testing.T) {
	t.Parallel()
	current := desktopSettings{
		Network: &desktopNetworkSettings{
			Mode: "proxy", ProxyURLs: []string{"socks5://proxy.example.test:1080"},
			ProxyUsername: "user", SoxySessionTTL: "30m",
		},
		Hooks: []desktopHookSettings{{
			ID: "custom", Name: "Custom", Kind: eventhook.KindWebhook,
			URL: "https://example.test/hook", Enabled: true,
		}},
	}
	legacy := legacyDesktopSettings{
		Network: &desktopNetworkSettings{
			Mode: "proxy", ProxyURLs: []string{"socks5://proxy.example.test:1080"},
			ProxyUsername: "user", ProxyPassword: "password", SoxySessionTTL: "30m",
		},
		Hooks: []desktopHookSettings{{
			ID: "custom", Name: "Custom", Kind: eventhook.KindWebhook,
			URL: "https://example.test/hook", Secret: "secret", Enabled: true,
		}},
	}
	merged, err := mergeLegacyDesktopSettings(current, legacy)
	if err != nil || merged.Network.ProxyPassword != "password" || merged.Hooks[0].Secret != "secret" {
		t.Fatalf("mergeLegacyDesktopSettings() = %+v, %v", merged, err)
	}
	if current.Network.ProxyPassword != "" || current.Hooks[0].Secret != "" {
		t.Fatal("merge mutated the current Central settings")
	}
}

func TestMergeLegacySettingsRejectsDifferentExistingSecrets(t *testing.T) {
	t.Parallel()
	current := desktopSettings{Network: &desktopNetworkSettings{
		Mode: "soxy", SoxyURL: "https://soxy.example.test", SoxySessionTTL: "30m", SoxyAPIToken: "central",
	}}
	legacy := legacyDesktopSettings{Network: &desktopNetworkSettings{
		Mode: "soxy", SoxyURL: "https://soxy.example.test", SoxySessionTTL: "30m", SoxyAPIToken: "local",
	}}
	if _, err := mergeLegacyDesktopSettings(current, legacy); err == nil {
		t.Fatal("different Soxy credentials were silently discarded")
	}
	current = desktopSettings{Hooks: []desktopHookSettings{{
		ID: "custom", Name: "Custom", Kind: eventhook.KindWebhook,
		URL: "https://example.test/hook", Secret: "central", Enabled: true,
	}}}
	legacy = legacyDesktopSettings{Hooks: []desktopHookSettings{{
		ID: "custom", Name: "Custom", Kind: eventhook.KindWebhook,
		URL: "https://example.test/hook", Secret: "local", Enabled: true,
	}}}
	if _, err := mergeLegacyDesktopSettings(current, legacy); err == nil {
		t.Fatal("different hook credentials were silently discarded")
	}
}

func writeLegacySettings(t *testing.T, path string, settings legacyDesktopSettings) {
	t.Helper()
	contents, err := json.Marshal(settings)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, contents, 0o600); err != nil {
		t.Fatal(err)
	}
}
