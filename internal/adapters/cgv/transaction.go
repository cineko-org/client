package cgv

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/cineko-org/client/internal/logging"
	catalogpb "github.com/cineko-org/contracts/v3/gen/go/cineko/catalog"
	clientpb "github.com/cineko-org/contracts/v3/gen/go/cineko/client"
	"google.golang.org/protobuf/proto"
)

var moneyPattern = regexp.MustCompile(`(?:총\s*결제금액|결제금액|총금액)\s*([\d,]+원)`)

func stringPointer(value string) *string { return &value }

func (adapter *Adapter) PreparePayment(
	ctx context.Context,
	showtime *catalogpb.Showtime,
	seatLabels []string,
) (*clientpb.Reservation, error) {
	adapter.mu.Lock()
	defer adapter.mu.Unlock()
	started := time.Now()
	showtimeID := ""
	if showtime != nil {
		showtimeID = showtime.GetId()
	}
	fail := func(stage string, err error) (*clientpb.Reservation, error) {
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			return nil, err
		}
		if strings.Contains(strings.ToLower(err.Error()), "no longer selectable") {
			logging.Info(ctx, "cgv.booking.round.unavailable",
				"event", "cgv.booking.round.unavailable", "scenario", "seat_selection",
				"operation", stage, "outcome", "unavailable", "showtime_id", showtimeID,
				"seat_labels", strings.Join(seatLabels, ","), "duration_ms", browserDurationMs(started),
				"error", fmt.Sprintf("%+v", err))
			return nil, err
		}
		logging.ErrorUnexpected(ctx, "cgv.booking.prepare.failed", "seat_selection", stage,
			"selected seats advance to a prepared payment screen", "booking preparation stopped", err,
			"showtime_id", showtimeID, "seat_labels", strings.Join(seatLabels, ","),
			"duration_ms", browserDurationMs(started))
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if err := adapter.selectBookingSeats(seatLabels); err != nil {
		return fail("select_seats", err)
	}
	if err := adapter.confirmSeatSelection(); err != nil {
		return fail("confirm_seat_selection", err)
	}
	if err := adapter.checkRequiredAgreements(); err != nil {
		return fail("check_required_agreements", err)
	}
	total, err := adapter.paymentTotal()
	if err != nil {
		return fail("read_payment_total", err)
	}
	adapter.preparedPayment = true
	if err := adapter.PresentPaymentWindow(); err != nil {
		// The provider hold is already prepared. Keep the successful reservation
		// alive even if the desktop window manager refuses the focus request.
		logging.WarnUnexpected(ctx, "cgv.booking.window.present.failed", "seat_selection", "present_payment_window",
			"the winning payment tab is restored and focused", "payment tab stayed in the background",
			"showtime_id", showtimeID, "error", fmt.Sprintf("%+v", err))
	}
	logging.Info(ctx, "cgv.booking.prepare.completed",
		"event", "cgv.booking.prepare.completed", "scenario", "seat_selection",
		"operation", "prepare_payment", "outcome", "succeeded",
		"showtime_id", showtimeID, "seat_labels", strings.Join(seatLabels, ","),
		"total_price", total, "duration_ms", browserDurationMs(started))
	return clientpb.Reservation_builder{
		SeatLabels: append([]string(nil), seatLabels...), TotalPrice: &total,
		Showtime: proto.CloneOf(showtime),
	}.Build(), nil
}

func (adapter *Adapter) selectBookingSeats(seatLabels []string) error {
	for _, label := range seatLabels {
		clicked, err := adapter.clickSeat(label)
		if err != nil {
			return err
		}
		if !clicked {
			return fmt.Errorf("seat %s is no longer selectable", label)
		}
	}
	return nil
}

func (adapter *Adapter) confirmSeatSelection() error {
	clicked, err := adapter.clickButtonExact("선택완료")
	if err != nil {
		return err
	}
	if !clicked {
		return fmt.Errorf("%w: seat confirmation button not found", ErrUIContractChanged)
	}
	return adapter.wait(time.Second)
}

func (adapter *Adapter) paymentTotal() (string, error) {
	body, err := adapter.bodyText()
	if err != nil {
		return "", err
	}
	if match := moneyPattern.FindStringSubmatch(normalize(body)); match != nil {
		return match[1], nil
	}
	return "", fmt.Errorf("%w: payment total was not found", ErrUIContractChanged)
}

