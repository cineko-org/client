package cgv

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestClassifyBookingDialog(t *testing.T) {
	tests := []struct {
		name   string
		dialog bookingDialog
		kind   string
		button string
		err    error
	}{
		{
			name: "four-side SCREENX notice",
			dialog: bookingDialog{Visible: true,
				Text:    "세계 최초 4면 상영관입니다. *4면 SCREENX는 영화명에 표기됩니다. 확인",
				Buttons: []string{"확인"}},
			kind: "screenx-four-side-notice", button: "확인",
		},
		{
			name: "payment confirmation",
			dialog: bookingDialog{Visible: true,
				Text:    "결제 전 확인해 주세요",
				Buttons: []string{"취소", "결제하기"}},
			kind: "payment-confirmation", button: "결제하기",
		},
		{
			name: "authentication",
			dialog: bookingDialog{Visible: true,
				Text: "CGV 회원 로그인이 필요한 서비스입니다.", Buttons: []string{"확인"}},
			kind: "authentication", err: ErrAuthenticationRequired,
		},
		{
			name: "captcha",
			dialog: bookingDialog{Visible: true,
				Text: "자동입력 방지 문자를 입력하세요.", Buttons: []string{"확인"}},
			kind: "captcha", err: ErrCaptchaRequired,
		},
		{
			name: "optional non-member choice",
			dialog: bookingDialog{Visible: true,
				Text: "예매 방식을 선택해 주세요.", Buttons: []string{"로그인", "비회원 예매"}},
			kind: "non-member-booking", button: "비회원 예매",
		},
		{
			name: "unknown is never confirmed",
			dialog: bookingDialog{Visible: true,
				Text: "새로운 공급자 메시지", Buttons: []string{"확인"}},
			kind: "unknown", err: ErrUIContractChanged,
		},
		{
			name: "known notice changed its action",
			dialog: bookingDialog{Visible: true,
				Text: "4면 SCREENX 안내", Buttons: []string{"닫기"}},
			kind: "screenx-four-side-notice", err: ErrUIContractChanged,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			action := classifyBookingDialog(test.dialog)
			if action.Kind != test.kind || action.Button != test.button {
				t.Fatalf("classifyBookingDialog() = %+v, want kind=%q button=%q", action, test.kind, test.button)
			}
			if test.err == nil && action.Err != nil {
				t.Fatalf("classifyBookingDialog() unexpected error = %v", action.Err)
			}
			if test.err != nil && !errors.Is(action.Err, test.err) {
				t.Fatalf("classifyBookingDialog() error = %v, want %v", action.Err, test.err)
			}
		})
	}
}

func TestWaitForSeatSelectionPageAllowsOptionalPopup(t *testing.T) {
	if testing.Short() {
		t.Skip("requires Playwright Chromium")
	}
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Content-Type", "text/html; charset=utf-8")
		popup := ""
		if request.URL.Query().Get("popup") == "1" {
			popup = `<div role="dialog" style="position:fixed;inset:20px">
				<p>세계 최초 4면 상영관입니다. *4면 SCREENX는 영화명에 표기됩니다.</p>
				<button id="confirm">확인</button>
			</div>
			<script>
				const confirm = document.getElementById('confirm');
				confirm['__reactProps$fixture'] = {onClick() {
					window.popupHandled = true;
					confirm.closest('[role="dialog"]').remove();
				}};
			</script>`
		}
		_, _ = fmt.Fprintf(writer, `<html><body>
			<button aria-label="새로고침">새로고침</button>
			<button>2</button>
			%s
		</body></html>`, popup)
	}))
	defer server.Close()

	adapter, err := NewAdapter(context.Background(), localBrowserTestConfig(t))
	if err != nil {
		t.Fatalf("NewAdapter() error = %v", err)
	}
	defer adapter.Close()

	for _, test := range []struct {
		name        string
		query       string
		wantHandled bool
	}{
		{name: "popup absent"},
		{name: "popup present", query: "?popup=1", wantHandled: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			if err := adapter.navigate(server.URL + "/cnm/selectVisitorCnt" + test.query); err != nil {
				t.Fatalf("navigate() error = %v", err)
			}
			started := time.Now()
			if err := adapter.waitForSeatSelectionPage(context.Background()); err != nil {
				t.Fatalf("waitForSeatSelectionPage() error = %v", err)
			}
			elapsed := time.Since(started)
			if elapsed >= time.Second {
				t.Fatalf("state-driven wait took %s, want under 1s", elapsed)
			}
			var handled bool
			if err := adapter.evaluate("window.popupHandled === true", &handled); err != nil {
				t.Fatalf("read popup result: %v", err)
			}
			if handled != test.wantHandled {
				t.Fatalf("popup handled = %t, want %t", handled, test.wantHandled)
			}
			t.Logf("optional popup=%t settled in %s", test.wantHandled, elapsed)
		})
	}
}
