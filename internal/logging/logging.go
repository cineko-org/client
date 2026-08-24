// Package logging contains the Client-wide structured logging and request
// correlation primitives.  It intentionally uses the standard library so
// logs are available during desktop startup, before any application services
// have been constructed.
package logging

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

const RequestIDHeader = "X-Request-Id"

const maxLoggedBodyBytes = 64 << 10

type requestIDContextKey struct{}

type requestLoggedContextKey struct{}

var (
	loggerMu sync.RWMutex
	logger   = newLogger(os.Stderr)
	sequence atomic.Uint64
)

func newLogger(output io.Writer) *slog.Logger {
	return slog.New(slog.NewJSONHandler(output, &slog.HandlerOptions{Level: slog.LevelInfo})).With("service", "client")
}

// SetOutput replaces the structured logger output and returns a restore
// function.  It is primarily useful to focused tests and diagnostics.
func SetOutput(output io.Writer) func() {
	if output == nil {
		output = io.Discard
	}
	loggerMu.Lock()
	previous := logger
	logger = newLogger(output)
	loggerMu.Unlock()
	return func() {
		loggerMu.Lock()
		logger = previous
		loggerMu.Unlock()
	}
}

// OpenPersistent mirrors all structured Client and embedded-scanner logs to
// ~/cineko/client.log while retaining stderr for local terminal diagnostics.
func OpenPersistent(dataDir string) (func() error, error) {
	if strings.TrimSpace(dataDir) == "" || !filepath.IsAbs(dataDir) {
		return nil, errors.New("absolute Client data directory is required for logging")
	}
	if err := os.MkdirAll(dataDir, 0o700); err != nil {
		return nil, fmt.Errorf("create Client log directory: %w", err)
	}
	path := filepath.Join(dataDir, "client.log")
	file, err := os.OpenFile(filepath.Clean(path), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return nil, fmt.Errorf("open Client log: %w", err)
	}
	restore := SetOutput(io.MultiWriter(os.Stderr, file))
	return func() error {
		restore()
		return file.Close()
	}, nil
}

func logEvent(ctx context.Context, level slog.Level, event string, args ...any) {
	loggerMu.RLock()
	current := logger
	loggerMu.RUnlock()
	current.Log(ctx, level, event, args...)
}

// Logger returns the current structured client logger for components that
// accept a standard-library slog.Logger (for example the embedded Probe
// runtime).  The logger already carries the service=client attribute and uses
// the same output as Info and Error.
func Logger() *slog.Logger {
	loggerMu.RLock()
	current := logger
	loggerMu.RUnlock()
	return current
}

func Info(ctx context.Context, event string, args ...any) {
	logEvent(ctx, slog.LevelInfo, event, args...)
}

func Warn(ctx context.Context, event string, args ...any) {
	logEvent(ctx, slog.LevelWarn, event, args...)
}

func Error(ctx context.Context, event string, args ...any) {
	logEvent(ctx, slog.LevelError, event, args...)
}

// WarnUnexpected records a recoverable deviation from one scenario contract.
// Keeping these fields identical across scanners, monitors, and booking makes
// the local operations view useful without parsing human error messages.
func WarnUnexpected(
	ctx context.Context,
	event string,
	scenario string,
	operation string,
	expected string,
	observed string,
	args ...any,
) {
	fields := make([]any, 0, 12+len(args))
	fields = append(fields,
		"event", event,
		"scenario", scenario,
		"operation", operation,
		"outcome", "unexpected",
		"expected", expected,
		"observed", observed,
	)
	Warn(ctx, event, append(fields, args...)...)
}

// ErrorUnexpected records a deviation that stopped the active scenario.
func ErrorUnexpected(
	ctx context.Context,
	event string,
	scenario string,
	operation string,
	expected string,
	observed string,
	err error,
	args ...any,
) {
	fields := []any{
		"event", event,
		"scenario", scenario,
		"operation", operation,
		"outcome", "failed",
		"expected", expected,
		"observed", observed,
	}
	if err != nil {
		fields = append(fields, "error", fmt.Sprintf("%+v", err))
	}
	Error(ctx, event, append(fields, args...)...)
}

func NewRequestID() string {
	value := make([]byte, 16)
	if _, err := rand.Read(value); err == nil {
		return hex.EncodeToString(value)
	}
	// A request ID is diagnostic correlation data, not an authentication
	// secret.  Keep logging available even if the system CSPRNG is unavailable.
	return fmt.Sprintf("%x-%x", time.Now().UnixNano(), sequence.Add(1))
}