func (adapter *Adapter) PrepareCancellation(
	ctx context.Context,
	reservation *clientpb.Reservation,
) (*clientpb.WebUICancellationResult, error) {
	adapter.mu.Lock()
	defer adapter.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if err := adapter.navigate(homeURL); err != nil {
		return nil, err
	}
	clicked, err := adapter.clickButtonExact("티켓")
	if err != nil {
		return nil, err
	}
	if !clicked {
		return nil, fmt.Errorf("%w: ticket history button not found", ErrUIContractChanged)
	}
	if err := adapter.wait(800 * time.Millisecond); err != nil {
		return nil, err
	}
	showtime := reservation.GetShowtime()
	movieTitle := ""
	if showtime != nil && showtime.GetMovie() != nil {
		movieTitle = showtime.GetMovie().GetTitle()
	}
	clicked, err = adapter.openReservation(reservation.GetBookingNumber(), movieTitle)
	if err != nil {
		return nil, err
	}
	if !clicked {
		return nil, fmt.Errorf("reservation %s was not found", reservation.GetBookingNumber())
	}
	clicked, err = adapter.clickButtonMatching(`예매.*취소|예약.*취소`)
	if err != nil {
		return nil, err
	}
	if !clicked {
		return nil, fmt.Errorf("%w: cancellation button not found", ErrUIContractChanged)
	}
	if err := adapter.wait(500 * time.Millisecond); err != nil {
		return nil, err
	}
	body, err := adapter.bodyText()
	if err != nil {
		return nil, err
	}
	refund := ""
	if match := regexp.MustCompile(`(?:환불금액|취소금액)\s*([\d,]+원)`).FindStringSubmatch(normalize(body)); match != nil {
		refund = match[1]
	}
	adapter.preparedCancel = true
	return clientpb.WebUICancellationResult_builder{
		ReservationId: stringPointer(reservation.GetId()),
		BookingNumber: stringPointer(reservation.GetBookingNumber()),
		RefundAmount:  &refund,
	}.Build(), nil
}

func (adapter *Adapter) CommitCancellation(ctx context.Context) error {
	adapter.mu.Lock()
	defer adapter.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return err
	}
	if !adapter.preparedCancel {
		return errors.New("cancellation has not been prepared")
	}
	clicked, err := adapter.clickButtonMatching(`예매.*취소|예약.*취소|^확인$`)
	if err != nil {
		return err
	}
	if !clicked {
		return fmt.Errorf("%w: final cancellation button not found", ErrUIContractChanged)
	}
	if err := adapter.wait(time.Second); err != nil {
		return err
	}
	body, err := adapter.bodyText()
	if err != nil {
		return err
	}
	if !strings.Contains(body, "취소 완료") && !strings.Contains(body, "정상적으로 취소") {
		return fmt.Errorf("%w: cancellation completion was not confirmed", ErrUIContractChanged)
	}
	adapter.preparedCancel = false
	return nil
}

func (adapter *Adapter) clickSeat(label string) (bool, error) {
	expression := fmt.Sprintf(`(() => {
		const target = %s;
		const normalize = value => (value || '').replace(/\s+/g, ' ').trim();
		const pattern = new RegExp('(?:^|\\s)' + target.replace(/[.*+?^${}()|[\\]\\\\]/g, '\\$&') + '(?:\\s|$)', 'i');
		const element = window.__cinekoQueryAll('button,[role="button"],[data-seatlocno]')
			.find(item => !item.disabled && item.getAttribute('aria-disabled') !== 'true' && pattern.test(normalize(
				item.getAttribute('aria-label') || item.getAttribute('title') || item.getAttribute('data-seatlocno') || item.innerText || item.textContent
			)));
		if (!element) return false;
		element.click();
		return true;
	})()`, jsString(label))
	var clicked bool
	err := adapter.evaluate(expression, &clicked)
	return clicked, err
}

func (adapter *Adapter) checkRequiredAgreements() error {
	return adapter.evaluate(`(() => {
		const inputs = window.__cinekoQueryAll('input[type="checkbox"]')
			.filter(input => !input.disabled && !input.checked);
		for (const input of inputs) {
			const id = input.id;
			const label = id ? window.__cinekoQuery('label[for="' + CSS.escape(id) + '"]') : null;
			const text = ((label && label.innerText) || input.getAttribute('aria-label') || '').replace(/\s+/g, ' ').trim();
			if (/필수|전체.*동의/.test(text)) input.click();
		}
		return true;
	})()`, new(bool))
}

func (adapter *Adapter) openReservation(bookingNumber, movie string) (bool, error) {
	expression := fmt.Sprintf(`(() => {
		const bookingNumber = %s;
		const movie = %s;
		const normalize = value => (value || '').replace(/\s+/g, ' ').trim();
		const candidates = window.__cinekoQueryAll('article,li,[role="listitem"],button');
		const element = candidates.find(item => {
			const text = normalize(item.innerText || item.textContent);
			return (bookingNumber && text.includes(bookingNumber)) || (movie && text.includes(movie));
		});
		if (!element) return false;
		element.click();
		return true;
	})()`, jsString(bookingNumber), jsString(movie))
	var clicked bool
	err := adapter.evaluate(expression, &clicked)
	return clicked, err
}

func (adapter *Adapter) bodyText() (string, error) {
	var text string
	err := adapter.evaluate(`document.body ? document.body.innerText : ''`, &text)
	return text, err
}
