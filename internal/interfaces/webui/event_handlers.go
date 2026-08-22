package webui

import (
	"context"
	"net/http"
	"strings"
	"time"

	clientpb "github.com/cineko-org/contracts/v3/gen/go/cineko/client"
)

type appEventRepository interface {
	PutAppEvent(context.Context, *clientpb.Resource) error
	ListAppEvents(context.Context, string, int) ([]*clientpb.Resource, error)
	MarkAppEventsRead(context.Context, string, time.Time) error
	DeleteAppEvents(context.Context, string) error
	DeleteAppEventsBefore(context.Context, time.Time) error
}

type startupRecoveryRepository interface {
	RecoverInterruptedWork(context.Context, time.Time) ([]*clientpb.Resource, error)
}

func (server *Server) addEvent(event *clientpb.AppEvent) {
	root := server.rootContext
	if root == nil {
		root = context.Background()
	}
	_ = server.persistEvent(context.WithoutCancel(root), event)
}

// RecordSystemEvent exposes the durable notification boundary to the desktop
// shell for startup and configuration failures.
func (server *Server) RecordSystemEvent(event *clientpb.AppEvent) {
	server.addEvent(event)
}

// RecordLocalSystemEvent deliberately skips external publishers. It is used
// when a publisher itself fails, preventing recursive delivery attempts.
func (server *Server) RecordLocalSystemEvent(event *clientpb.AppEvent) {
	root := server.rootContext
	if root == nil {
		root = context.Background()
	}
	_ = server.persistEventLocally(context.WithoutCancel(root), event)
}

func (server *Server) persistEvent(ctx context.Context, event *clientpb.AppEvent) error {
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

func (server *Server) persistEventLocally(ctx context.Context, event *clientpb.AppEvent) error {
	repository, ok := server.repository.(appEventRepository)
	if !ok {
		return nil
	}
	if ctx == nil {
		ctx = context.Background()
	}
	event = server.normalizedEvent(event)
	if err := repository.PutAppEvent(ctx, resourceFromAppEvent(event)); err != nil {
		server.recordMaintenanceFailure("event-log", err)
		return err
	}
	return nil
}

func (server *Server) normalizedEvent(event *clientpb.AppEvent) *clientpb.AppEvent {
	if event == nil {
		event = &clientpb.AppEvent{}
	}
	if event.GetId() == "" {
		event.SetId(server.ids.NewID())
	}
	if event.GetCreatedAt() == nil {
		event.SetCreatedAt(timestamp(server.clock.Now()))
	}
	return event
}

func appErrorEvent(userID, kind, message string) *clientpb.AppEvent {
	return clientpb.AppEvent_builder{UserId: &userID, Kind: &kind, Message: &message, Error: clientpb.EventError_builder{}.Build()}.Build()
}

func appSuccessEvent(userID, kind, message string) *clientpb.AppEvent {
	return clientpb.AppEvent_builder{UserId: &userID, Kind: &kind, Message: &message, Success: clientpb.EventSuccess_builder{}.Build()}.Build()
}

func appWarningEvent(userID, kind, message string) *clientpb.AppEvent {
	return clientpb.AppEvent_builder{UserId: &userID, Kind: &kind, Message: &message, Warning: clientpb.EventWarning_builder{}.Build()}.Build()
}

func resourceFromAppEvent(event *clientpb.AppEvent) *clientpb.Resource {
	createdAt := time.Time{}
	if event.GetCreatedAt() != nil {
		createdAt = event.GetCreatedAt().AsTime()
	}
	return clientpb.Resource_builder{Identity: resourceIdentity(event.GetId(), createdAt), AppEvent: event}.Build()
}

func (server *Server) events(writer http.ResponseWriter, request *http.Request) {
	userID := strings.TrimSpace(request.URL.Query().Get("user"))
	if userID == "" {
		userID = "local-user"
	}
	repository, ok := server.repository.(appEventRepository)
	if !ok {
		writeProtoJSON(writer, http.StatusOK, clientpb.WebUIResourceList_builder{}.Build())
		return
	}
	values, err := repository.ListAppEvents(request.Context(), userID, 100)
	if err != nil {
		server.writeError(writer, err)
		return
	}
	writeProtoJSON(writer, http.StatusOK, clientpb.WebUIResourceList_builder{Resources: values}.Build())
}

func (server *Server) createEvent(writer http.ResponseWriter, request *http.Request) {
	input := &clientpb.AppEvent{}
	if !decodeProtoJSON(server, writer, request, input) {
		return
	}
	event := server.normalizedEvent(input)
	if err := server.persistEvent(request.Context(), event); err != nil {
		server.writeError(writer, err)
		return
	}
	writeProtoJSON(writer, http.StatusCreated, resourceFromAppEvent(event))
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
	input := &clientpb.WebUIAppEventUserRequest{}
	if !decodeProtoJSON(server, writer, request, input) {
		return
	}
	repository, ok := server.repository.(appEventRepository)
	if !ok {
		writeProtoJSON(writer, http.StatusOK, actionStatus(false))
		return
	}
	if err := mutation(request.Context(), repository, strings.TrimSpace(input.GetUserId())); err != nil {
		server.writeError(writer, err)
		return
	}
	writeProtoJSON(writer, http.StatusOK, actionStatus(false))
}
