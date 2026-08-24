package main

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestResolveDesktopDataDirUsesVisibleCinekoHome(t *testing.T) {
	home := filepath.Join(string(filepath.Separator), "Users", "cineko")
	got, err := resolveDesktopDataDir("", func() (string, error) { return home, nil })
	if err != nil {
		t.Fatal(err)
	}
	if want := filepath.Join(home, "cineko"); got != want {
		t.Fatalf("data dir = %q, want %q", got, want)
	}
}

func TestConfigureDesktopRuntimePathsKeepsRuntimeUnderDataDirectory(t *testing.T) {
	dataDir := filepath.Join(t.TempDir(), "cineko")
	t.Setenv("CINEKO_PLAYWRIGHT_DRIVER_PATH", "")
	t.Setenv("PLAYWRIGHT_DRIVER_PATH", "")
	t.Setenv("PLAYWRIGHT_BROWSERS_PATH", "")
	t.Setenv("TMPDIR", "outside")
	t.Setenv("TMP", "outside")
	t.Setenv("TEMP", "outside")
	if err := configureDesktopRuntimePaths(dataDir); err != nil {
		t.Fatal(err)
	}
	wantDriver := filepath.Join(dataDir, "runtime", "playwright", "driver")
	wantBrowsers := filepath.Join(dataDir, "runtime", "playwright", "browsers")
	wantTemporary := filepath.Join(dataDir, "tmp")
	if got := os.Getenv("PLAYWRIGHT_DRIVER_PATH"); got != wantDriver {
		t.Fatalf("PLAYWRIGHT_DRIVER_PATH = %q, want %q", got, wantDriver)
	}
	if got := os.Getenv("PLAYWRIGHT_BROWSERS_PATH"); got != wantBrowsers {
		t.Fatalf("PLAYWRIGHT_BROWSERS_PATH = %q, want %q", got, wantBrowsers)
	}
	for _, name := range []string{"TMPDIR", "TMP", "TEMP"} {
		if got := os.Getenv(name); got != wantTemporary {
			t.Fatalf("%s = %q, want %q", name, got, wantTemporary)
		}
	}
	for _, path := range []string{wantDriver, wantBrowsers, wantTemporary} {
		info, err := os.Stat(path)
		if err != nil || !info.IsDir() {
			t.Fatalf("runtime directory %q was not created: %v", path, err)
		}
	}
}

func TestConfigureDesktopRuntimePathsPreservesLauncherDriver(t *testing.T) {
	dataDir := filepath.Join(t.TempDir(), "cineko")
	t.Setenv("CINEKO_PLAYWRIGHT_DRIVER_PATH", filepath.Join(dataDir, "installed-driver"))
	installedBrowsers := filepath.Join(dataDir, "installed-browsers")
	t.Setenv("PLAYWRIGHT_DRIVER_PATH", "")
	t.Setenv("PLAYWRIGHT_BROWSERS_PATH", installedBrowsers)
	for _, name := range []string{"TMPDIR", "TMP", "TEMP"} {
		t.Setenv(name, os.Getenv(name))
	}
	if err := configureDesktopRuntimePaths(dataDir); err != nil {
		t.Fatal(err)
	}
	if got := os.Getenv("PLAYWRIGHT_DRIVER_PATH"); got != "" {
		t.Fatalf("PLAYWRIGHT_DRIVER_PATH = %q, want empty when Launcher driver is configured", got)
	}
	if got := os.Getenv("PLAYWRIGHT_BROWSERS_PATH"); got != installedBrowsers {
		t.Fatalf("PLAYWRIGHT_BROWSERS_PATH = %q, want Launcher path %q", got, installedBrowsers)
	}
}

func TestResolveDesktopDataDirRequiresAbsoluteOverride(t *testing.T) {
	if _, err := resolveDesktopDataDir("relative", func() (string, error) { return "", errors.New("unused") }); err == nil {
		t.Fatal("relative CINEKO_DATA_DIR was accepted")
	}
}
