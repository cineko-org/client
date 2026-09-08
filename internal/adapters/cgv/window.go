package cgv

import (
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/cineko-org/client/internal/logging"
)

type browserWindowTarget struct {
	WindowID int `json:"windowId"`
}

type browserWindowBounds struct {
	Bounds struct {
		Left        int    `json:"left"`
		Top         int    `json:"top"`
		Width       int    `json:"width"`
		Height      int    `json:"height"`
		WindowState string `json:"windowState"`
	} `json:"bounds"`
}

func (adapter *Adapter) rootAdapter() *Adapter {
	if adapter == nil {
		return nil
	}
	for adapter.owner != nil {
		adapter = adapter.owner
	}
	return adapter
}

// browserApplicationPID resolves Chrome itself. processPID intentionally
// remains the Playwright driver PID for the existing lifecycle/reaping code.
func (adapter *Adapter) browserApplicationPID() (int, error) {
	root := adapter.rootAdapter()
	if root == nil || root.browserContext == nil || root.browserContext.Browser() == nil {
		return 0, errors.New("browser context is unavailable")
	}
	if root.browserProcessPID > 0 {
		return root.browserProcessPID, nil
	}
	session, err := root.browserContext.Browser().NewBrowserCDPSession()
	if err != nil {
		return 0, fmt.Errorf("open browser CDP session: %w", err)
	}
	defer func() { _ = session.Detach() }()
	value, err := session.Send("SystemInfo.getProcessInfo", nil)
	if err != nil {
		return 0, fmt.Errorf("resolve Chrome process: %w", err)
	}
	encoded, err := json.Marshal(value)
	if err != nil {
		return 0, fmt.Errorf("encode Chrome process list: %w", err)
	}
	var processes struct {
		ProcessInfo []struct {
			Type string `json:"type"`
			ID   int    `json:"id"`
		} `json:"processInfo"`
	}
	if err := json.Unmarshal(encoded, &processes); err != nil {
		return 0, fmt.Errorf("decode Chrome process list: %w", err)
	}
	for _, process := range processes.ProcessInfo {
		if process.Type == "browser" && process.ID > 0 {
			root.browserProcessPID = process.ID
			return process.ID, nil
		}
	}
	return 0, errors.New("chrome browser process is unavailable")
}

func (adapter *Adapter) browserWindowID() (int, error) {
	if adapter == nil || adapter.page == nil || adapter.identitySession == nil {
		return 0, errors.New("browser tab is unavailable")
	}
	result, err := adapter.identitySession.Send("Browser.getWindowForTarget", nil)
	if err != nil {
		return 0, fmt.Errorf("resolve browser window: %w", err)
	}
	encoded, err := json.Marshal(result)
	if err != nil {
		return 0, fmt.Errorf("encode browser window: %w", err)
	}
	var target browserWindowTarget
	if err := json.Unmarshal(encoded, &target); err != nil || target.WindowID <= 0 {
		return 0, errors.Join(errors.New("browser window id is unavailable"), err)
	}
	return target.WindowID, nil
}

// minimizeBrowserWindow keeps the headed, authenticated warm browser out of
// the user's workspace while monitoring. The same live window is restored
// only after seats have advanced to the payment boundary.
func (adapter *Adapter) minimizeBrowserWindow() error {
	return adapter.hideBookingWindow(true)
}

// This single lock covers CDP changes AND native application hide/show.
// Recheck handoff after locking so already-queued navigation callbacks cannot
// hide the winning payment tab after presentation completes.
func (adapter *Adapter) changeWindowVisibility(present bool, action func() error) error {
	root := adapter.rootAdapter()
	if root == nil {
		return errors.New("browser adapter is unavailable")
	}
	root.windowVisibilityMu.Lock()
	defer root.windowVisibilityMu.Unlock()
	if !present && (root.paymentHandoff.Load() || root.closing.Load()) {
		return nil
	}
	if present {
		root.paymentHandoff.Store(true)
		adapter.paymentHandoff.Store(true)
	}
	return action()
}

