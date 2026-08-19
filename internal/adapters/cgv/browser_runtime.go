package cgv

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/mxschmitt/playwright-go"
)

func startPlaywright() (*playwright.Playwright, error) {
	driverDirectory := strings.TrimSpace(os.Getenv("CINEKO_PLAYWRIGHT_DRIVER_PATH"))
	if driverDirectory == "" {
		return playwright.Run()
	}
	if err := validatePlaywrightDriver(driverDirectory); err != nil {
		return nil, err
	}
	return playwright.Run(&playwright.RunOptions{DriverDirectory: driverDirectory})
}

func resolveBrowserExecutable(pw *playwright.Playwright, configured string) (string, error) {
	configured = strings.TrimSpace(configured)
	if configured != "" {
		return validateBrowserExecutable(configured, "configured")
	}
	if pw == nil || pw.Chromium == nil {
		return "", errors.New("playwright Chromium is unavailable")
	}
	return validateBrowserExecutable(pw.Chromium.ExecutablePath(), "managed")
}

func validatePlaywrightDriver(driverDirectory string) error {
	node := "node"
	if runtime.GOOS == "windows" {
		node = "node.exe"
	}
	for _, required := range []string{
		filepath.Join(driverDirectory, node),
		filepath.Join(driverDirectory, "package", "cli.js"),
	} {
		if _, err := os.Stat(required); err != nil { // #nosec G703 -- Launcher provides and verifies this runtime root.
			return fmt.Errorf("launcher Playwright driver is incomplete at %q: %w", required, err)
		}
	}
	return nil
}

func validateBrowserExecutable(path, source string) (string, error) {
	path = filepath.Clean(strings.TrimSpace(path))
	if path == "." {
		return "", fmt.Errorf("%s Chromium executable path is empty", source)
	}
	info, err := os.Stat(path)
	if err != nil {
		return "", fmt.Errorf("%s Chromium executable %q: %w", source, path, err)
	}
	if !info.Mode().IsRegular() {
		return "", fmt.Errorf("%s Chromium executable %q is not a file", source, path)
	}
	return path, nil
}
