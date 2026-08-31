package logging

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/cineko-org/probe/v2/networkcapture"
)

func TestHTTPMiddlewareCorrelatesRequestAndResponse(t *testing.T) {
	var output bytes.Buffer
	restore := SetOutput(&output)
	defer restore()
	restoreDebug := SetDebug(true)
	defer restoreDebug()

	requestID := "request-from-browser"
	handler := HTTPMiddleware(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.Header.Get(RequestIDHeader) != requestID || RequestID(request.Context()) != requestID {
			t.Fatalf("request ID was not propagated: header=%q context=%q", request.Header.Get(RequestIDHeader), RequestID(request.Context()))
		}
		_, _ = io.WriteString(writer, "ok")
	}))
	request := httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/api/presets?ignored=user", strings.NewReader("body"))
	request.Header.Set(RequestIDHeader, requestID)
	recorder := httptest.NewRecorder()

	handler.ServeHTTP(recorder, request)
	if recorder.Header().Get(RequestIDHeader) != requestID {
		t.Fatalf("response request ID = %q", recorder.Header().Get(RequestIDHeader))
	}
	if recorder.Code != http.StatusOK || recorder.Body.String() != "ok" {
		t.Fatalf("response = %d/%q", recorder.Code, recorder.Body.String())
	}
	event := decodeEvent(t, output.String())
	if event["service"] != "client" || event["msg"] != "http.server.request.completed" ||
		event["request_id"] != requestID || event["method"] != http.MethodPost ||
		event["route"] != "/api/presets" || event["status"] != float64(http.StatusOK) || event["response_bytes"] != float64(2) {
		t.Fatalf("server event = %#v", event)
	}
}

func TestHTTPMiddlewareLogsFailedRequestAndResponseBodies(t *testing.T) {
	var output bytes.Buffer
	restore := SetOutput(&output)
	defer restore()

	handler := HTTPMiddleware(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if _, err := io.ReadAll(request.Body); err != nil {
			t.Fatal(err)
		}
		writer.WriteHeader(http.StatusBadRequest)
		_, _ = io.WriteString(writer, `{"error":{"message":"contract failed"}}`)
	}))
	request := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/api/presets", strings.NewReader(`{"mutation":{}}`))
	recorder := httptest.NewRecorder()

	handler.ServeHTTP(recorder, request)
	event := decodeEvent(t, output.String())
	if event["level"] != "ERROR" || event["request_body"] != `{"mutation":{}}` ||
		event["response_body"] != `{"error":{"message":"contract failed"}}` {
		t.Fatalf("failed server event = %#v", event)
	}
}

func TestHTTPMiddlewarePersistsCompleteRequestAndResponseArtifact(t *testing.T) {
	store, err := networkcapture.NewStore(t.TempDir(), nil)
	if err != nil {
		t.Fatal(err)
	}
	restoreCapture := SetNetworkCapture(store)
	defer restoreCapture()
	handler := HTTPMiddleware(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if _, err := io.ReadAll(request.Body); err != nil {
			t.Fatal(err)
		}
		writer.Header().Add("Retry-After", "60")
		writer.Header().Set("Content-Type", "application/json")
		writer.WriteHeader(http.StatusTooManyRequests)
		_, _ = io.WriteString(writer, `{"error":"limited"}`)
	}))
	request := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "http://localhost/api/test?round=1", strings.NewReader(`{"date":"2026-08-29"}`))
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)

	entries, err := networkcapture.List(store.Root(), networkcapture.Query{Status: http.StatusTooManyRequests})
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 {
		t.Fatalf("network entries = %+v", entries)
	}
	exchange, err := networkcapture.ReadExchange(store.Root(), entries[0].ID)
	if err != nil {
		t.Fatal(err)
	}
	if exchange.Request.URL != "http://localhost/api/test?round=1" || exchange.Response == nil || exchange.Response.Status != 429 {
		t.Fatalf("exchange = %+v", exchange)
	}
	requestPath, _, err := networkcapture.BodyPath(store.Root(), exchange, "request")
	if err != nil {
		t.Fatal(err)
	}
	responsePath, _, err := networkcapture.BodyPath(store.Root(), exchange, "response")
	if err != nil {
		t.Fatal(err)
	}
	requestBody, requestErr := os.ReadFile(requestPath)    // #nosec G304 -- BodyPath validates the temporary capture root.
	responseBody, responseErr := os.ReadFile(responsePath) // #nosec G304 -- BodyPath validates the temporary capture root.
	if requestErr != nil || responseErr != nil || string(requestBody) != `{"date":"2026-08-29"}` || string(responseBody) != `{"error":"limited"}` {
		t.Fatalf("artifact bodies = %q/%q, errors = %v/%v", requestBody, responseBody, requestErr, responseErr)
	}
}

