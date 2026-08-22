package webui

import (
	"context"
	"errors"
	"time"

	clientpb "github.com/cineko-org/contracts/v3/gen/go/cineko/client"
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
	resource *clientpb.Resource,
	automation Automation,
) bool {
	reservation := resource.GetReservation()
	if reservation == nil {
		automation.Close()
		return false
	}
	if retention, ok := automation.(paymentRetention); ok {
		if err := retention.RetainPayment(); err != nil {
			automation.Close()
			server.recordMaintenanceFailure("payment-retention:"+monitorID, err)
			return false
		}
	}
	session := &paymentSession{
		automation: automation, reservationID: reservation.GetId(), userID: reservation.GetUserId(),
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
			if err := server.finishPaymentAttempt(ctx, monitorID, session, paymentUnknownState()); err != nil {
				server.recordMaintenanceFailure("payment-crash:"+monitorID, err)
				server.refreshBookingDemand(ctx)
				return
			}
			server.refreshBookingDemand(ctx)
			server.addEvent(appErrorEvent(session.userID, "payment.browser_failed",
				"결제 화면이 종료되었습니다. CGV 예매 내역을 확인한 뒤 다시 시도하세요."))
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
		monitor := job.GetMonitor()
		if err != nil || monitor == nil || monitor.GetState().GetTriggered() == nil && monitor.GetState().GetPaymentUnknown() == nil || monitor.GetReservationId() == "" {
			return false, err
		}
		session = &paymentSession{reservationID: monitor.GetReservationId(), userID: monitor.GetUserId()}
	}
	closePaymentSession(session)
	server.signalExecutionAvailable()
	err := server.finishPaymentAttempt(ctx, monitorID, session, pendingMonitorState())
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
	if err := server.finishPaymentAttempt(ctx, monitorID, session, paymentUnknownState()); err != nil {
		server.recordMaintenanceFailure("payment-session:"+monitorID, err)
		server.refreshBookingDemand(ctx)
		return
	}
	server.refreshBookingDemand(ctx)
	server.addEvent(appWarningEvent(
		session.userID, "payment.expired", "결제 대기 시간이 끝났습니다. CGV 예매 내역을 확인한 뒤 다시 실행하세요.",
	))
}

func (server *Server) finishPaymentAttempt(
	ctx context.Context,
	monitorID string,
	session *paymentSession,
	monitorState *clientpb.MonitorState,
) error {
	var result error
	if _, err := server.repository.GetReservation(ctx, session.reservationID); err != nil {
		result = errors.Join(result, err)
	}
	if monitorState == nil {
		return result
	}
	job, err := server.repository.GetMonitor(ctx, monitorID)
	if err != nil {
		return errors.Join(result, err)
	}
	monitor := job.GetMonitor()
	if monitor != nil && (monitor.GetState().GetTriggered() != nil || monitor.GetState().GetPaymentUnknown() != nil) &&
		monitor.GetReservationId() == session.reservationID {
		if monitorState.GetPending() != nil {
			monitor.SetReservationId("")
		}
		monitor.SetState(monitorState)
		monitor.SetUpdatedAt(timestamp(server.clock.Now()))
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
			ctx, retained.monitorID, retained.session, paymentUnknownState(),
		); err != nil {
			server.recordMaintenanceFailure("payment-shutdown:"+retained.monitorID, err)
		}
	}
	server.signalExecutionAvailable()
}

func pendingMonitorState() *clientpb.MonitorState {
	return clientpb.MonitorState_builder{Pending: clientpb.MonitorPending_builder{}.Build()}.Build()
}

func paymentUnknownState() *clientpb.MonitorState {
	return clientpb.MonitorState_builder{PaymentUnknown: clientpb.MonitorPaymentUnknown_builder{}.Build()}.Build()
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
