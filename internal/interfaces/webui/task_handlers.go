package webui

import (
	"context"
	"net/http"

	"github.com/cineko-org/client/internal/application"
)

type ownedTaskRequest struct {
	ID            string
	UserID        string
	StartedStatus string
	LoadOwner     func(context.Context, string) (string, error)
}

func (server *Server) runOwnedTask(
	writer http.ResponseWriter,
	request *http.Request,
	input ownedTaskRequest,
	start func() error,
) {
	owner, err := input.LoadOwner(request.Context(), input.ID)
	if err != nil || owner != input.UserID {
		server.writeError(writer, application.ErrNotFound)
		return
	}
	if err := start(); err != nil {
		server.writeError(writer, err)
		return
	}
	server.writeJSON(writer, http.StatusAccepted, map[string]string{"status": input.StartedStatus})
}
