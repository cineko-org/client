//go:build darwin

package cgv

import (
	"context"
	"fmt"
	"os/exec"
	"strconv"
	"strings"
	"time"
)

const launchServicesAppInfo = "/usr/bin/lsappinfo"

func hideBrowserApplication(pid int) error {
	application := "#" + strconv.Itoa(pid)
	var lastErr error
	for attempt := 0; attempt < 10; attempt++ {
		if output, err := runLaunchServices("send", "hideRequest", "-asn", application); err != nil {
			lastErr = fmt.Errorf("send LaunchServices hide request: %w: %s", err, strings.TrimSpace(string(output)))
		} else if hidden, err := browserApplicationHidden(pid); err != nil {
			lastErr = err
		} else if hidden {
			return nil
		} else {
			lastErr = fmt.Errorf("LaunchServices did not hide Chrome pid %d", pid)
		}
		time.Sleep(50 * time.Millisecond)
	}
	return lastErr
}

func showBrowserApplication(pid int) error {
	application := "#" + strconv.Itoa(pid)
	if output, err := runLaunchServices("send", "showRequest", "-asn", application); err != nil {
		return fmt.Errorf("send LaunchServices show request: %w: %s", err, strings.TrimSpace(string(output)))
	}
	if output, err := runLaunchServices("setfront", application); err != nil {
		return fmt.Errorf("activate payment Chrome: %w: %s", err, strings.TrimSpace(string(output)))
	}
	return nil
}

func browserApplicationHidden(pid int) (bool, error) {
	application := "#" + strconv.Itoa(pid)
	output, err := runLaunchServices("info", application, "hidden")
	if err != nil {
		return false, fmt.Errorf("read LaunchServices hidden state: %w: %s", err, strings.TrimSpace(string(output)))
	}
	value := strings.ToLower(string(output))
	if strings.Contains(value, "=true") {
		return true, nil
	}
	if strings.Contains(value, "=false") {
		return false, nil
	}
	return false, fmt.Errorf("LaunchServices returned no hidden state for Chrome pid %d: %s", pid, strings.TrimSpace(string(output)))
}

func runLaunchServices(arguments ...string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	return exec.CommandContext(ctx, launchServicesAppInfo, arguments...).CombinedOutput() // #nosec G204 -- executable is a constant and callers supply only fixed verbs plus a numeric application ASN.
}
