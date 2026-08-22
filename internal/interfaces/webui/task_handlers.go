package webui

import (
	"net/http"

	"github.com/cineko-org/client/internal/application"
	clientpb "github.com/cineko-org/contracts/v3/gen/go/cineko/client"
)

func (server *Server) runOwnedTask(
	writer http.ResponseWriter,
	request *http.Request,
	input *clientpb.WebUIMonitorRetryRequest,
	start func() error,
) {
	monitor := input.GetMonitor()
	job, err := server.repository.GetMonitor(request.Context(), monitor.GetId())
	if err != nil || job.GetMonitor().GetUserId() != monitor.GetUserId() {
		server.writeError(writer, application.ErrNotFound)
		return
	}
	if err := start(); err != nil {
		server.writeError(writer, err)
		return
	}
	writeProtoJSON(writer, http.StatusAccepted, actionStatus(true))
}
