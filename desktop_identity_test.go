package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestDesktopIdentityPersistsAcrossRestart(t *testing.T) {
	dataDir := t.TempDir()
	created, err := loadOrCreateDesktopIdentity(dataDir)
	if err != nil {
		t.Fatal(err)
	}
	loaded, err := loadOrCreateDesktopIdentity(dataDir)
	if err != nil {
		t.Fatal(err)
	}
	if created != loaded || created.InstallationID == "" || created.DeviceID == "" {
		t.Fatalf("desktop identities = %+v, %+v", created, loaded)
	}
	info, err := os.Stat(filepath.Join(dataDir, "installation.json"))
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("desktop identity mode = %v", info.Mode().Perm())
	}
}

func TestDesktopIdentityRejectsInvalidState(t *testing.T) {
	for name, contents := range map[string]string{
		"invalid-json":  `{`,
		"unknown-field": `{"installationId":"install","deviceId":"device","unknown":true}`,
		"incomplete":    `{"installationId":"install"}`,
	} {
		t.Run(name, func(t *testing.T) {
			dataDir := t.TempDir()
			if err := os.WriteFile(filepath.Join(dataDir, "installation.json"), []byte(contents), 0o600); err != nil {
				t.Fatal(err)
			}
			if _, err := loadOrCreateDesktopIdentity(dataDir); err == nil {
				t.Fatal("invalid desktop identity accepted")
			}
		})
	}
}
