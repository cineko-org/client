package cgv

import (
	"encoding/json"
	"errors"
	"fmt"

	"github.com/cineko-org/client/internal/logging"
)

type browserWindowTarget struct {
	WindowID int `json:"windowId"`
}

// PresentPaymentWindow restores the off-screen headed browser and focuses the
// exact tab that successfully advanced to payment. A headless browser cannot
// be converted to headed at runtime, so warm booking browsers start headed and
// remain off-screen until this boundary succeeds.
func (adapter *Adapter) PresentPaymentWindow() error {
	if adapter == nil || adapter.page == nil || adapter.identitySession == nil {
		return errors.New("payment browser tab is unavailable")
	}
	result, err := adapter.identitySession.Send("Browser.getWindowForTarget", nil)
	if err != nil {
		return fmt.Errorf("resolve payment browser window: %w", err)
	}
	encoded, err := json.Marshal(result)
	if err != nil {
		return fmt.Errorf("encode payment browser window: %w", err)
	}
	var target browserWindowTarget
	if err := json.Unmarshal(encoded, &target); err != nil || target.WindowID <= 0 {
		return errors.Join(errors.New("payment browser window id is unavailable"), err)
	}
	if _, err := adapter.identitySession.Send("Browser.setWindowBounds", map[string]any{
		"windowId": target.WindowID,
		"bounds":   map[string]any{"windowState": "normal"},
	}); err != nil {
		return fmt.Errorf("restore payment browser window: %w", err)
	}
	if _, err := adapter.identitySession.Send("Browser.setWindowBounds", map[string]any{
		"windowId": target.WindowID,
		"bounds": map[string]any{
			"left": 80, "top": 80, "width": 1440, "height": 1000,
		},
	}); err != nil {
		return fmt.Errorf("position payment browser window: %w", err)
	}
	if err := adapter.page.BringToFront(); err != nil {
		return fmt.Errorf("focus payment browser tab: %w", err)
	}
	logging.Info(adapter.ctx, "cgv.booking.window.presented",
		"event", "cgv.booking.window.presented", "scenario", "seat_selection",
		"operation", "present_payment_window", "outcome", "succeeded",
		"window_id", target.WindowID)
	return nil
}
