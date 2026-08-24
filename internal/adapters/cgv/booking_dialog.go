package cgv

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/cineko-org/client/internal/logging"
)

const (
	bookingPagePollInterval = 50 * time.Millisecond
	bookingPageReadyTimeout = 10 * time.Second
	maxBookingPopupActions  = 6
)

type bookingPageState struct {
	Ready  bool          `json:"ready"`
	Path   string        `json:"path"`
	Dialog bookingDialog `json:"dialog"`
}

type bookingDialog struct {
	Visible bool     `json:"visible"`
	Text    string   `json:"text"`
	Buttons []string `json:"buttons"`
}

type bookingDialogAction struct {
	Kind   string
	Button string
	Err    error
}

// waitForSeatSelectionPage treats the rendered seat page and a modal dialog as
// competing states. A showtime may open the seat page directly or may first
// show one or more provider notices, so waiting for either state avoids both a
// missing-popup timeout and a popup obscuring an otherwise rendered page.
func (adapter *Adapter) waitForSeatSelectionPage(ctx context.Context) error {
	if ctx == nil {
		ctx = adapter.ctx
	}
	started := time.Now()
	deadline := started.Add(bookingPageReadyTimeout)
	popupActions := 0
	popupAttempts := make(map[string]int)
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		state, err := adapter.inspectSeatSelectionPage()
		if err != nil {
			logging.Error(ctx, "cgv.booking.page.failed",
				"event", "cgv.booking.page.failed",
				"scenario", "seat_selection",
				"operation", "wait_for_seat_page",
				"outcome", "failed",
				"phase", "seat-selection-ready",
				"duration_ms", browserDurationMs(started),
				"error", err.Error(),
			)
			return err
		}
		if state.Dialog.Visible {
			action := classifyBookingDialog(state.Dialog)
			if action.Err != nil {
				logging.Error(ctx, "cgv.booking.popup.blocked",
					"event", "cgv.booking.popup.blocked",
					"scenario", "seat_selection",
					"operation", "handle_optional_popup",
					"outcome", "failed",
					"expected", "known optional booking popup or ready seat page",
					"observed", action.Kind,
					"route", state.Path,
					"text", normalize(state.Dialog.Text),
					"buttons", strings.Join(state.Dialog.Buttons, " | "),
					"error", action.Err.Error(),
				)
				return action.Err
			}
			fingerprint := action.Kind + "\x00" + normalize(state.Dialog.Text)
			popupAttempts[fingerprint]++
			popupActions++
			if popupAttempts[fingerprint] > 2 || popupActions > maxBookingPopupActions {
				err := fmt.Errorf("%w: booking popup repeated without progress: %s", ErrUIContractChanged, action.Kind)
				logging.Error(ctx, "cgv.booking.page.failed",
					"event", "cgv.booking.page.failed", "scenario", "seat_selection",
					"operation", "handle_optional_popup", "outcome", "failed",
					"phase", "seat-selection-ready",
					"route", state.Path,
					"popup_kind", action.Kind,
					"popup_actions", popupActions,
					"duration_ms", browserDurationMs(started),
					"error", err.Error(),
				)
				return err
			}
			logging.Info(ctx, "cgv.booking.popup.detected",
				"event", "cgv.booking.popup.detected", "scenario", "seat_selection",
				"operation", "handle_optional_popup", "outcome", "detected",
				"route", state.Path,
				"kind", action.Kind,
				"text", normalize(state.Dialog.Text),
				"button", action.Button,
			)
			handled, err := adapter.invokeCurrentDialogReactHandler(action.Button)
			if err != nil {
				return err
			}
			if !handled {
				err := fmt.Errorf("%w: current React handler for %s popup was not found", ErrUIContractChanged, action.Kind)
				logging.Error(ctx, "cgv.booking.page.failed",
					"event", "cgv.booking.page.failed", "scenario", "seat_selection",
					"operation", "handle_optional_popup", "outcome", "failed",
					"phase", "seat-selection-ready",
					"route", state.Path,
					"popup_kind", action.Kind,
					"duration_ms", browserDurationMs(started),
					"error", err.Error(),
				)
				return err
			}
			logging.Info(ctx, "cgv.booking.popup.handled",
				"event", "cgv.booking.popup.handled", "scenario", "seat_selection",
				"operation", "handle_optional_popup", "outcome", "succeeded",
				"route", state.Path,
				"kind", action.Kind,
				"button", action.Button,
			)
			// Handling a dialog causes a React render. Never reuse the old DOM,
			// props, Fiber, or store references on the next iteration.
			if err := waitForBookingPagePoll(ctx); err != nil {
				return err
			}
			continue
		}
		if state.Ready {
			logging.Info(ctx, "cgv.booking.page.ready",
				"event", "cgv.booking.page.ready", "scenario", "seat_selection",
				"operation", "wait_for_seat_page", "outcome", "succeeded",
				"phase", "seat-selection-ready",
				"route", state.Path,
				"popup_actions", popupActions,
				"duration_ms", browserDurationMs(started),
			)
			return nil
		}
		if !time.Now().Before(deadline) {
			err := fmt.Errorf("%w: timed out waiting for CGV seat selection page", ErrUIContractChanged)
			logging.Error(ctx, "cgv.booking.page.failed",
				"event", "cgv.booking.page.failed", "scenario", "seat_selection",
				"operation", "wait_for_seat_page", "outcome", "failed",
				"phase", "seat-selection-ready",
				"route", state.Path,
				"popup_actions", popupActions,
				"duration_ms", browserDurationMs(started),
				"error", err.Error(),
			)
			return err
		}
		if err := waitForBookingPagePoll(ctx); err != nil {
			return err
		}
	}
}

