package cgv

import (
	"encoding/json"
	"os"
	"slices"
	"testing"
	"time"
)

func TestStartMinimizedBrowserUsesNativeMinimizedLaunch(t *testing.T) {
	t.Parallel()
	config := DefaultBrowserConfig()
	config.Headless = false
	config.StartMinimized = true
	options := persistentContextOptions(config, "ko-KR")
	if !slices.Contains(options.Args, "--start-minimized") {
		t.Fatalf("start-minimized args = %v", options.Args)
	}
}

func TestPresentPaymentWindowRestoresOffscreenBrowser(t *testing.T) {
	if os.Getenv("CINEKO_TEST_WINDOW_PRESENTATION") != "1" {
		t.Skip("set CINEKO_TEST_WINDOW_PRESENTATION=1 to exercise the desktop window manager")
	}
	config := localBrowserTestConfig(t)
	config.Headless = false
	config.StartMinimized = true
	adapter, err := NewAdapter(t.Context(), config)
	if err != nil {
		t.Fatalf("NewAdapter() error = %v", err)
	}
	defer adapter.Close()
	if err := adapter.PresentPaymentWindow(); err != nil {
		t.Fatalf("PresentPaymentWindow() error = %v", err)
	}
	targetValue, err := adapter.identitySession.Send("Browser.getWindowForTarget", nil)
	if err != nil {
		t.Fatal(err)
	}
	targetData, _ := json.Marshal(targetValue)
	var target browserWindowTarget
	if err := json.Unmarshal(targetData, &target); err != nil {
		t.Fatal(err)
	}
	boundsValue, err := adapter.identitySession.Send("Browser.getWindowBounds", map[string]any{"windowId": target.WindowID})
	if err != nil {
		t.Fatal(err)
	}
	boundsData, _ := json.Marshal(boundsValue)
	var result struct {
		Bounds struct {
			Left        int    `json:"left"`
			Top         int    `json:"top"`
			WindowState string `json:"windowState"`
		} `json:"bounds"`
	}
	if err := json.Unmarshal(boundsData, &result); err != nil {
		t.Fatal(err)
	}
	if result.Bounds.Left < 0 || result.Bounds.Top < 0 || result.Bounds.WindowState != "normal" {
		t.Fatalf("restored bounds = %+v", result.Bounds)
	}
}

func TestStartMinimizedBrowserStaysMinimizedUntilPayment(t *testing.T) {
	if os.Getenv("CINEKO_TEST_WINDOW_PRESENTATION") != "1" {
		t.Skip("set CINEKO_TEST_WINDOW_PRESENTATION=1 to exercise the desktop window manager")
	}
	config := localBrowserTestConfig(t)
	config.Headless = false
	config.StartMinimized = true
	adapter, err := NewAdapter(t.Context(), config)
	if err != nil {
		t.Fatalf("NewAdapter() error = %v", err)
	}
	defer adapter.Close()
	windowID, err := adapter.browserWindowID()
	if err != nil {
		t.Fatal(err)
	}
	boundsValue, err := adapter.identitySession.Send("Browser.getWindowBounds", map[string]any{"windowId": windowID})
	if err != nil {
		t.Fatal(err)
	}
	boundsData, _ := json.Marshal(boundsValue)
	var result struct {
		Bounds struct {
			WindowState string `json:"windowState"`
		} `json:"bounds"`
	}
	if err := json.Unmarshal(boundsData, &result); err != nil {
		t.Fatal(err)
	}
	if result.Bounds.WindowState != "minimized" {
		t.Fatalf("initial window state = %q, want minimized", result.Bounds.WindowState)
	}
	pid, err := adapter.browserApplicationPID()
	if err != nil {
		t.Fatal(err)
	}
	hidden, err := browserApplicationHidden(pid)
	if err != nil {
		t.Fatal(err)
	}
	if !hidden {
		t.Fatalf("Chrome pid %d is not hidden after startup", pid)
	}
	tab, err := adapter.OpenTab(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer tab.Close()
	boundsValue, err = adapter.identitySession.Send("Browser.getWindowBounds", map[string]any{"windowId": windowID})
	if err != nil {
		t.Fatal(err)
	}
	boundsData, _ = json.Marshal(boundsValue)
	if err := json.Unmarshal(boundsData, &result); err != nil {
		t.Fatal(err)
	}
	if result.Bounds.WindowState != "minimized" {
		t.Fatalf("window state after opening a tab = %q, want minimized", result.Bounds.WindowState)
	}
	hidden, err = browserApplicationHidden(pid)
	if err != nil {
		t.Fatal(err)
	}
	if !hidden {
		t.Fatalf("Chrome pid %d became visible after opening a tab", pid)
	}
	if _, err := tab.page.Goto("data:text/html,<title>navigation visibility test</title>"); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(2 * time.Second)
	for {
		hidden, err = browserApplicationHidden(pid)
		if err != nil {
			t.Fatal(err)
		}
		if hidden {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("Chrome pid %d became visible after navigation", pid)
		}
		time.Sleep(25 * time.Millisecond)
	}
}