func TestHTTPMiddlewareDoesNotCaptureObservabilityReads(t *testing.T) {
	var output bytes.Buffer
	restoreOutput := SetOutput(&output)
	defer restoreOutput()
	store, err := networkcapture.NewStore(t.TempDir(), nil)
	if err != nil {
		t.Fatal(err)
	}
	restoreCapture := SetNetworkCapture(store)
	defer restoreCapture()
	handler := HTTPMiddleware(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if RequestID(request.Context()) == "" {
			t.Fatal("request ID was not propagated")
		}
		_, _ = io.WriteString(writer, `{"entries":[]}`)
	}))
	request := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "http://wails/api/logs/network?outcome=failed", nil)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)

	if response.Code != http.StatusOK || response.Header().Get(RequestIDHeader) == "" {
		t.Fatalf("response = %d, request id = %q", response.Code, response.Header().Get(RequestIDHeader))
	}
	if output.Len() != 0 {
		t.Fatalf("observability read emitted an access log: %s", output.String())
	}
	entries, err := networkcapture.List(store.Root(), networkcapture.Query{})
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatalf("observability read captured network artifacts: %+v", entries)
	}
}

func TestLoggerCarriesClientServiceForEmbeddedRuntimes(t *testing.T) {
	var output bytes.Buffer
	restore := SetOutput(&output)
	defer restore()

	Logger().Info("probe.lifecycle", "phase", "ready")
	event := decodeEvent(t, output.String())
	if event["service"] != "client" || event["msg"] != "probe.lifecycle" || event["phase"] != "ready" {
		t.Fatalf("embedded runtime event = %#v", event)
	}
}

func TestRoundTripperAddsRequestIDAndLogsResponseBytes(t *testing.T) {
	var output bytes.Buffer
	restore := SetOutput(&output)
	defer restore()
	restoreDebug := SetDebug(true)
	defer restoreDebug()

	var receivedRequestID string
	base := roundTripperFunc(func(request *http.Request) (*http.Response, error) {
		receivedRequestID = request.Header.Get(RequestIDHeader)
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     make(http.Header),
			Body:       io.NopCloser(strings.NewReader("catalog")),
			Request:    request,
		}, nil
	})
	client := &http.Client{Transport: RoundTripper(base)}
	request, err := http.NewRequestWithContext(t.Context(), http.MethodGet, "http://local.test/v1/catalog", nil)
	if err != nil {
		t.Fatal(err)
	}
	response, err := client.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := io.ReadAll(response.Body); err != nil {
		t.Fatal(err)
	}
	if err := response.Body.Close(); err != nil {
		t.Fatal(err)
	}
	if receivedRequestID == "" {
		t.Fatal("transport did not add a request ID")
	}
	events := decodeEvents(t, output.String())
	if len(events) != 2 {
		t.Fatalf("events = %#v", events)
	}
	if events[0]["msg"] != "http.client.request.attempted" || events[1]["msg"] != "http.client.request.completed" {
		t.Fatalf("client events = %#v", events)
	}
	if events[1]["request_id"] != receivedRequestID || events[1]["path"] != "/v1/catalog" || events[1]["response_bytes"] != float64(len("catalog")) {
		t.Fatalf("completed client event = %#v", events[1])
	}
}

func TestRoundTripperLogsRawTransportError(t *testing.T) {
	var output bytes.Buffer
	restore := SetOutput(&output)
	defer restore()

	want := errors.New("dial tcp local.test: connection refused")
	client := &http.Client{Transport: RoundTripper(roundTripperFunc(func(*http.Request) (*http.Response, error) {
		return nil, want
	}))}
	request, err := http.NewRequestWithContext(t.Context(), http.MethodGet, "http://local.test/v1/catalog", nil)
	if err != nil {
		t.Fatal(err)
	}
	response, err := client.Do(request)
	if response != nil {
		if closeErr := response.Body.Close(); closeErr != nil {
			t.Fatal(closeErr)
		}
	}
	if !errors.Is(err, want) {
		t.Fatalf("error = %v", err)
	}
	events := decodeEvents(t, output.String())
	if len(events) != 1 || events[0]["msg"] != "http.client.request.completed" || events[0]["error"] != want.Error() || events[0]["status"] != float64(0) {
		t.Fatalf("failed client events = %#v", events)
	}
	if events[0]["level"] != "ERROR" {
		t.Fatalf("failed client level = %#v", events[0]["level"])
	}
}

func TestDefaultModeSuppressesSuccessfulRequestDiagnostics(t *testing.T) {
	var output bytes.Buffer
	restoreOutput := SetOutput(&output)
	defer restoreOutput()
	restoreDebug := SetDebug(false)
	defer restoreDebug()
	store, err := networkcapture.NewStore(t.TempDir(), nil)
	if err != nil {
		t.Fatal(err)
	}
	restoreCapture := SetNetworkCapture(store)
	defer restoreCapture()

	handler := HTTPMiddleware(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(writer, "ok")
	}))
	handler.ServeHTTP(httptest.NewRecorder(), httptest.NewRequestWithContext(t.Context(), http.MethodGet, "http://localhost/api/health", nil))

	if output.Len() != 0 {
		t.Fatalf("default mode emitted successful request logs: %s", output.String())
	}
	entries, err := networkcapture.List(store.Root(), networkcapture.Query{})
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatalf("default mode persisted successful request artifacts: %+v", entries)
	}
}