func waitForBookingPagePoll(ctx context.Context) error {
	timer := time.NewTimer(bookingPagePollInterval)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

func (adapter *Adapter) inspectSeatSelectionPage() (bookingPageState, error) {
	const expression = `(() => {
		const normalize = value => (value || '').replace(/\s+/g, ' ').trim();
		const queryAll = window.__cinekoQueryAll || ((selector, root = document) => Array.from(root.querySelectorAll(selector)));
		const visible = element => {
			if (!element) return false;
			const style = getComputedStyle(element);
			if (style.display === 'none' || style.visibility === 'hidden' || Number(style.opacity) === 0) return false;
			const rect = element.getBoundingClientRect();
			return rect.width > 0 && rect.height > 0;
		};
		const dialogs = queryAll('[role="dialog"],[aria-modal="true"],dialog')
			.filter(visible);
		const dialog = dialogs.length ? dialogs[dialogs.length - 1] : null;
		if (dialog) {
			return {
				ready: false,
				path: location.pathname,
				dialog: {
					visible: true,
					text: normalize(dialog.innerText || dialog.textContent),
					buttons: queryAll('button', dialog).filter(visible)
						.map(button => normalize(button.innerText || button.textContent)).filter(Boolean)
				}
			};
		}
		const buttons = queryAll('button').filter(visible);
		const hasRefresh = buttons.some(button => {
			const label = normalize(button.getAttribute('aria-label') || button.title || button.innerText || button.textContent);
			return !button.disabled && (label === '새로고침' || label === 'Refresh');
		});
		const hasPartySize = buttons.some(button => !button.disabled && /^[1-8]$/.test(normalize(button.innerText || button.textContent)));
		return {
			ready: location.pathname === '/cnm/selectVisitorCnt' && hasRefresh && hasPartySize,
			path: location.pathname,
			dialog: {visible: false, text: '', buttons: []}
		};
	})()`
	var state bookingPageState
	if err := adapter.evaluate(expression, &state); err != nil {
		return bookingPageState{}, fmt.Errorf("inspect CGV booking page: %w", err)
	}
	return state, nil
}

func classifyBookingDialog(dialog bookingDialog) bookingDialogAction {
	text := normalize(dialog.Text)
	buttons := make(map[string]struct{}, len(dialog.Buttons))
	for _, button := range dialog.Buttons {
		buttons[normalize(button)] = struct{}{}
	}
	hasButton := func(label string) bool {
		_, ok := buttons[label]
		return ok
	}
	switch {
	case strings.Contains(text, "CGV 회원 로그인이 필요한 서비스") || strings.Contains(text, "로그인이 필요"):
		return bookingDialogAction{Kind: "authentication", Err: ErrAuthenticationRequired}
	case strings.Contains(strings.ToLower(text), "captcha") || strings.Contains(text, "자동입력 방지"):
		return bookingDialogAction{Kind: "captcha", Err: ErrCaptchaRequired}
	case hasButton("비회원 예매"):
		return bookingDialogAction{Kind: "non-member-booking", Button: "비회원 예매"}
	case strings.Contains(text, "4면 SCREENX"):
		if hasButton("확인") {
			return bookingDialogAction{Kind: "screenx-four-side-notice", Button: "확인"}
		}
		return bookingDialogAction{Kind: "screenx-four-side-notice", Err: fmt.Errorf("%w: SCREENX notice has no confirmation action", ErrUIContractChanged)}
	case strings.Contains(text, "결제 전 확인해 주세요"):
		if hasButton("결제하기") {
			return bookingDialogAction{Kind: "payment-confirmation", Button: "결제하기"}
		}
		return bookingDialogAction{Kind: "payment-confirmation", Err: fmt.Errorf("%w: payment confirmation has no payment action", ErrUIContractChanged)}
	default:
		return bookingDialogAction{Kind: "unknown", Err: fmt.Errorf("%w: unrecognized booking popup", ErrUIContractChanged)}
	}
}

func (adapter *Adapter) invokeCurrentDialogReactHandler(label string) (bool, error) {
	expression := fmt.Sprintf(`(async () => {
		const expected = %s;
		const normalize = value => (value || '').replace(/\s+/g, ' ').trim();
		const queryAll = window.__cinekoQueryAll || ((selector, root = document) => Array.from(root.querySelectorAll(selector)));
		const visible = element => {
			if (!element) return false;
			const style = getComputedStyle(element);
			if (style.display === 'none' || style.visibility === 'hidden' || Number(style.opacity) === 0) return false;
			const rect = element.getBoundingClientRect();
			return rect.width > 0 && rect.height > 0;
		};
		const dialogs = queryAll('[role="dialog"],[aria-modal="true"],dialog').filter(visible);
		const dialog = dialogs.length ? dialogs[dialogs.length - 1] : null;
		if (!dialog) return false;
		const button = queryAll('button', dialog)
			.find(candidate => visible(candidate) && !candidate.disabled && normalize(candidate.innerText || candidate.textContent) === expected);
		if (!button) return false;
		const propsKey = Object.keys(button).find(key => key.startsWith('__reactProps$'));
		const handler = propsKey && button[propsKey] && button[propsKey].onClick;
		if (typeof handler !== 'function') return false;
		const event = {
			currentTarget: button,
			target: button,
			preventDefault() {},
			stopPropagation() {},
			nativeEvent: null
		};
		await Promise.resolve(handler(event));
		return true;
	})()`, jsString(label))
	var handled bool
	if err := adapter.evaluate(expression, &handled); err != nil {
		return false, fmt.Errorf("invoke current CGV popup React handler: %w", err)
	}
	return handled, nil
}
