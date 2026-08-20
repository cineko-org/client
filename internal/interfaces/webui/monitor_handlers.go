package webui

import (
	"context"
	"fmt"
	"net/http"
	"time"

	"github.com/cineko-org/client/internal/application"
	"github.com/cineko-org/client/internal/domain"
)

type monitorRequest struct {
	Revision          int64              `json:"revision"`
	IdempotencyKey    string             `json:"idempotencyKey"`
	ID                string             `json:"id"`
	UserID            string             `json:"userId"`
	PresetID          string             `json:"presetId"`
	Mode              domain.MonitorMode `json:"mode"`
	MovieID           string             `json:"movieId"`
	Movie             string             `json:"movie"`
	TargetDates       []string           `json:"targetDates"`
	TargetWeekdays    []int              `json:"targetWeekdays"`
	SearchHorizonDays int                `json:"searchHorizonDays"`
	EarliestTime      string             `json:"earliestTime"`
	LatestTime        string             `json:"latestTime"`
	PollInterval      time.Duration      `json:"pollInterval"`
	PollIntervalMax   time.Duration      `json:"pollIntervalMax"`
	Headful           bool               `json:"headful"`
}

func (server *Server) retryMonitor(writer http.ResponseWriter, request *http.Request) {
	var input monitorRequest
	if !server.decode(writer, request, &input) {
		return
	}
	server.runOwnedTask(writer, request, ownedTaskRequest{
		ID: input.ID, UserID: input.UserID,
		StartedStatus: "monitor retry started",
		LoadOwner: func(ctx context.Context, id string) (string, error) {
			job, err := server.repository.GetMonitor(ctx, id)
			return job.UserID, err
		},
	}, func() error { return server.startMonitorRetry(input) })
}

func (server *Server) startMonitorRetry(input monitorRequest) error {
	job, err := server.repository.GetMonitor(context.Background(), input.ID)
	if err != nil {
		return err
	}
	if job.Status != domain.MonitorTriggered && job.Status != domain.MonitorPaymentUnknown {
		return fmt.Errorf("%w: only an unfinished payment can be retried", application.ErrConflict)
	}
	taskID := "monitor-retry:" + input.ID
	if !server.beginTask(taskID) {
		return fmt.Errorf("%w: monitor retry is already running", application.ErrConflict)
	}
	if _, err := server.abandonPaymentSession(context.Background(), input.ID); err != nil {
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

func (input monitorRequest) applicationRequest() application.CreateMonitorRequest {
	return application.CreateMonitorRequest{
		ExpectedRevision: input.Revision,
		UserID:           input.UserID, PresetID: input.PresetID, Mode: input.Mode,
		MovieID: input.MovieID, Movie: input.Movie,
		TargetDates: input.TargetDates, TargetWeekdays: input.TargetWeekdays,
		SearchHorizonDays: input.SearchHorizonDays,
		EarliestTime:      input.EarliestTime, LatestTime: input.LatestTime,
		PollInterval: input.PollInterval, PollIntervalMax: input.PollIntervalMax,
	}
}

func (server *Server) createMonitor(writer http.ResponseWriter, request *http.Request) {
	handleJSON(server, writer, request, http.StatusCreated, func(ctx context.Context, input monitorRequest) (domain.MonitorJob, error) {
		service := application.NewMonitorService(
			server.repository, server.repository, server.ids, server.clock,
		)
		if input.IdempotencyKey != "" {
			job, err := service.CreateIdempotent(ctx, input.IdempotencyKey, input.applicationRequest())
			if err == nil {
				server.refreshBookingDemand(ctx)
			}
			return job, err
		}
		job, err := service.Create(ctx, input.applicationRequest())
		if err == nil {
			server.refreshBookingDemand(ctx)
		}
		return job, err
	})
}

func (server *Server) updateMonitor(writer http.ResponseWriter, request *http.Request) {
	handleJSON(server, writer, request, http.StatusOK, func(ctx context.Context, input monitorRequest) (domain.MonitorJob, error) {
		job, err := application.NewMonitorService(
			server.repository, server.repository, server.ids, server.clock,
		).Update(ctx, application.UpdateMonitorRequest{
			ID: input.ID, CreateMonitorRequest: input.applicationRequest(),
		})
		if err == nil {
			server.refreshBookingDemand(ctx)
		}
		return job, err
	})
}

func (server *Server) deleteMonitor(writer http.ResponseWriter, request *http.Request) {
	var input monitorRequest
	if !server.decode(writer, request, &input) {
		return
	}
	job, err := server.repository.GetMonitor(request.Context(), input.ID)
	if err != nil || job.UserID != input.UserID {
		server.writeError(writer, application.ErrNotFound)
		return
	}
	if _, err = server.abandonPaymentSession(request.Context(), input.ID); err != nil {
		server.writeError(writer, err)
		return
	}
	err = application.NewMonitorService(
		server.repository, server.repository, server.ids, server.clock,
	).Delete(request.Context(), input.UserID, input.ID, input.Revision)
	if err != nil {
		server.writeError(writer, err)
		return
	}
	server.refreshBookingDemand(request.Context())
	server.writeJSON(writer, http.StatusOK, map[string]string{"status": "deleted"})
}
