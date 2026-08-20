package webui

import (
	"context"
	"errors"
	"time"

	"github.com/cineko-org/client/internal/domain"
)

const paymentSessionTTL = 15 * time.Minute

type paymentSession struct {
	automation    Automation
	reservationID string
	userID        string
	timer         *time.Timer
}

type PaymentFailureNotifier interface {
	PaymentFailure() <-chan struct{}
}

// CanAcceptExecution prevents a Client from claiming work it cannot start.
// A prepared payment keeps the sole interactive browser session until the user
// finishes or abandons it, so Central must retain any later command meanwhile.
func (server *Server) CanAcceptExecution() bool {
	if server.bookingCapacityAvailable != nil {
		return server.bookingCapacityAvailable()
	}
	server.paymentMu.Lock()
	defer server.paymentMu.Unlock()
	return len(server.paymentSessions) == 0
}

func (server *Server) ExecutionAvailable() <-chan struct{} { return server.executionReady }

// NotifyBookingCapacityChanged wakes the execution worker after a local warm
// slot becomes ready or is retired. It does not claim or mutate Central work.
func (server *Server) NotifyBookingCapacityChanged() { server.signalExecutionAvailable() }

func (server *Server) retainPaymentSession(
	monitorID string,
	reservation domain.Reservation,
	automation Automation,
) {
	session := &paymentSession{
		automation: automation, reservationID: reservation.ID, userID: reservation.UserID,
	}
	server.paymentMu.Lock()
	if server.paymentSessions == nil {
		server.paymentSessions = make(map[string]*paymentSession)
	}
	previous := server.paymentSessions[monitorID]
	server.paymentSessions[monitorID] = session
	session.timer = time.AfterFunc(paymentSessionTTL, func() {
		server.expirePaymentSession(monitorID, session)
	})
	server.paymentMu.Unlock()
	closePaymentSession(previous)
	if notifier, ok := automation.(PaymentFailureNotifier); ok {
		go server.watchPaymentFailure(monitorID, session, notifier.PaymentFailure())
	}
}

func (server *Server) watchPaymentFailure(monitorID string, expected *paymentSession, failure <-chan struct{}) {
	if failure == nil {
		return
	}
	select {
	case <-failure:
	case <-server.lifetimeContext().Done():
		return
	}
	session := server.removePaymentSession(monitorID, expected)
	if session == nil {
		return
	}
	closePaymentSession(session)
	ctx := server.lifetimeContext()
	if err := server.finishPaymentAttempt(ctx, monitorID, session, "unknown", domain.MonitorPaymentUnknown); err != nil {
		server.recordMaintenanceFailure("payment-browser-crash:"+monitorID, err)
		return
	}
	server.addEvent(session.userID, "payment.browser_crashed", domain.EventError,
		"결제 화면 브라우저가 종료되어 예매 결과를 확인해야 합니다.")
}

func (server *Server) hasPaymentSession(monitorID string) bool {
	server.paymentMu.Lock()
	defer server.paymentMu.Unlock()
	return server.paymentSessions[monitorID] != nil
}

func (server *Server) abandonPaymentSession(ctx context.Context, monitorID string) (bool, error) {
	session := server.removePaymentSession(monitorID, nil)
	if session == nil {
		job, err := server.repository.GetMonitor(ctx, monitorID)
		if err != nil || job.Status != domain.MonitorTriggered && job.Status != domain.MonitorPaymentUnknown || job.ReservationID == "" {
			return false, err
		}
		session = &paymentSession{reservationID: job.ReservationID, userID: job.UserID}
	}
	closePaymentSession(session)
	return true, server.finishPaymentAttempt(ctx, monitorID, session, "abandoned", domain.MonitorPending)
}

func (server *Server) expirePaymentSession(monitorID string, expected *paymentSession) {
	session := server.removePaymentSession(monitorID, expected)
	if session == nil {
		return
	}
	closePaymentSession(session)
	ctx := server.rootContext
	if ctx == nil || ctx.Err() != nil {
		ctx = context.Background()
	}
	if err := server.finishPaymentAttempt(ctx, monitorID, session, "unknown", domain.MonitorPaymentUnknown); err != nil {
		server.recordMaintenanceFailure("payment-session:"+monitorID, err)
		return
	}
	server.addEvent(
		session.userID, "payment.expired", domain.EventWarning,
		"결제 대기 시간이 끝났습니다. CGV 예매 내역을 확인한 뒤 다시 실행하세요.",
	)
}

func (server *Server) finishPaymentAttempt(
	ctx context.Context,
	monitorID string,
	session *paymentSession,
	reservationStatus string,
	monitorStatus domain.MonitorStatus,
) error {
	var result error
	reservation, err := server.repository.GetReservation(ctx, session.reservationID)
	if err == nil && reservation.Status == "prepared" {
		reservation.Status = reservationStatus
		result = errors.Join(result, server.repository.PutReservation(ctx, reservation))
	} else if err != nil {
		result = errors.Join(result, err)
	}
	if monitorStatus == "" {
		return result
	}
	job, err := server.repository.GetMonitor(ctx, monitorID)
	if err != nil {
		return errors.Join(result, err)
	}
	if (job.Status == domain.MonitorTriggered || job.Status == domain.MonitorPaymentUnknown) &&
		job.ReservationID == session.reservationID {
		if monitorStatus == domain.MonitorPending {
			job.ReservationID = ""
		}
		job.LastError = ""
		job.Transition(monitorStatus, server.clock.Now())
		result = errors.Join(result, server.repository.PutMonitor(ctx, job))
	}
	return result
}

func (server *Server) removePaymentSession(monitorID string, expected *paymentSession) *paymentSession {
	server.paymentMu.Lock()
	session := server.paymentSessions[monitorID]
	if session == nil || expected != nil && session != expected {
		server.paymentMu.Unlock()
		return nil
	}
	delete(server.paymentSessions, monitorID)
	becameAvailable := len(server.paymentSessions) == 0
	server.paymentMu.Unlock()
	if becameAvailable {
		server.signalExecutionAvailable()
	}
	return session
}

func (server *Server) signalExecutionAvailable() {
	select {
	case server.executionReady <- struct{}{}:
	default:
	}
}

func (server *Server) closePaymentSessions() {
	server.paymentMu.Lock()
	type retainedSession struct {
		monitorID string
		session   *paymentSession
	}
	sessions := make([]retainedSession, 0, len(server.paymentSessions))
	for monitorID, session := range server.paymentSessions {
		sessions = append(sessions, retainedSession{monitorID: monitorID, session: session})
		delete(server.paymentSessions, monitorID)
	}
	server.paymentMu.Unlock()
	if len(sessions) > 0 {
		server.signalExecutionAvailable()
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	for _, retained := range sessions {
		closePaymentSession(retained.session)
		if err := server.finishPaymentAttempt(
			ctx, retained.monitorID, retained.session, "unknown", domain.MonitorPaymentUnknown,
		); err != nil {
			server.recordMaintenanceFailure("payment-shutdown:"+retained.monitorID, err)
		}
	}
}

func closePaymentSession(session *paymentSession) {
	if session == nil {
		return
	}
	if session.timer != nil {
		session.timer.Stop()
	}
	if session.automation != nil {
		session.automation.Close()
	}
}
