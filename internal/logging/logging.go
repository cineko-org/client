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

	"github.com/cineko-org/probe/v2/networkcapture"
)

const RequestIDHeader = "X-Request-Id"

const maxLoggedBodyBytes = 64 << 10

type requestIDContextKey struct{}

type requestLoggedContextKey struct{}

var (
	loggerMu     sync.RWMutex
	logger       = newLogger(os.Stderr)
	minimumLevel slog.LevelVar
	debugEnabled atomic.Bool
	sequence     atomic.Uint64
	networkStore atomic.Pointer[networkcapture.Store]
)

func SetNetworkCapture(store *networkcapture.Store) func() {
	previous := networkStore.Swap(store)
	return func() { networkStore.Store(previous) }
}

func newLogger(output io.Writer) *slog.Logger {
	return slog.New(slog.NewJSONHandler(output, &slog.HandlerOptions{Level: &minimumLevel})).With("service", "client")
}

// SetDebug controls verbose request-level diagnostics for the process. Normal
// operation retains semantic INFO events plus every WARN and ERROR record.
func SetDebug(enabled bool) func() {
	previousLevel := minimumLevel.Level()
	previousDebug := debugEnabled.Swap(enabled)
	if enabled {
		minimumLevel.Set(slog.LevelDebug)
	} else {
		minimumLevel.Set(slog.LevelInfo)
	}
	return func() {
		debugEnabled.Store(previousDebug)
		minimumLevel.Set(previousLevel)
	}
}

