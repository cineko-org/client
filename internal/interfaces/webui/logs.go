package webui

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"

	"github.com/cineko-org/client/internal/logging"
)

const maximumClientLogEventBytes = 64 << 10

func (server *Server) logs(writer http.ResponseWriter, request *http.Request) {
	minimum, err := logging.ParseMinimumLevel(request.URL.Query().Get("min_level"))
	if err != nil {
		server.writeAPIError(writer, request, http.StatusBadRequest, "invalid_log_level", err.Error(), false)
		return
	}
	limit := 0
	if value := strings.TrimSpace(request.URL.Query().Get("limit")); value != "" {
		limit, err = strconv.Atoi(value)
		if err != nil || limit <= 0 {
			server.writeAPIError(writer, request, http.StatusBadRequest, "invalid_log_limit", "limit must be a positive integer", false)
			return
		}
	}
	if strings.TrimSpace(server.logPath) == "" {
		server.writeAPIError(writer, request, http.StatusServiceUnavailable, "logs_unavailable", "local log journal is unavailable", true)
		return
	}
	snapshot, err := logging.ReadSnapshot(server.logPath, logging.Query{
		MinimumLevel: minimum,
		Scenario:     strings.TrimSpace(request.URL.Query().Get("scenario")),
		Limit:        limit,
	})
	if err != nil {
		logging.ErrorUnexpected(request.Context(), "operations.logs.read.failed", "operations", "read_log_journal",
			"readable local JSONL journal", "journal read failed", err, "log_path", server.logPath)
		server.writeAPIError(writer, request, http.StatusInternalServerError, "logs_unavailable", "local logs could not be read", true)
		return
	}
	writer.Header().Set("Content-Type", "application/json; charset=utf-8")
	if err := json.NewEncoder(writer).Encode(snapshot); err != nil {
		logging.ErrorUnexpected(request.Context(), "operations.logs.encode.failed", "operations", "encode_log_snapshot",
			"JSON response", "response encoding failed", err)
	}
}

func (server *Server) recordClientLog(writer http.ResponseWriter, request *http.Request) {
	request.Body = http.MaxBytesReader(writer, request.Body, maximumClientLogEventBytes)
	decoder := json.NewDecoder(request.Body)
	decoder.DisallowUnknownFields()
	var input logging.ClientEvent
	if err := decoder.Decode(&input); err != nil {
		server.writeAPIError(writer, request, http.StatusBadRequest, "invalid_log_event", "client log event is invalid", false)
		return
	}
	if err := ensureJSONEnd(decoder); err != nil {
		server.writeAPIError(writer, request, http.StatusBadRequest, "invalid_log_event", "client log event has trailing data", false)
		return
	}
	if err := input.Record(request.Context()); err != nil {
		server.writeAPIError(writer, request, http.StatusBadRequest, "invalid_log_event", err.Error(), false)
		return
	}
	writer.WriteHeader(http.StatusNoContent)
}

func ensureJSONEnd(decoder *json.Decoder) error {
	var extra any
	err := decoder.Decode(&extra)
	if err == io.EOF {
		return nil
	}
	if err == nil {
		return fmt.Errorf("unexpected trailing JSON")
	}
	return err
}
