package cgv

import (
	"encoding/json"
	"os"
	"testing"
)

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