func (adapter *Adapter) hideBookingWindow(report bool) error {
	return adapter.changeWindowVisibility(false, func() error {
		windowID, err := adapter.parkBrowserWindowLocked()
		if err != nil {
			return err
		}
		pid, err := adapter.browserApplicationPID()
		if err != nil {
			return err
		}
		if err := hideBrowserApplication(pid); err != nil {
			return fmt.Errorf("hide background Chrome application: %w", err)
		}
		if report {
			logging.Info(adapter.ctx, "cgv.booking.window.minimized",
				"event", "cgv.booking.window.minimized", "scenario", "booking_monitoring",
				"operation", "hide_booking_window", "outcome", "succeeded",
				"window_id", windowID, "browser_pid", pid)
		}
		return nil
	})
}

// Caller holds the root visibility lock. Never park off-screen: macOS may
// clamp those coordinates and leave a visible strip at the display edge.
func (adapter *Adapter) parkBrowserWindowLocked() (int, error) {
	windowID, err := adapter.browserWindowID()
	if err != nil {
		return 0, err
	}
	current, err := adapter.readBrowserWindowBounds(windowID)
	if err != nil {
		return 0, err
	}
	if current.Bounds.WindowState == "minimized" {
		return windowID, nil
	}
	var lastBounds browserWindowBounds
	for attempt := 0; attempt < 10; attempt++ {
		if _, err := adapter.identitySession.Send("Browser.setWindowBounds", map[string]any{
			"windowId": windowID,
			"bounds":   map[string]any{"windowState": "minimized"},
		}); err != nil {
			return 0, fmt.Errorf("minimize browser window: %w", err)
		}
		if err := adapter.wait(50 * time.Millisecond); err != nil {
			return 0, err
		}
		lastBounds, err = adapter.readBrowserWindowBounds(windowID)
		if err != nil {
			return 0, err
		}
		if lastBounds.Bounds.WindowState == "minimized" {
			return windowID, nil
		}
	}
	return 0, fmt.Errorf("browser window did not stay minimized: state=%q left=%d top=%d",
		lastBounds.Bounds.WindowState, lastBounds.Bounds.Left, lastBounds.Bounds.Top)
}

func (adapter *Adapter) readBrowserWindowBounds(windowID int) (browserWindowBounds, error) {
	value, err := adapter.identitySession.Send("Browser.getWindowBounds", map[string]any{"windowId": windowID})
	if err != nil {
		return browserWindowBounds{}, fmt.Errorf("verify browser window bounds: %w", err)
	}
	encoded, err := json.Marshal(value)
	if err != nil {
		return browserWindowBounds{}, fmt.Errorf("encode browser window bounds: %w", err)
	}
	var bounds browserWindowBounds
	if err := json.Unmarshal(encoded, &bounds); err != nil {
		return browserWindowBounds{}, fmt.Errorf("decode browser window bounds: %w", err)
	}
	return bounds, nil
}

// ensureBackgroundBrowserHidden closes the race where creating a new page can
// make Chrome active again on macOS. Once payment is prepared, the winning
// browser is deliberately left visible.
func (adapter *Adapter) ensureBackgroundBrowserHidden() error {
	root := adapter.rootAdapter()
	if root == nil || !root.hideUntilPayment || root.paymentHandoff.Load() {
		return nil
	}
	return root.hideBookingWindow(false)
}

// PresentPaymentWindow restores the minimized headed browser and focuses the
// exact tab that successfully advanced to payment. A headless browser cannot
// be converted to headed at runtime, so warm booking browsers stay minimized
// until this boundary succeeds.
func (adapter *Adapter) PresentPaymentWindow() error {
	return adapter.changeWindowVisibility(true, func() error {
		err := adapter.presentPaymentWindowLocked()
		adapter.rootAdapter().windowPresentationErr = err
		return err
	})
}

func (adapter *Adapter) PaymentPresentationError() error {
	root := adapter.rootAdapter()
	if root == nil {
		return nil
	}
	root.windowVisibilityMu.Lock()
	defer root.windowVisibilityMu.Unlock()
	return root.windowPresentationErr
}

