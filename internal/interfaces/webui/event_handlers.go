package webui

import (
	"context"
	"net/http"
	"strings"
	"time"

	"github.com/cineko-org/client/internal/domain"
)

type appEventRepository interface {
	PutAppEvent(context.Context, domain.AppEvent) error
	ListAppEvents(context.Context, string, int) ([]domain.AppEvent, error)
	MarkAppEventsRead(context.Context, string, time.Time) error
	DeleteAppEvents(context.Context, string) error
	DeleteAppEventsBefore(context.Context, time.Time) error
}

type startupRecoveryRepository interface {
	RecoverInterruptedWork(context.Context, time.Time) ([]domain.AppEvent, error)
}

func (server *Server) addEvent(userID, kind string, tone domain.EventTone, message string) {
	root := server.rootContext
	if root == nil {
		root = context.Background()
	}
	_ = server.persistEvent(context.WithoutCancel(root), domain.AppEvent{
		UserID: userID, Kind: kind, Tone: tone, Message: message, CreatedAt: server.clock.Now(),
	})
}

// RecordSystemEvent exposes the durable notification boundary to the desktop
// shell for startup and configuration failures.
func (server *Server) RecordSystemEvent(userID, kind string, tone domain.EventTone, message string) {
	server.addEvent(userID, kind, tone, message)
}

// RecordLocalSystemEvent deliberately skips external publishers. It is used
// when a publisher itself fails, preventing recursive delivery attempts.
func (server *Server) RecordLocalSystemEvent(userID, kind string, tone domain.EventTone, message string) {
	root := server.rootContext
	if root == nil {
		root = context.Background()
	}
	_ = server.persistEventLocally(context.WithoutCancel(root), domain.AppEvent{
		UserID: userID, Kind: kind, Tone: tone, Message: message, CreatedAt: server.clock.Now(),
	})
}

func (server *Server) persistEvent(ctx context.Context, event domain.AppEvent) error {
	if ctx == nil {
		ctx = context.Background()
	}
	event = server.normalizedEvent(event)
	if err := server.persistEventLocally(ctx, event); err != nil {
		return err
	}
	if server.eventPublisher != nil {
		if err := server.eventPublisher.Publish(context.WithoutCancel(ctx), event); err != nil {
			server.recordMaintenanceFailure("event-publisher", err)
		}
	}
	return nil
}

func (server *Server) persistEventLocally(ctx context.Context, event domain.AppEvent) error {
	repository, ok := server.repository.(appEventRepository)
	if !ok {
		return nil
	}
	if ctx == nil {
		ctx = context.Background()
	}
	event = server.normalizedEvent(event)
	if err := repository.PutAppEvent(ctx, event); err != nil {
		server.recordMaintenanceFailure("event-log", err)
		return err
	}
	return nil
}

func (server *Server) normalizedEvent(event domain.AppEvent) domain.AppEvent {
	if event.ID == "" {
		event.ID = server.ids.NewID()
	}
	if event.CreatedAt.IsZero() {
		event.CreatedAt = server.clock.Now()
	}
	return event
}

func (server *Server) events(writer http.ResponseWriter, request *http.Request) {
	userID := strings.TrimSpace(request.URL.Query().Get("user"))
	if userID == "" {
		userID = "local-user"
	}
	repository, ok := server.repository.(appEventRepository)
	if !ok {
		server.writeJSON(writer, http.StatusOK, []domain.AppEvent{})
		return
	}
	values, err := repository.ListAppEvents(request.Context(), userID, 100)
	if err != nil {
		server.writeError(writer, err)
		return
	}
	server.writeJSON(writer, http.StatusOK, values)
}

func (server *Server) createEvent(writer http.ResponseWriter, request *http.Request) {
	var input struct {
		UserID  string           `json:"userId"`
		Kind    string           `json:"kind"`
		Tone    domain.EventTone `json:"tone"`
		Message string           `json:"message"`
	}
	if !server.decode(writer, request, &input) {
		return
	}
	input.UserID = strings.TrimSpace(input.UserID)
	input.Message = strings.TrimSpace(input.Message)
	if input.UserID == "" || input.Message == "" {
		server.writeJSON(writer, http.StatusBadRequest, map[string]string{"error": "알림 내용을 입력하세요."})
		return
	}
	event := domain.AppEvent{ID: server.ids.NewID(), UserID: input.UserID, Kind: input.Kind,
		Tone: input.Tone, Message: input.Message, CreatedAt: server.clock.Now()}
	if err := server.persistEvent(request.Context(), event); err != nil {
		server.writeError(writer, err)
		return
	}
	server.writeJSON(writer, http.StatusCreated, event)
}

func (server *Server) readEvents(writer http.ResponseWriter, request *http.Request) {
	server.mutateEvents(writer, request, func(ctx context.Context, repository appEventRepository, userID string) error {
		return repository.MarkAppEventsRead(ctx, userID, server.clock.Now())
	})
}

func (server *Server) clearEvents(writer http.ResponseWriter, request *http.Request) {
	server.mutateEvents(writer, request, func(ctx context.Context, repository appEventRepository, userID string) error {
		return repository.DeleteAppEvents(ctx, userID)
	})
}

func (server *Server) mutateEvents(
	writer http.ResponseWriter,
	request *http.Request,
	mutation func(context.Context, appEventRepository, string) error,
) {
	var input struct {
		UserID string `json:"userId"`
	}
	if !server.decode(writer, request, &input) {
		return
	}
	repository, ok := server.repository.(appEventRepository)
	if !ok {
		server.writeJSON(writer, http.StatusOK, map[string]string{"status": "ok"})
		return
	}
	if err := mutation(request.Context(), repository, strings.TrimSpace(input.UserID)); err != nil {
		server.writeError(writer, err)
		return
	}
	server.writeJSON(writer, http.StatusOK, map[string]string{"status": "ok"})
}
