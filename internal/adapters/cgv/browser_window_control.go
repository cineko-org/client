package cgv

import (
	"context"
	"crypto/rand"
	"embed"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/mxschmitt/playwright-go"
)

//go:embed window_control/manifest.json window_control/background.js
var windowControlFiles embed.FS

func usesWindowControl(config BrowserConfig) bool { return config.StartMinimized && !config.Headless }

func windowControlPath(config BrowserConfig) string {
	return filepath.Join(config.ProfileDir, "cineko-window-control")
}

// These are runtime resources in the owned browser profile, not OS temp files.
func prepareWindowControl(config BrowserConfig) error {
	if !usesWindowControl(config) {
		return nil
	}
	directory := windowControlPath(config)
	if err := os.MkdirAll(directory, 0o700); err != nil {
		return err
	}
	for _, name := range []string{"manifest.json", "background.js"} {
		data, err := windowControlFiles.ReadFile("window_control/" + name)
		if err != nil {
			return err
		}
		if err := os.WriteFile(filepath.Join(directory, name), data, 0o600); err != nil {
			return err
		}
	}
	return nil
}

type bookingWindowControl struct {
	worker   playwright.Worker
	windowID int
}

func (adapter *Adapter) initializeWindowControl() error {
	ctx, cancel := context.WithTimeout(adapter.ctx, 10*time.Second)
	defer cancel()
	for {
		for _, worker := range adapter.browserContext.ServiceWorkers() {
			if !strings.HasPrefix(worker.URL(), "chrome-extension://") || !strings.HasSuffix(worker.URL(), "/background.js") {
				continue
			}
			value, err := worker.Evaluate(`async () => {
				if (chrome.runtime.getManifest().name !== 'Cineko Browser Control') throw new Error('Unexpected window controller');
				return cinekoWindowControl.initialize();
			}`)
			if err != nil {
				return fmt.Errorf("initialize booking window controller: %w", err)
			}
			id, ok := value.(int)
			if !ok || id <= 0 {
				return errors.New("window controller returned an invalid window ID")
			}
			adapter.windowControl = &bookingWindowControl{worker: worker, windowID: id}
			return nil
		}
		timer := time.NewTimer(20 * time.Millisecond)
		select {
		case <-ctx.Done():
			timer.Stop()
			return fmt.Errorf("wait for booking window controller: %w", ctx.Err())
		case <-timer.C:
		}
	}
}

func (adapter *Adapter) newBookingPage(ctx context.Context) (playwright.Page, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if !adapter.hideUntilPayment {
		return adapter.browserContext.NewPage()
	}
	control := adapter.windowControl
	if control == nil {
		return nil, errors.New("background booking window controller is unavailable")
	}
	// Chrome canonicalizes about:blank fragments to lowercase when opened via
	// tabs.create. Generate the marker in that form before matching the page.
	marker := "about:blank#cineko-tab-" + strings.ToLower(rand.Text())
	tabID, err := control.worker.Evaluate("request => cinekoWindowControl.createTab(request)", map[string]any{
		"windowId": control.windowID, "marker": marker,
	})
	if err != nil {
		return nil, fmt.Errorf("create inactive booking tab: %w", err)
	}
	// The Page event can precede its initial fragment navigation. A one-shot
	// URL predicate drops that page permanently. Wait for the exact marker in
	// the context instead; never adopt an unrelated popup or create a fallback.
	wait, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()
	for {
		if wait.Err() == nil {
			for _, page := range adapter.browserContext.Pages() {
				if page.URL() == marker {
					return page, nil
				}
			}
		}
		select {
		case <-wait.Done():
			_, _ = control.worker.Evaluate("id => cinekoWindowControl.closeTab(id)", tabID)
			return nil, fmt.Errorf("adopt inactive booking tab: %w", wait.Err())
		case <-ticker.C:
		}
	}
}
