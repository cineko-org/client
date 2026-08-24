package webui

import (
	"context"
	"fmt"
	"net/http"

	"github.com/cineko-org/client/internal/application"
	clientpb "github.com/cineko-org/contracts/v3/gen/go/cineko/client"
	"google.golang.org/protobuf/proto"
)

func (server *Server) retryMonitor(writer http.ResponseWriter, request *http.Request) {
	input := &clientpb.WebUIMonitorRetryRequest{}
	if !decodeProtoJSON(server, writer, request, input) {
		return
	}
	server.runOwnedTask(writer, request, input, func() error { return server.startMonitorRetry(input) })
}

func (server *Server) stopMonitor(writer http.ResponseWriter, request *http.Request) {
	input := &clientpb.WebUIMonitorRetryRequest{}
	if !decodeProtoJSON(server, writer, request, input) {
		return
	}
	monitor := input.GetMonitor()
	_, err := application.NewMonitorService(
		server.repository, server.repository, server.ids, server.clock,
	).SetEnabled(request.Context(), monitor.GetUserId(), monitor.GetId(), false)
	if err != nil {
		server.writeError(writer, err)
		return
	}
	server.refreshBookingDemand(request.Context())
	writeProtoJSON(writer, http.StatusOK, actionStatus(false))
}

func (server *Server) startMonitorRetry(input *clientpb.WebUIMonitorRetryRequest) error {
	monitor := input.GetMonitor()
	ctx := server.lifetimeContext()
	job, err := server.repository.GetMonitor(ctx, monitor.GetId())
	if err != nil {
		return err
	}
	state := job.GetMonitor().GetState()
	if state.GetTriggered() == nil && state.GetPaymentUnknown() == nil && state.GetFailed() == nil && state.GetStopped() == nil {
		return fmt.Errorf("%w: monitor is not retryable in its current state", application.ErrConflict)
	}
	taskID := "monitor-retry:" + monitor.GetId()
	if !server.beginTask(taskID) {
		return fmt.Errorf("%w: monitor retry is already running", application.ErrConflict)
	}
	if state.GetTriggered() != nil || state.GetPaymentUnknown() != nil {
		_, err = server.abandonPaymentSession(ctx, monitor.GetId())
	} else {
		err = server.rearmMonitor(ctx, monitor.GetId())
	}
	if err != nil {
		server.finishTask(taskID, err)
		return err
	}
	// The durable monitor mutation is the wake-up. The local supervisor starts
	// it when a warm browser is available.
	server.finishTask(taskID, nil)
	server.refreshBookingDemand(ctx)
	return nil
}

func (server *Server) rearmMonitor(ctx context.Context, monitorID string) error {
	resource, err := server.repository.GetMonitor(ctx, monitorID)
	if err != nil {
		return err
	}
	monitor := resource.GetMonitor()
	if monitor == nil {
		return application.ErrNotFound
	}
	state := monitor.GetState()
	if state.GetFailed() == nil && state.GetStopped() == nil {
		return fmt.Errorf("%w: monitor changed before retry", application.ErrConflict)
	}
	monitor = proto.CloneOf(monitor)
	monitor.SetReservationId("")
	monitor.SetState(pendingMonitorState())
	monitor.SetUpdatedAt(timestamp(server.clock.Now()))
	resource = proto.CloneOf(resource)
	resource.SetMonitor(monitor)
	return server.repository.PutMonitor(ctx, resource)
}

func (server *Server) createMonitor(writer http.ResponseWriter, request *http.Request) {
	input := &clientpb.WebUIResourceMutation{}
	if !decodeProtoJSON(server, writer, request, input) {
		return
	}
	service := application.NewMonitorService(server.repository, server.repository, server.ids, server.clock)
	var (
		job *clientpb.Resource
		err error
	)
	if input.GetMutation().GetCommandId() != "" {
		job, err = service.CreateIdempotent(request.Context(), input)
	} else {
		job, err = service.Create(request.Context(), input)
	}
	if err != nil {
		server.writeError(writer, err)
		return
	}
	server.refreshBookingDemand(request.Context())
	writeProtoJSON(writer, http.StatusCreated, job)
}

func (server *Server) updateMonitor(writer http.ResponseWriter, request *http.Request) {
	input := &clientpb.WebUIResourceMutation{}
	if !decodeProtoJSON(server, writer, request, input) {
		return
	}
	job, err := application.NewMonitorService(server.repository, server.repository, server.ids, server.clock).Update(request.Context(), input)
	if err != nil {
		server.writeError(writer, err)
		return
	}
	server.refreshBookingDemand(request.Context())
	writeProtoJSON(writer, http.StatusOK, job)
}

func (server *Server) deleteMonitor(writer http.ResponseWriter, request *http.Request) {
	input := &clientpb.WebUIResourceDeletion{}
	if !decodeProtoJSON(server, writer, request, input) {
		return
	}
	job, err := server.repository.GetMonitor(request.Context(), input.GetId())
	if err != nil || job.GetMonitor().GetUserId() != input.GetUserId() {
		server.writeError(writer, application.ErrNotFound)
		return
	}
	if _, err = server.abandonPaymentSession(request.Context(), input.GetId()); err != nil {
		server.writeError(writer, err)
		return
	}
	err = application.NewMonitorService(
		server.repository, server.repository, server.ids, server.clock,
	).Delete(request.Context(), input.GetUserId(), input.GetId(), input.GetMutation().GetExpectedRevision())
	if err != nil {
		server.writeError(writer, err)
		return
	}
	server.refreshBookingDemand(request.Context())
	writeProtoJSON(writer, http.StatusOK, actionStatus(false))
}
