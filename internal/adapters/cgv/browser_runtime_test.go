package cgv

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestLauncherPlaywrightDriverMustBeComplete(t *testing.T) {
	t.Parallel()
	driver := t.TempDir()
	if err := validatePlaywrightDriver(driver); err == nil {
		t.Fatal("validatePlaywrightDriver(incomplete) error = nil")
	}
	node := "node"
	if runtime.GOOS == "windows" {
		node = "node.exe"
	}
	if err := os.WriteFile(filepath.Join(driver, node), []byte("node"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(driver, "package"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(driver, "package", "cli.js"), []byte("driver"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := validatePlaywrightDriver(driver); err != nil {
		t.Fatalf("validatePlaywrightDriver() error = %v", err)
	}
}

func TestConfiguredBrowserExecutableMustExist(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "chromium")
	if _, err := resolveBrowserExecutable(nil, path); err == nil {
		t.Fatal("resolveBrowserExecutable(missing) error = nil")
	}
	if err := os.WriteFile(path, []byte("browser"), 0o600); err != nil {
		t.Fatal(err)
	}
	resolved, err := resolveBrowserExecutable(nil, path)
	if err != nil || resolved != path {
		t.Fatalf("resolveBrowserExecutable() = %q, %v", resolved, err)
	}
}