func WithRequestID(ctx context.Context, requestID string) context.Context {
	return context.WithValue(ctx, requestIDContextKey{}, strings.TrimSpace(requestID))
}

func RequestID(ctx context.Context) string {
	if ctx == nil {
		return ""
	}
	requestID, _ := ctx.Value(requestIDContextKey{}).(string)
	return strings.TrimSpace(requestID)
}

func requestIDFor(request *http.Request) string {
	if request != nil {
		if value := strings.TrimSpace(request.Header.Get(RequestIDHeader)); value != "" {
			return value
		}
		// EventSource cannot set arbitrary request headers, so the frontend
		// carries its correlation ID in a dedicated query parameter instead.
		if value := strings.TrimSpace(request.URL.Query().Get("request_id")); value != "" {
			return value
		}
		if value := RequestID(request.Context()); value != "" {
			return value
		}
	}
	return NewRequestID()
}

type responseRecorder struct {
	http.ResponseWriter
	status      int
	bytes       int64
	wroteHeader bool
	body        boundedCapture
}

type boundedCapture struct {
	contents []byte
}

func (capture *boundedCapture) Write(contents []byte) (int, error) {
	remaining := maxLoggedBodyBytes - len(capture.contents)
	if remaining > 0 {
		if len(contents) < remaining {
			remaining = len(contents)
		}
		capture.contents = append(capture.contents, contents[:remaining]...)
	}
	return len(contents), nil
}

func (capture *boundedCapture) String() string {
	return string(capture.contents)
}

type captureReadCloser struct {
	reader io.Reader
	closer io.Closer
}

func (reader *captureReadCloser) Read(contents []byte) (int, error) {
	return reader.reader.Read(contents)
}

func (reader *captureReadCloser) Close() error {
	return reader.closer.Close()
}

func (recorder *responseRecorder) WriteHeader(status int) {
	if recorder.wroteHeader {
		return
	}
	recorder.status = status
	recorder.wroteHeader = true
	recorder.ResponseWriter.WriteHeader(status)
}

func (recorder *responseRecorder) Write(contents []byte) (int, error) {
	if !recorder.wroteHeader {
		recorder.WriteHeader(http.StatusOK)
	}
	_, _ = recorder.body.Write(contents)
	count, err := recorder.ResponseWriter.Write(contents)
	recorder.bytes += int64(count)
	return count, err
}

func (recorder *responseRecorder) Flush() {
	if !recorder.wroteHeader {
		recorder.WriteHeader(http.StatusOK)
	}
	if flusher, ok := recorder.ResponseWriter.(http.Flusher); ok {
		flusher.Flush()
	}
}

func (recorder *responseRecorder) Unwrap() http.ResponseWriter {
	return recorder.ResponseWriter
}

// HTTPMiddleware records every inbound WebUI request, including requests
// served by the Wails desktop asset handler.  It preserves http.Flusher for
// the seat-map SSE endpoint.
func HTTPMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.Context().Value(requestLoggedContextKey{}) != nil {
			next.ServeHTTP(writer, request)
			return
		}
		requestID := requestIDFor(request)
		requestContext := WithRequestID(request.Context(), requestID)
		requestContext = context.WithValue(requestContext, requestLoggedContextKey{}, true)
		request = request.WithContext(requestContext)
		request.Header.Set(RequestIDHeader, requestID)
		writer.Header().Set(RequestIDHeader, requestID)
		requestBody := &boundedCapture{}
		if request.Body != nil {
			request.Body = &captureReadCloser{
				reader: io.TeeReader(request.Body, requestBody),
				closer: request.Body,
			}
		}
		recorder := &responseRecorder{ResponseWriter: writer}
		started := time.Now()
		next.ServeHTTP(recorder, request)
		if recorder.status == 0 {
			recorder.status = http.StatusOK
		}
		// A handler may have written its own value; the inbound correlation ID
		// is authoritative and is restored for clients after the handler runs.
		writer.Header().Set(RequestIDHeader, requestID)
		args := []any{
			"request_id", requestID,
			"method", request.Method,
			"route", request.URL.Path,
			"path", request.URL.Path,
			"status", recorder.status,
			"duration_ms", float64(time.Since(started).Microseconds()) / 1000,
			"response_bytes", recorder.bytes,
		}
		if request.ContentLength >= 0 {
			args = append(args, "request_bytes", request.ContentLength)
		}
		if recorder.status >= http.StatusBadRequest {
			args = append(args,
				"error", http.StatusText(recorder.status),
				"request_body", requestBody.String(),
				"response_body", recorder.body.String(),
			)
			Error(request.Context(), "http.server.request.completed", args...)
			return
		}
		Info(request.Context(), "http.server.request.completed", args...)
	})
}