func TestRoundTripperPersistsCompleteRequestAndResponseArtifact(t *testing.T) {
	store, err := networkcapture.NewStore(t.TempDir(), nil)
	if err != nil {
		t.Fatal(err)
	}
	restoreCapture := SetNetworkCapture(store)
	defer restoreCapture()
	base := roundTripperFunc(func(request *http.Request) (*http.Response, error) {
		if _, err := io.ReadAll(request.Body); err != nil {
			t.Fatal(err)
		}
		return &http.Response{
			StatusCode: http.StatusTooManyRequests, Status: "429 Too Many Requests", Proto: "HTTP/2.0",
			Header: http.Header{"Retry-After": []string{"60"}, "Content-Type": []string{"application/json"}},
			Body:   io.NopCloser(strings.NewReader(`{"error":"limited"}`)), ContentLength: int64(len(`{"error":"limited"}`)),
			Request: request,
		}, nil
	})
	client := &http.Client{Transport: RoundTripper(base)}
	request, err := http.NewRequestWithContext(t.Context(), http.MethodPost, "https://cgv.test/api?date=20260829", strings.NewReader(`{"theater":"0013"}`))
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Content-Type", "application/json")
	response, err := client.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := io.ReadAll(response.Body); err != nil {
		t.Fatal(err)
	}
	if err := response.Body.Close(); err != nil {
		t.Fatal(err)
	}
	entries, err := networkcapture.List(store.Root(), networkcapture.Query{Status: 429})
	if err != nil || len(entries) != 1 {
		t.Fatalf("network entries = %+v, %v", entries, err)
	}
	exchange, err := networkcapture.ReadExchange(store.Root(), entries[0].ID)
	if err != nil {
		t.Fatal(err)
	}
	if exchange.Request.URL != request.URL.String() || exchange.Response == nil || exchange.Response.Protocol != "HTTP/2.0" {
		t.Fatalf("exchange = %+v", exchange)
	}
	requestPath, _, err := networkcapture.BodyPath(store.Root(), exchange, "request")
	if err != nil {
		t.Fatal(err)
	}
	responsePath, _, err := networkcapture.BodyPath(store.Root(), exchange, "response")
	if err != nil {
		t.Fatal(err)
	}
	requestContents, requestErr := os.ReadFile(requestPath)    // #nosec G304 -- BodyPath validates the temporary capture root.
	responseContents, responseErr := os.ReadFile(responsePath) // #nosec G304 -- BodyPath validates the temporary capture root.
	if requestErr != nil || responseErr != nil || string(requestContents) != `{"theater":"0013"}` || string(responseContents) != `{"error":"limited"}` {
		t.Fatalf("artifact bodies = %q/%q, errors = %v/%v", requestContents, responseContents, requestErr, responseErr)
	}
}

func BenchmarkHTTPMiddlewareWithoutNetworkCapture(b *testing.B) {
	benchmarkHTTPMiddleware(b, false, false)
}

func BenchmarkHTTPMiddlewareWithNetworkCapture(b *testing.B) {
	benchmarkHTTPMiddleware(b, true, false)
}

func BenchmarkHTTPMiddlewareWithDebugNetworkCapture(b *testing.B) {
	benchmarkHTTPMiddleware(b, true, true)
}

func benchmarkHTTPMiddleware(b *testing.B, capture bool, debug bool) {
	restoreOutput := SetOutput(io.Discard)
	defer restoreOutput()
	restoreDebug := SetDebug(debug)
	defer restoreDebug()
	var store *networkcapture.Store
	if capture {
		var err error
		store, err = networkcapture.NewStore(b.TempDir(), slog.New(slog.NewTextHandler(io.Discard, nil)), networkcapture.WithDebug(debug))
		if err != nil {
			b.Fatal(err)
		}
	}
	restoreCapture := SetNetworkCapture(store)
	defer restoreCapture()
	payload := strings.Repeat("x", 1024)
	handler := HTTPMiddleware(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		_, _ = io.Copy(io.Discard, request.Body)
		_, _ = io.WriteString(writer, payload)
	}))
	b.ReportAllocs()
	b.SetBytes(int64(len(payload)))
	b.ResetTimer()
	for range b.N {
		request := httptest.NewRequestWithContext(context.Background(), http.MethodPost, "http://localhost/api/fixture", strings.NewReader(payload))
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
	}
}

type roundTripperFunc func(*http.Request) (*http.Response, error)

func (function roundTripperFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return function(request)
}

func decodeEvent(t *testing.T, line string) map[string]any {
	t.Helper()
	events := decodeEvents(t, line)
	if len(events) != 1 {
		t.Fatalf("events = %#v", events)
	}
	return events[0]
}

func decodeEvents(t *testing.T, output string) []map[string]any {
	t.Helper()
	lines := strings.Split(strings.TrimSpace(output), "\n")
	events := make([]map[string]any, 0, len(lines))
	for _, line := range lines {
		if line == "" {
			continue
		}
		var event map[string]any
		if err := json.Unmarshal([]byte(line), &event); err != nil {
			t.Fatalf("decode log %q: %v", line, err)
		}
		events = append(events, event)
	}
	return events
}
