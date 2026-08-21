package webui

import (
	"context"
	"fmt"
	"net/http"

	"github.com/cineko-org/client/internal/application"
	clientpb "github.com/cineko-org/contracts/gen/go/cineko/client"
)

func (server *Server) retryMonitor(writer http.ResponseWriter, request *http.Request) {
	input := &clientpb.WebUIMonitorRetryRequest{}
	if !decodeProtoJSON(server, writer, request, input) {
		return
	}
	server.runOwnedTask(writer, request, input, func() error { return server.startMonitorRetry(input) })
}

func (server *Server) startMonitorRetry(input *clientpb.WebUIMonitorRetryRequest) error {
	monitor := input.GetMonitor()
	job, err := server.repository.GetMonitor(context.Background(), monitor.GetId())
	if err != nil {
		return err
	}
	state := job.GetMonitor().GetState()
	if state.GetTriggered() == nil && state.GetPaymentUnknown() == nil {
		return fmt.Errorf("%w: only an unfinished payment can be retried", application.ErrConflict)
	}
	taskID := "monitor-retry:" + monitor.GetId()
	if !server.beginTask(taskID) {
		return fmt.Errorf("%w: monitor retry is already running", application.ErrConflict)
	}
	if _, err := server.abandonPaymentSession(context.Background(), monitor.GetId()); err != nil {
		server.finishTask(taskID, err)
		return err
	}
	root := server.rootContext
	if root == nil {
		root = context.Background()
	}
	// #nosec G118 -- cancel is retained until the retry task completes.
	retryContext, cancel := context.WithCancel(root)
	server.tasksMu.Lock()
	server.taskCancels[taskID] = cancel
	server.tasksMu.Unlock()
	go server.executeMonitorRetry(retryContext, input, taskID)
	return nil
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