type roundTripper struct {
	base http.RoundTripper
}

// RoundTripper wraps outbound HTTP transports so every request emits the same
// structured lifecycle.
func RoundTripper(base http.RoundTripper) http.RoundTripper {
	if base == nil {
		base = http.DefaultTransport
	}
	return &roundTripper{base: base}
}

func (transport *roundTripper) RoundTrip(request *http.Request) (*http.Response, error) {
	requestID := requestIDFor(request)
	requestContext := WithRequestID(request.Context(), requestID)
	request = request.Clone(requestContext)
	request.Header.Set(RequestIDHeader, requestID)
	started := time.Now()
	attemptArgs := []any{
		"request_id", requestID,
		"method", request.Method,
		"route", request.URL.Path,
		"path", request.URL.Path,
	}
	if request.ContentLength >= 0 {
		attemptArgs = append(attemptArgs, "request_bytes", request.ContentLength)
	}
	Info(requestContext, "http.client.request.attempted", attemptArgs...)

	response, err := transport.base.RoundTrip(request)
	if err != nil {
		logClientCompleted(requestContext, request, requestID, "", 0, 0, started, fmt.Sprintf("%+v", err))
		return nil, err
	}
	if response == nil {
		err = errors.New("HTTP transport returned a nil response")
		logClientCompleted(requestContext, request, requestID, "", 0, 0, started, err.Error())
		return nil, err
	}
	upstreamRequestID := strings.TrimSpace(response.Header.Get(RequestIDHeader))
	if response.Body == nil {
		errorText := ""
		if response.StatusCode >= http.StatusBadRequest {
			errorText = http.StatusText(response.StatusCode)
		}
		logClientCompleted(requestContext, request, requestID, upstreamRequestID, response.StatusCode, 0, started, errorText)
		return response, nil
	}
	response.Body = &responseBody{
		ReadCloser: response.Body,
		context:    requestContext,
		request:    request,
		requestID:  requestID,
		upstreamID: upstreamRequestID,
		status:     response.StatusCode,
		started:    started,
	}
	return response, nil
}

type responseBody struct {
	io.ReadCloser
	context    context.Context
	request    *http.Request
	requestID  string
	upstreamID string
	status     int
	started    time.Time
	bytes      int64
	once       sync.Once
	readError  error
}

func (body *responseBody) Read(contents []byte) (int, error) {
	count, err := body.ReadCloser.Read(contents)
	body.bytes += int64(count)
	if err != nil {
		if err != io.EOF {
			body.readError = err
		}
		body.complete()
	}
	return count, err
}

func (body *responseBody) Close() error {
	err := body.ReadCloser.Close()
	if err != nil && body.readError == nil {
		body.readError = err
	}
	body.complete()
	return err
}

func (body *responseBody) complete() {
	body.once.Do(func() {
		errorText := ""
		if body.readError != nil {
			errorText = fmt.Sprintf("%+v", body.readError)
		} else if body.status >= http.StatusBadRequest {
			errorText = http.StatusText(body.status)
		}
		logClientCompleted(body.context, body.request, body.requestID, body.upstreamID, body.status, body.bytes, body.started, errorText)
	})
}

func logClientCompleted(
	ctx context.Context,
	request *http.Request,
	requestID string,
	upstreamRequestID string,
	status int,
	responseBytes int64,
	started time.Time,
	errorText string,
) {
	args := []any{
		"request_id", requestID,
		"method", request.Method,
		"route", request.URL.Path,
		"path", request.URL.Path,
		"status", status,
		"duration_ms", float64(time.Since(started).Microseconds()) / 1000,
		"response_bytes", responseBytes,
	}
	if request.ContentLength >= 0 {
		args = append(args, "request_bytes", request.ContentLength)
	}
	if upstreamRequestID != "" && upstreamRequestID != requestID {
		args = append(args, "upstream_request_id", upstreamRequestID)
	}
	if errorText != "" {
		args = append(args, "error", errorText)
	}
	if errorText != "" || status >= http.StatusBadRequest {
		Error(ctx, "http.client.request.completed", args...)
		return
	}
	Info(ctx, "http.client.request.completed", args...)
}
