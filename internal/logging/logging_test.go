package logging

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestHTTPMiddlewareCorrelatesRequestAndResponse(t *testing.T) {
	var output bytes.Buffer
	restore := SetOutput(&output)
	defer restore()

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
	if len(events) != 2 || events[1]["msg"] != "http.client.request.completed" || events[1]["error"] != want.Error() || events[1]["status"] != float64(0) {
		t.Fatalf("failed client events = %#v", events)
	}
	if events[1]["level"] != "ERROR" {
		t.Fatalf("failed client level = %#v", events[1]["level"])
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
