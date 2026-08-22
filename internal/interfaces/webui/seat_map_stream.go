package webui

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"buf.build/go/protovalidate"
	seatmappb "github.com/cineko-org/contracts/v3/gen/go/cineko/seatmap"
	servicepb "github.com/cineko-org/contracts/v3/gen/go/cineko/service"
	"google.golang.org/protobuf/encoding/protojson"
)

const seatMapEventType = "cineko.seat-map"

type seatMapWatcher interface {
	WatchSeatMap(context.Context, string, func(*seatmappb.Resolution) error) error
}

// watchAuditoriumSeatMap forwards Central's generated watch contract to the
// local UI without exposing Probe scheduling or adding a polling loop.
func (server *Server) watchAuditoriumSeatMap(writer http.ResponseWriter, request *http.Request) {
	auditoriumID := strings.TrimSpace(request.URL.Query().Get("auditoriumId"))
	if auditoriumID == "" {
		server.writeAPIError(writer, request, http.StatusBadRequest, "invalid_request", "auditoriumId is required", false)
		return
	}
	watcher, supported := server.repository.(seatMapWatcher)
	if !supported {
		server.writeAPIError(writer, request, http.StatusServiceUnavailable, "seat_map_unavailable", "seat-map streaming is unavailable", true)
		return
	}
	flusher, supported := writer.(http.Flusher)
	if !supported {
		server.writeAPIError(writer, request, http.StatusInternalServerError, "stream_unavailable", "seat-map streaming is unavailable", true)
		return
	}
	writer.Header().Set("Content-Type", "text/event-stream; charset=utf-8")
	writer.Header().Set("Cache-Control", "no-cache, no-transform")
	writer.Header().Set("X-Accel-Buffering", "no")

	started := false
	err := watcher.WatchSeatMap(request.Context(), auditoriumID, func(resolution *seatmappb.Resolution) error {
		response := servicepb.WatchSeatMapResponse_builder{Resolution: resolution}.Build()
		if err := protovalidate.Validate(response); err != nil {
			return fmt.Errorf("validate seat-map stream response: %w", err)
		}
		encoded, err := (protojson.MarshalOptions{UseProtoNames: false}).Marshal(response)
		if err != nil {
			return fmt.Errorf("encode seat-map stream response: %w", err)
		}
		if !started {
			writer.WriteHeader(http.StatusOK)
			started = true
		}
		if _, err := fmt.Fprintf(writer, "event: %s\ndata: %s\n\n", seatMapEventType, encoded); err != nil {
			return err
		}
		flusher.Flush()
		return nil
	})
	if err != nil && !started && !errors.Is(err, context.Canceled) {
		writer.Header().Del("Content-Type")
		writer.Header().Del("Cache-Control")
		writer.Header().Del("X-Accel-Buffering")
		server.writeError(writer, err)
	}
}