func DebugEnabled() bool {
	return debugEnabled.Load()
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

// PersistentJournal serializes application log writes with destructive
// maintenance such as clearing the local operations journal.
type PersistentJournal struct {
	mu     sync.Mutex
	file   *os.File
	closed bool
}

func (journal *PersistentJournal) Write(payload []byte) (int, error) {
	if journal == nil {
		return 0, errors.New("persistent journal is nil")
	}
	journal.mu.Lock()
	defer journal.mu.Unlock()
	if journal.closed || journal.file == nil {
		return 0, os.ErrClosed
	}
	return journal.file.Write(payload)
}

// Clear removes every durable structured log record while keeping the active
// logger usable for records written after the operation completes.
func (journal *PersistentJournal) Clear() error {
	if journal == nil {
		return errors.New("persistent journal is nil")
	}
	journal.mu.Lock()
	defer journal.mu.Unlock()
	if journal.closed || journal.file == nil {
		return os.ErrClosed
	}
	if err := journal.file.Truncate(0); err != nil {
		return fmt.Errorf("truncate Client log: %w", err)
	}
	if _, err := journal.file.Seek(0, io.SeekStart); err != nil {
		return fmt.Errorf("rewind Client log: %w", err)
	}
	return journal.file.Sync()
}

func (journal *PersistentJournal) Close() error {
	if journal == nil {
		return nil
	}
	journal.mu.Lock()
	defer journal.mu.Unlock()
	if journal.closed {
		return nil
	}
	journal.closed = true
	if journal.file == nil {
		return nil
	}
	return journal.file.Close()
}

// OpenPersistentJournal mirrors structured logs to the application-owned
// journal and returns the synchronized handle used by the operations UI.
func OpenPersistentJournal(dataDir string) (*PersistentJournal, func() error, error) {
	if strings.TrimSpace(dataDir) == "" || !filepath.IsAbs(dataDir) {
		return nil, nil, errors.New("absolute Client data directory is required for logging")
	}
	if err := os.MkdirAll(dataDir, 0o700); err != nil {
		return nil, nil, fmt.Errorf("create Client log directory: %w", err)
	}
	path := filepath.Join(dataDir, "client.log")
	file, err := os.OpenFile(filepath.Clean(path), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return nil, nil, fmt.Errorf("open Client log: %w", err)
	}
	journal := &PersistentJournal{file: file}
	restore := SetOutput(io.MultiWriter(os.Stderr, journal))
	return journal, func() error {
		restore()
		return journal.Close()
	}, nil
}

// OpenPersistent mirrors all structured Client and embedded-scanner logs to
// ~/cineko/client.log while retaining stderr for local terminal diagnostics.
func OpenPersistent(dataDir string) (func() error, error) {
	_, closeJournal, err := OpenPersistentJournal(dataDir)
	return closeJournal, err
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

func Debug(ctx context.Context, event string, args ...any) {
	logEvent(ctx, slog.LevelDebug, event, args...)
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
	status       int
	bytes        int64
	wroteHeader  bool
	body         boundedCapture
	artifactBody io.Writer
	artifactErr  error
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

type stagedBody struct {
	file      *os.File
	path      string
	cleanupFn func()
	writeErr  error
}

func newStagedBody(store *networkcapture.Store) stagedBody {
	if store == nil {
		return stagedBody{}
	}
	file, cleanup, err := store.NewStagingFile()
	if err != nil {
		Error(context.Background(), "network.capture.staging.failed", "event", "network.capture.staging.failed", "error", err)
		return stagedBody{writeErr: err}
	}
	return stagedBody{file: file, path: file.Name(), cleanupFn: cleanup}
}

func (body *stagedBody) close() {
	if body == nil || body.file == nil {
		return
	}
	body.writeErr = errors.Join(body.writeErr, body.file.Close())
	body.file = nil
}

func (body stagedBody) cleanup() {
	if body.cleanupFn != nil {
		body.cleanupFn()
	}
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
	if recorder.artifactBody != nil && recorder.artifactErr == nil {
		_, recorder.artifactErr = recorder.artifactBody.Write(contents)
	}
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
		if request.Header == nil {
			request.Header = make(http.Header)
		}
		request.Header.Set(RequestIDHeader, requestID)
		writer.Header().Set(RequestIDHeader, requestID)
		// Observability reads must not record themselves. The operations screen
		// polls these routes, and capturing those successful reads creates an
		// unbounded feedback loop of artifacts and access logs.
		if request.URL.Path == "/api/logs" || strings.HasPrefix(request.URL.Path, "/api/logs/network") ||
			request.URL.Path == "/api/logs/client" {
			next.ServeHTTP(writer, request)
			return
		}
		captureStore := networkStore.Load()
		requestArtifact := stagedBody{}
		responseArtifact := stagedBody{}
		if captureStore != nil && captureStore.DebugEnabled() {
			requestArtifact = newStagedBody(captureStore)
			responseArtifact = newStagedBody(captureStore)
		}
		defer requestArtifact.cleanup()
		defer responseArtifact.cleanup()
		requestBody := &boundedCapture{}
		if request.Body != nil {
			captureWriter := io.Writer(requestBody)
			if requestArtifact.file != nil {
				captureWriter = io.MultiWriter(requestBody, requestArtifact.file)
			}
			request.Body = &captureReadCloser{
				reader: io.TeeReader(request.Body, captureWriter),
				closer: request.Body,
			}
		}
		recorder := &responseRecorder{ResponseWriter: writer}
		if responseArtifact.file != nil {
			recorder.artifactBody = responseArtifact.file
		}
		started := time.Now()
		next.ServeHTTP(recorder, request)
		if request.Body != nil {
			_, err := io.Copy(io.Discard, request.Body)
			requestArtifact.writeErr = errors.Join(requestArtifact.writeErr, err)
		}
		requestArtifact.close()
		responseArtifact.writeErr = errors.Join(responseArtifact.writeErr, recorder.artifactErr)
		responseArtifact.close()
		if recorder.status == 0 {
			recorder.status = http.StatusOK
		}
		// A handler may have written its own value; the inbound correlation ID
		// is authoritative and is restored for clients after the handler runs.
		writer.Header().Set(RequestIDHeader, requestID)
		captureHTTPServerExchange(captureStore, request, recorder, requestID, started, requestArtifact, responseArtifact, requestBody.contents)
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
		Debug(request.Context(), "http.server.request.completed", args...)
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
	if request.Header == nil {
		request.Header = make(http.Header)
	}
	request.Header.Set(RequestIDHeader, requestID)
	started := time.Now()
	captureStore := networkStore.Load()
	requestArtifact := stagedBody{}
	if request.Body != nil && captureStore != nil && captureStore.DebugEnabled() {
		requestArtifact = newStagedBody(captureStore)
		if requestArtifact.file != nil {
			request.Body = &captureReadCloser{reader: io.TeeReader(request.Body, requestArtifact.file), closer: request.Body}
		}
	}
	attemptArgs := []any{
		"request_id", requestID,
		"method", request.Method,
		"route", request.URL.Path,
		"path", request.URL.Path,
	}
	if request.ContentLength >= 0 {
		attemptArgs = append(attemptArgs, "request_bytes", request.ContentLength)
	}
	Debug(requestContext, "http.client.request.attempted", attemptArgs...)

	response, err := transport.base.RoundTrip(request)
	requestArtifact.close()
	if err != nil {
		logClientCompleted(requestContext, request, requestID, "", 0, 0, started, fmt.Sprintf("%+v", err))
		captureHTTPClientExchange(captureStore, request, nil, requestID, started, requestArtifact, stagedBody{}, err)
		requestArtifact.cleanup()
		return nil, err
	}
	if response == nil {
		err = errors.New("HTTP transport returned a nil response")
		logClientCompleted(requestContext, request, requestID, "", 0, 0, started, err.Error())
		captureHTTPClientExchange(captureStore, request, nil, requestID, started, requestArtifact, stagedBody{}, err)
		requestArtifact.cleanup()
		return nil, err
	}
	upstreamRequestID := strings.TrimSpace(response.Header.Get(RequestIDHeader))
	if response.Body == nil {
		errorText := ""
		var captureErr error
		if response.StatusCode >= http.StatusBadRequest {
			errorText = http.StatusText(response.StatusCode)
			captureErr = errors.New(errorText)
		}
		logClientCompleted(requestContext, request, requestID, upstreamRequestID, response.StatusCode, 0, started, errorText)
		captureHTTPClientExchange(captureStore, request, response, requestID, started, requestArtifact, stagedBody{}, captureErr)
		requestArtifact.cleanup()
		return response, nil
	}
	responseArtifact := stagedBody{}
	if captureStore != nil && (captureStore.DebugEnabled() || response.StatusCode >= http.StatusBadRequest) {
		responseArtifact = newStagedBody(captureStore)
	}
	response.Body = &responseBody{
		ReadCloser: response.Body,
		context:    requestContext,
		request:    request,
		requestID:  requestID,
		upstreamID: upstreamRequestID,
		status:     response.StatusCode,
		started:    started,
		response:   response, store: captureStore,
		requestArtifact: requestArtifact, responseArtifact: responseArtifact,
	}
	return response, nil
}

type responseBody struct {
	io.ReadCloser
	context          context.Context
	request          *http.Request
	requestID        string
	upstreamID       string
	status           int
	started          time.Time
	bytes            int64
	once             sync.Once
	readError        error
	response         *http.Response
	store            *networkcapture.Store
	requestArtifact  stagedBody
	responseArtifact stagedBody
}

func (body *responseBody) Read(contents []byte) (int, error) {
	count, err := body.ReadCloser.Read(contents)
	body.bytes += int64(count)
	if count > 0 && body.responseArtifact.file != nil && body.responseArtifact.writeErr == nil {
		_, body.responseArtifact.writeErr = body.responseArtifact.file.Write(contents[:count])
	}
	if err != nil {
		if err != io.EOF {
			body.readError = err
		}
		body.complete()
	}
	return count, err
}

func (body *responseBody) Close() error {
	if body.readError == nil {
		_, body.readError = io.Copy(io.Discard, body)
		if errors.Is(body.readError, io.EOF) {
			body.readError = nil
		}
	}
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
		body.requestArtifact.close()
		body.responseArtifact.close()
		captureHTTPClientExchange(body.store, body.request, body.response, body.requestID, body.started,
			body.requestArtifact, body.responseArtifact, body.readError)
		body.requestArtifact.cleanup()
		body.responseArtifact.cleanup()
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
	Debug(ctx, "http.client.request.completed", args...)
}

func captureHTTPServerExchange(
	store *networkcapture.Store,
	request *http.Request,
	recorder *responseRecorder,
	requestID string,
	started time.Time,
	requestBody stagedBody,
	responseBody stagedBody,
	requestContents []byte,
) {
	if store == nil || request == nil || recorder == nil {
		return
	}
	if !store.DebugEnabled() && recorder.status < http.StatusBadRequest {
		return
	}
	captureErr := errors.Join(requestBody.writeErr, responseBody.writeErr)
	if recorder.status >= http.StatusBadRequest {
		captureErr = errors.Join(captureErr, fmt.Errorf("HTTP %d", recorder.status))
	}
	outcome := "succeeded"
	errorText := ""
	if captureErr != nil {
		outcome = "failed"
		errorText = captureErr.Error()
	}
	responseHeaders := recorder.Header().Clone()
	record := networkcapture.Record{
		Exchange: networkcapture.Exchange{
			CorrelationID: requestID, Service: "client", Scenario: "webui", Transport: "http_server",
			StartedAt: started, CompletedAt: time.Now(), Outcome: outcome, Error: errorText,
			Request: networkcapture.Request{
				Method: request.Method, URL: absoluteRequestURL(request), Headers: httpHeaders(request.Header),
				Bytes: max(request.ContentLength, 0),
			},
			Response: &networkcapture.Response{
				Status: recorder.status, StatusText: http.StatusText(recorder.status), Protocol: request.Proto,
				Headers: httpHeaders(responseHeaders), Bytes: recorder.bytes,
			},
		},
		RequestBodyPath: requestBody.path, RequestContentType: request.Header.Get("Content-Type"), RequestRepresentation: "application",
		ResponseBodyPath: responseBody.path, ResponseContentType: responseHeaders.Get("Content-Type"), ResponseRepresentation: "application",
	}
	if requestBody.path == "" && recorder.status >= http.StatusBadRequest {
		record.RequestBody = append([]byte(nil), requestContents...)
	}
	if responseBody.path == "" && recorder.status >= http.StatusBadRequest {
		record.ResponseBody = append([]byte(nil), recorder.body.contents...)
	}
	if _, err := store.Save(context.WithoutCancel(request.Context()), record); err != nil {
		ErrorUnexpected(request.Context(), "network.capture.save.failed", "network", "capture_http_server_exchange",
			"complete HTTP server request and response artifact", "network artifact write failed", err,
			"request_id", requestID, "method", request.Method, "request_url", record.Request.URL)
	}
}

//nolint:gocyclo,cyclop // One immutable capture aggregates all optional HTTP request and response evidence.
func captureHTTPClientExchange(
	store *networkcapture.Store,
	request *http.Request,
	response *http.Response,
	requestID string,
	started time.Time,
	requestBody stagedBody,
	responseBody stagedBody,
	rawErr error,
) {
	if store == nil || request == nil {
		return
	}
	if !store.DebugEnabled() && rawErr == nil && response != nil && response.StatusCode < http.StatusBadRequest {
		return
	}
	captureErr := errors.Join(rawErr, requestBody.writeErr, responseBody.writeErr)
	if response != nil && response.StatusCode >= http.StatusBadRequest {
		captureErr = errors.Join(captureErr, fmt.Errorf("HTTP %d", response.StatusCode))
	}
	outcome, errorText := "succeeded", ""
	if captureErr != nil {
		outcome, errorText = "failed", captureErr.Error()
	}
	record := networkcapture.Record{
		Exchange: networkcapture.Exchange{
			CorrelationID: requestID, Service: "client", Scenario: "http_client", Transport: "go_http",
			StartedAt: started, CompletedAt: time.Now(), Outcome: outcome, Error: errorText,
			Request: networkcapture.Request{
				Method: request.Method, URL: request.URL.String(), Headers: httpHeaders(request.Header),
				Bytes: max(request.ContentLength, 0),
			},
		},
		RequestBodyPath: requestBody.path, RequestContentType: request.Header.Get("Content-Type"), RequestRepresentation: "application",
		ResponseBodyPath: responseBody.path, ResponseRepresentation: "application",
	}
	if response != nil {
		representation := "encoded"
		if response.Uncompressed {
			representation = "decoded"
		}
		record.ResponseRepresentation = representation
		record.ResponseContentType = response.Header.Get("Content-Type")
		record.Response = &networkcapture.Response{
			Status: response.StatusCode, StatusText: http.StatusText(response.StatusCode), Protocol: response.Proto,
			Headers: httpHeaders(response.Header), Bytes: max(response.ContentLength, 0),
		}
		if response.StatusCode >= http.StatusBadRequest && requestBody.path == "" && request.GetBody != nil {
			body, err := request.GetBody()
			if err != nil {
				record.CaptureError = combinedCaptureError(record.CaptureError, err)
			} else {
				contents, readErr := io.ReadAll(body)
				closeErr := body.Close()
				record.RequestBody = contents
				if combined := errors.Join(readErr, closeErr); combined != nil {
					record.CaptureError = combinedCaptureError(record.CaptureError, combined)
				}
			}
		}
	}
	if _, err := store.Save(context.WithoutCancel(request.Context()), record); err != nil {
		ErrorUnexpected(request.Context(), "network.capture.save.failed", "network", "capture_http_client_exchange",
			"complete HTTP client request and response artifact", "network artifact write failed", err,
			"request_id", requestID, "method", request.Method, "request_url", record.Request.URL)
	}
}

func combinedCaptureError(current string, err error) string {
	if err == nil {
		return current
	}
	if strings.TrimSpace(current) == "" {
		return err.Error()
	}
	return errors.Join(errors.New(current), err).Error()
}

func httpHeaders(headers http.Header) []networkcapture.Header {
	result := make([]networkcapture.Header, 0, len(headers))
	for name, values := range headers {
		for _, value := range values {
			result = append(result, networkcapture.Header{Name: name, Value: value})
		}
	}
	return result
}

func absoluteRequestURL(request *http.Request) string {
	if request == nil || request.URL == nil {
		return ""
	}
	copy := *request.URL
	if copy.Scheme == "" {
		copy.Scheme = "http"
		if request.TLS != nil {
			copy.Scheme = "https"
		}
	}
	if copy.Host == "" {
		copy.Host = request.Host
	}
	return copy.String()
}
