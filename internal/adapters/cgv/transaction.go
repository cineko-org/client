package cgv

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/cineko-org/client/internal/domain"
)

var moneyPattern = regexp.MustCompile(`(?:총\s*결제금액|결제금액|총금액)\s*([\d,]+원)`)

func (adapter *Adapter) PreparePayment(
	ctx context.Context,
	showtime domain.Showtime,
	seatLabels []string,
) (domain.BookingDraft, error) {
	adapter.mu.Lock()
	defer adapter.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return domain.BookingDraft{}, err
	}
	if err := adapter.selectBookingSeats(seatLabels); err != nil {
		return domain.BookingDraft{}, err
	}
	if err := adapter.confirmSeatSelection(); err != nil {
		return domain.BookingDraft{}, err
	}
	if err := adapter.checkRequiredAgreements(); err != nil {
		return domain.BookingDraft{}, err
	}
	total, err := adapter.paymentTotal()
	if err != nil {
		return domain.BookingDraft{}, err
	}
	adapter.preparedPayment = true
	return domain.BookingDraft{
		Showtime: showtime, SeatLabels: append([]string(nil), seatLabels...),
		TotalPrice: total,
	}, nil
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
	clicked, err := adapter.clickButtonExact("선택")
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
	return "", nil
}

func (adapter *Adapter) PrepareCancellation(
	ctx context.Context,
	reservation domain.Reservation,
) (domain.CancellationDraft, error) {
	adapter.mu.Lock()
	defer adapter.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return domain.CancellationDraft{}, err
	}
	if err := adapter.navigate(homeURL); err != nil {
		return domain.CancellationDraft{}, err
	}
	clicked, err := adapter.clickButtonExact("티켓")
	if err != nil {
		return domain.CancellationDraft{}, err
	}
	if !clicked {
		return domain.CancellationDraft{}, fmt.Errorf("%w: ticket history button not found", ErrUIContractChanged)
	}
	if err := adapter.wait(800 * time.Millisecond); err != nil {
		return domain.CancellationDraft{}, err
	}
	clicked, err = adapter.openReservation(reservation.BookingNumber, reservation.Draft.Showtime.Movie)
	if err != nil {
		return domain.CancellationDraft{}, err
	}
	if !clicked {
		return domain.CancellationDraft{}, fmt.Errorf("reservation %s was not found", reservation.BookingNumber)
	}
	clicked, err = adapter.clickButtonMatching(`예매.*취소|예약.*취소`)
	if err != nil {
		return domain.CancellationDraft{}, err
	}
	if !clicked {
		return domain.CancellationDraft{}, fmt.Errorf("%w: cancellation button not found", ErrUIContractChanged)
	}
	if err := adapter.wait(500 * time.Millisecond); err != nil {
		return domain.CancellationDraft{}, err
	}
	body, err := adapter.bodyText()
	if err != nil {
		return domain.CancellationDraft{}, err
	}
	refund := ""
	if match := regexp.MustCompile(`(?:환불금액|취소금액)\s*([\d,]+원)`).FindStringSubmatch(normalize(body)); match != nil {
		refund = match[1]
	}
	adapter.preparedCancel = true
	return domain.CancellationDraft{
		ReservationID: reservation.ID, BookingNumber: reservation.BookingNumber,
		RefundAmount: refund,
	}, nil
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
