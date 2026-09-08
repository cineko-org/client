package cgv

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"slices"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestConcurrentBackgroundTabsFinishWithoutBlockingBrowserEvents(t *testing.T) {
	if os.Getenv("CINEKO_TEST_WINDOW_PRESENTATION") != "1" {
		t.Skip("requires the desktop window manager; uses only data URLs")
	}
	config := localBrowserTestConfig(t)
	config.Headless, config.StartMinimized = false, true
	root, err := NewAdapter(t.Context(), config)
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()
	type result struct {
		tab *Adapter
		err error
	}
	results := make(chan result, 6)
	started := time.Now()
	for index := range 6 {
		go func() {
			tab, err := root.OpenTab(t.Context())
			if err == nil {
				_, err = tab.page.Goto(fmt.Sprintf("data:text/html,<title>Local tab %d</title>", index))
			}
			results <- result{tab, err}
		}()
	}
	deadline := time.NewTimer(10 * time.Second)
	defer deadline.Stop()
	var tabs []*Adapter
	for range 6 {
		select {
		case result := <-results:
			if result.err != nil {
				_ = root.KillProcessTree()
				t.Fatal(result.err)
			}
			tabs = append(tabs, result.tab)
		case <-deadline.C:
			// Terminate only this test's isolated driver so a reproduced deadlock
			// cannot leave test Chrome processes behind.
			_ = root.KillProcessTree()
			t.Fatalf("only %d/6 tabs initialized within 10s", len(tabs))
		}
	}
	t.Logf("6/6 local tabs initialized and navigated in %s", time.Since(started))
	for _, tab := range tabs {
		tab.Close()
	}
	if count := root.ProcessPageCount(); count != 1 {
		t.Fatalf("closed tabs leaked: pages=%d", count)
	}
}

func TestStartMinimizedBrowserUsesNativeMinimizedLaunch(t *testing.T) {
	t.Parallel()
	config := DefaultBrowserConfig()
	config.Headless = false
	config.StartMinimized = true
	options := persistentContextOptions(config, "ko-KR")
	if !slices.Contains(options.Args, "--start-minimized") {
		t.Fatalf("start-minimized args = %v", options.Args)
	}
	if slices.Contains(options.Args, "--window-position=-32000,-32000") {
		t.Fatal("offscreen parking can expose an edge strip")
	}
	if options.Screen != nil || options.Viewport != nil || options.NoViewport == nil || !*options.NoViewport {
		t.Fatal("headed window must use the physical display work area")
	}
}

func TestPaymentWindowPlacementFitsSmallAndNegativeOriginMonitors(t *testing.T) {
	for _, area := range []windowRectangle{{0, 33, 1728, 1024}, {0, 25, 1366, 743}, {-1920, -1080, 1920, 1040}, {100, 50, 800, 600}} {
		placement, err := paymentWindowPlacement(area)
		if err != nil {
			t.Fatal(err)
		}
		var window browserWindowBounds
		window.Bounds.Left, window.Bounds.Top = placement.Left, placement.Top
		window.Bounds.Width, window.Bounds.Height = placement.Width, placement.Height
		window.Bounds.WindowState = "normal"
		if !paymentWindowFits(window, area) {
			t.Fatalf("placement=%+v, area=%+v", placement, area)
		}
		window.Bounds.Left = area.Left - 32000
		if paymentWindowFits(window, area) {
			t.Fatal("off-screen window passed verification")
		}
	}
}

func TestPaymentPresentationSerializesQueuedHideCallbacks(t *testing.T) {
	root := &Adapter{ctx: context.Background()}
	tab := &Adapter{owner: root}
	showStarted, releaseShow := make(chan struct{}), make(chan struct{})
	var waits sync.WaitGroup
	waits.Go(func() {
		if err := tab.changeWindowVisibility(true, func() error { close(showStarted); <-releaseShow; return nil }); err != nil {
			t.Error(err)
		}
	})
	<-showStarted
	var hides atomic.Int32
	for range 100 {
		waits.Go(func() {
			if err := root.changeWindowVisibility(false, func() error { hides.Add(1); return nil }); err != nil {
				t.Error(err)
			}
		})
	}
	close(releaseShow)
	waits.Wait()
	if hides.Load() != 0 {
		t.Fatalf("%d queued callbacks rehid the payment window", hides.Load())
	}
	if !root.paymentHandoff.Load() || !tab.paymentHandoff.Load() {
		t.Fatal("handoff did not reach both tab and root")
	}
}

func TestFailedPresentationDoesNotReenableBackgroundHiding(t *testing.T) {
	root := &Adapter{ctx: context.Background()}
	want := errors.New("window manager refused focus")
	if err := root.changeWindowVisibility(true, func() error { return want }); !errors.Is(err, want) {
		t.Fatal(err)
	}
	if err := root.changeWindowVisibility(false, func() error { t.Fatal("failed display re-enabled hiding"); return nil }); err != nil {
		t.Fatal(err)
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

func TestPaymentTabRemainsVisibleAfterLateBackgroundNavigation(t *testing.T) {
	if os.Getenv("CINEKO_TEST_WINDOW_PRESENTATION") != "1" {
		t.Skip("requires the desktop window manager")
	}
	config := localBrowserTestConfig(t)
	config.Headless, config.StartMinimized = false, true
	root, err := NewAdapter(t.Context(), config)
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()
	winner, err := root.OpenTab(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer winner.Close()
	if _, err := winner.page.Goto("data:text/html,<title>Cineko window test - no real booking</title><h1>Payment window test</h1>"); err != nil {
		t.Fatal(err)
	}
	if _, err := winner.page.Evaluate(`window.cinekoTestHold = 'H10,H11'`); err != nil {
		t.Fatal(err)
	}
	beforePID, err := root.browserApplicationPID()
	if err != nil {
		t.Fatal(err)
	}
	if err := winner.PresentPaymentWindow(); err != nil {
		t.Fatal(err)
	}
	// An old watcher can finish navigating after the winning tab is shown.
	if _, err := root.page.Goto("data:text/html,<title>Late background watcher</title>"); err != nil {
		t.Fatal(err)
	}
	for range 10 {
		if err := root.ensureBackgroundBrowserHidden(); err != nil {
			t.Fatal(err)
		}
	}
	if err := winner.wait(300 * time.Millisecond); err != nil {
		t.Fatal(err)
	}
	windowID, err := winner.browserWindowID()
	if err != nil {
		t.Fatal(err)
	}
	bounds, err := winner.readBrowserWindowBounds(windowID)
	if err != nil {
		t.Fatal(err)
	}
	area, err := winner.paymentWorkArea()
	if err != nil {
		t.Fatal(err)
	}
	visible, err := browserApplicationVisible(beforePID)
	if err != nil || !visible || !paymentWindowFits(bounds, area) {
		t.Fatalf("late watcher hid payment: visible=%t bounds=%+v err=%v", visible, bounds.Bounds, err)
	}
	afterPID, err := root.browserApplicationPID()
	if err != nil || afterPID != beforePID {
		t.Fatalf("browser was replaced: %d -> %d", beforePID, afterPID)
	}
	marker, err := winner.page.Evaluate(`window.cinekoTestHold`)
	if err != nil || marker != "H10,H11" {
		t.Fatalf("winning tab state was lost: %v/%v", marker, err)
	}
	t.Logf("same browser PID %d, winning page state retained, bounds=%+v, visible=%t after 10 late hides", afterPID, bounds.Bounds, visible)
}
