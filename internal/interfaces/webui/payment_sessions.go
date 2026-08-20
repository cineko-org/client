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

type paymentRetention interface {
	RetainPayment() error
}

type paymentFailureNotifier interface {
	PaymentFailure() <-chan struct{}
}

func (server *Server) retainPaymentSession(
	monitorID string,
	reservation domain.Reservation,
	automation Automation,
) bool {
	if retention, ok := automation.(paymentRetention); ok {
		if err := retention.RetainPayment(); err != nil {
			automation.Close()
			server.recordMaintenanceFailure("payment-retention:"+monitorID, err)
			return false
		}
	}
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
	server.watchPaymentFailure(monitorID, session)
	return true
}

func (server *Server) watchPaymentFailure(monitorID string, expected *paymentSession) {
	notifier, ok := expected.automation.(paymentFailureNotifier)
	if !ok {
		return
	}
	failure := notifier.PaymentFailure()
	if failure == nil {
		return
	}
	go func() {
		select {
		case <-failure:
			session := server.removePaymentSession(monitorID, expected)
			if session == nil {
				return
			}
			closePaymentSession(session)
			ctx := server.lifetimeContext()
			if err := server.finishPaymentAttempt(ctx, monitorID, session, "unknown", domain.MonitorPaymentUnknown); err != nil {
				server.recordMaintenanceFailure("payment-crash:"+monitorID, err)
				server.refreshBookingDemand(ctx)
				return
			}
			server.refreshBookingDemand(ctx)
			server.addEvent(session.userID, "payment.browser_failed", domain.EventError,
				"결제 화면이 종료되었습니다. CGV 예매 내역을 확인한 뒤 다시 시도하세요.")
		case <-server.lifetimeContext().Done():
		}
	}()
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
	server.signalExecutionAvailable()
	err := server.finishPaymentAttempt(ctx, monitorID, session, "abandoned", domain.MonitorPending)
	server.refreshBookingDemand(ctx)
	return true, err
}

func (server *Server) expirePaymentSession(monitorID string, expected *paymentSession) {
	session := server.removePaymentSession(monitorID, expected)
	if session == nil {
		return
	}
	closePaymentSession(session)
	server.signalExecutionAvailable()
	ctx := server.rootContext
	if ctx == nil || ctx.Err() != nil {
		ctx = context.Background()
	}
	if err := server.finishPaymentAttempt(ctx, monitorID, session, "unknown", domain.MonitorPaymentUnknown); err != nil {
		server.recordMaintenanceFailure("payment-session:"+monitorID, err)
		server.refreshBookingDemand(ctx)
		return
	}
	server.refreshBookingDemand(ctx)
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
	defer server.paymentMu.Unlock()
	session := server.paymentSessions[monitorID]
	if session == nil || expected != nil && session != expected {
		return nil
	}
	delete(server.paymentSessions, monitorID)
	server.signalExecutionAvailable()
	return session
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
	server.signalExecutionAvailable()
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
