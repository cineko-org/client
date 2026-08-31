package webui

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/cineko-org/client/internal/logging"
	"github.com/cineko-org/probe/v2/networkcapture"
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
		Since:        server.observabilityStart(),
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

func (server *Server) networkLogs(writer http.ResponseWriter, request *http.Request) {
	if strings.TrimSpace(server.networkCaptureDir) == "" {
		server.writeAPIError(writer, request, http.StatusServiceUnavailable, "network_logs_unavailable", "network capture journal is unavailable", true)
		return
	}
	query := networkcapture.Query{
		Outcome:        strings.TrimSpace(request.URL.Query().Get("outcome")),
		URLContains:    strings.TrimSpace(request.URL.Query().Get("url")),
		CompletedAfter: server.observabilityStart(),
	}
	var err error
	for value, target := range map[string]*int{
		"limit": &query.Limit, "status": &query.Status, "minimum_status": &query.MinimumStatus,
	} {
		raw := strings.TrimSpace(request.URL.Query().Get(value))
		if raw == "" {
			continue
		}
		*target, err = strconv.Atoi(raw)
		if err != nil || *target < 0 {
			server.writeAPIError(writer, request, http.StatusBadRequest, "invalid_network_log_query", value+" must be a non-negative integer", false)
			return
		}
	}
	entries, err := networkcapture.List(server.networkCaptureDir, query)
	if err != nil {
		logging.ErrorUnexpected(request.Context(), "operations.network_logs.read.failed", "operations", "read_network_journal",
			"readable local network journal", "network journal read failed", err)
		server.writeAPIError(writer, request, http.StatusInternalServerError, "network_logs_unavailable", "network captures could not be read", true)
		return
	}
	statistics, err := networkcapture.Stats(server.networkCaptureDir, networkcapture.Query{
		CompletedAfter: server.observabilityStart(),
	})
	if err != nil {
		logging.ErrorUnexpected(request.Context(), "operations.network_logs.stats.failed", "operations", "read_network_statistics",
			"readable local network journal", "network statistics read failed", err)
		server.writeAPIError(writer, request, http.StatusInternalServerError, "network_logs_unavailable", "network captures could not be summarized", true)
		return
	}
	writer.Header().Set("Content-Type", "application/json; charset=utf-8")
	_ = json.NewEncoder(writer).Encode(map[string]any{"entries": entries, "matching": len(entries), "statistics": statistics})
}

func (server *Server) clearOperationLogs(writer http.ResponseWriter, request *http.Request) {
	if server.clearLogs == nil {
		server.writeAPIError(writer, request, http.StatusServiceUnavailable, "logs_clear_unavailable", "local logs cannot be cleared", true)
		return
	}
	if err := server.clearLogs(request.Context()); err != nil {
		logging.ErrorUnexpected(request.Context(), "operations.logs.clear.failed", "operations", "clear_local_logs",
			"empty structured and network journals", "journal clear failed", err)
		server.writeAPIError(writer, request, http.StatusInternalServerError, "logs_clear_failed", "local logs could not be cleared", true)
		return
	}
	server.observabilityMu.Lock()
	server.observabilityStartedAt = server.clock.Now()
	server.observabilityMu.Unlock()
	writer.WriteHeader(http.StatusNoContent)
}

func (server *Server) observabilityStart() time.Time {
	server.observabilityMu.RLock()
	defer server.observabilityMu.RUnlock()
	return server.observabilityStartedAt
}

func (server *Server) networkLog(writer http.ResponseWriter, request *http.Request) {
	exchange, err := networkcapture.ReadExchange(server.networkCaptureDir, request.PathValue("id"))
	if errors.Is(err, os.ErrNotExist) {
		server.writeAPIError(writer, request, http.StatusNotFound, "network_log_not_found", "network capture was not found", false)
		return
	}
	if err != nil {
		server.writeAPIError(writer, request, http.StatusBadRequest, "invalid_network_log", err.Error(), false)
		return
	}
	writer.Header().Set("Content-Type", "application/json; charset=utf-8")
	_ = json.NewEncoder(writer).Encode(exchange)
}

func (server *Server) networkLogBody(writer http.ResponseWriter, request *http.Request) {
	exchange, err := networkcapture.ReadExchange(server.networkCaptureDir, request.PathValue("id"))
	if err != nil {
		status := http.StatusBadRequest
		if errors.Is(err, os.ErrNotExist) {
			status = http.StatusNotFound
		}
		server.writeAPIError(writer, request, status, "network_log_not_found", "network capture was not found", false)
		return
	}
	path, body, err := networkcapture.BodyPath(server.networkCaptureDir, exchange, request.PathValue("side"))
	if err != nil {
		status := http.StatusBadRequest
		if errors.Is(err, os.ErrNotExist) {
			status = http.StatusNotFound
		}
		server.writeAPIError(writer, request, status, "network_body_not_found", "network body was not found", false)
		return
	}
	if body.ContentType != "" {
		writer.Header().Set("Content-Type", body.ContentType)
	} else {
		writer.Header().Set("Content-Type", "application/octet-stream")
	}
	writer.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="%s-%s.body"`, exchange.ID, request.PathValue("side")))
	http.ServeFile(writer, request, path) // #nosec G703 -- BodyPath confines the file to the application-owned capture root.
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
