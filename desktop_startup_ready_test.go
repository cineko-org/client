package main

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestSignalDesktopStartupReadyPublishesPrivateAtomicMarker(t *testing.T) {
	dataDir := t.TempDir()
	nonce := "abcdefghijklmnopqrstuvwxyz012345"
	path, err := desktopStartupReadyPath(dataDir, nonce)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := signalDesktopStartupReady(dataDir, nonce); err != nil {
		t.Fatal(err)
	}
	contents, err := os.ReadFile(path)
	if err != nil || string(contents) != nonce+"\n" {
		t.Fatalf("startup marker = %q, %v", contents, err)
	}
	if info, err := os.Stat(path); err != nil || runtime.GOOS != "windows" && info.Mode().Perm() != 0o600 {
		t.Fatalf("startup marker permissions = %v, %v", info, err)
	}
	if err := signalDesktopStartupReady(dataDir, nonce); err == nil {
		t.Fatal("existing startup marker was overwritten")
	}
}

func TestSignalDesktopStartupReadyRejectsUntrustedPath(t *testing.T) {
	dataDir := t.TempDir()
	nonce := "abcdefghijklmnopqrstuvwxyz012345"
	path, err := desktopStartupReadyPath(dataDir, nonce)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(filepath.Dir(path)), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(t.TempDir(), filepath.Dir(path)); err != nil {
		if runtime.GOOS == "windows" {
			t.Skip("symlink creation requires elevated privileges on Windows")
		}
		t.Fatal(err)
	}
	if err := signalDesktopStartupReady(dataDir, nonce); err == nil {
		t.Fatal("symlink startup directory was accepted")
	}
}