func (adapter *Adapter) presentPaymentWindowLocked() error {
	windowID, err := adapter.browserWindowID()
	if err != nil {
		return fmt.Errorf("resolve payment browser window: %w", err)
	}
	area, err := adapter.paymentWorkArea()
	if err != nil {
		return err
	}
	placement, err := paymentWindowPlacement(area)
	if err != nil {
		return err
	}
	pid, err := adapter.browserApplicationPID()
	if err != nil {
		return err
	}
	var lastErr error
	for attempt := 0; attempt < 3; attempt++ {
		if err := adapter.restorePaymentWindow(windowID, pid, placement); err != nil {
			return err
		}
		// Native restore is asynchronous. Require stable on-screen bounds and
		// an unhidden application before reporting presentation as successful.
		stable := 0
		for poll := 0; poll < 10; poll++ {
			if err := adapter.wait(75 * time.Millisecond); err != nil {
				return err
			}
			bounds, err := adapter.readBrowserWindowBounds(windowID)
			if err != nil {
				lastErr = err
				continue
			}
			visible, err := browserApplicationVisible(pid)
			if err != nil {
				lastErr = err
				continue
			}
			if visible && paymentWindowFits(bounds, area) {
				stable++
				if stable < 2 {
					continue
				}
				logging.Info(adapter.ctx, "cgv.booking.window.presented",
					"event", "cgv.booking.window.presented", "scenario", "seat_selection",
					"operation", "present_payment_window", "outcome", "succeeded",
					"window_id", windowID, "browser_pid", pid, "bounds", bounds.Bounds, "work_area", area, "verified", true)
				return nil
			}
			stable = 0
			lastErr = fmt.Errorf("payment window not visible in work area: visible=%t bounds=%+v area=%+v", visible, bounds.Bounds, area)
		}
	}
	return fmt.Errorf("verify payment window presentation: %w", lastErr)
}

func (adapter *Adapter) restorePaymentWindow(windowID, pid int, placement windowRectangle) error {
	if _, err := adapter.identitySession.Send("Browser.setWindowBounds", map[string]any{
		"windowId": windowID, "bounds": map[string]any{"windowState": "normal"},
	}); err != nil {
		return fmt.Errorf("restore payment browser window: %w", err)
	}
	if _, err := adapter.identitySession.Send("Browser.setWindowBounds", map[string]any{
		"windowId": windowID, "bounds": map[string]any{
			"left": placement.Left, "top": placement.Top, "width": placement.Width, "height": placement.Height,
		},
	}); err != nil {
		return fmt.Errorf("position payment browser window: %w", err)
	}
	if err := adapter.page.BringToFront(); err != nil {
		return fmt.Errorf("focus payment browser tab: %w", err)
	}
	if err := showBrowserApplication(pid); err != nil {
		return fmt.Errorf("show payment Chrome application: %w", err)
	}
	return nil
}

type windowRectangle struct {
	Left   int `json:"left"`
	Top    int `json:"top"`
	Width  int `json:"width"`
	Height int `json:"height"`
}

func (adapter *Adapter) paymentWorkArea() (windowRectangle, error) {
	var area windowRectangle
	err := adapter.evaluate(`(() => ({left: screen.availLeft ?? 0, top: screen.availTop ?? 0,
		width: screen.availWidth, height: screen.availHeight}))()`, &area)
	if err != nil {
		return area, fmt.Errorf("read payment monitor work area: %w", err)
	}
	return area, nil
}

func paymentWindowPlacement(area windowRectangle) (windowRectangle, error) {
	if area.Width < 320 || area.Height < 240 {
		return windowRectangle{}, fmt.Errorf("invalid monitor work area: %+v", area)
	}
	margin := min(24, min(area.Width, area.Height)/20)
	width, height := min(1440, area.Width-2*margin), min(1000, area.Height-2*margin)
	return windowRectangle{Left: area.Left + (area.Width-width)/2, Top: area.Top + (area.Height-height)/2, Width: width, Height: height}, nil
}

func paymentWindowFits(window browserWindowBounds, area windowRectangle) bool {
	b := window.Bounds
	return (b.WindowState == "normal" || b.WindowState == "maximized") &&
		b.Width >= 300 && b.Height >= 200 &&
		b.Left >= area.Left && b.Top >= area.Top &&
		b.Left+b.Width <= area.Left+area.Width && b.Top+b.Height <= area.Top+area.Height
}
