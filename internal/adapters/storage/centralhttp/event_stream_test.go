package centralhttp

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"testing"

	central "github.com/cineko-org/contracts/v3"
)

func TestExecutionClaimRetryClassification(t *testing.T) {
	store := &Store{}
	if !store.ExecutionClaimRetryable(centralAPIError{status: 503}) ||
		!store.ExecutionClaimRetryable(&net.DNSError{Err: "temporary", IsTemporary: true}) ||
		!store.ExecutionClaimRetryable(context.DeadlineExceeded) ||
		!store.ExecutionClaimRetryable(context.Canceled) {
		t.Fatal("retryable claim failure was classified as terminal")
	}
	if store.ExecutionClaimRetryable(errCentralUnauthorized) ||
		store.ExecutionClaimRetryable(centralAPIError{status: 400, code: "invalid_request"}) {
		t.Fatal("terminal claim failure was classified as retryable")
	}
}

func TestSSEParserRequiresMonotonicCursor(t *testing.T) {
	parser := newSSEParser(4)
	for _, line := range []string{"id: 5", "event: monitor.updated", `data: {"ok":true}`} {
		if _, complete, err := parser.Consume(line); err != nil || complete {
			t.Fatalf("consume %q = complete %v, error %v", line, complete, err)
		}
	}
	event, complete, err := parser.Consume("")
	if err != nil || !complete || event.id != 5 || event.type_ != "monitor.updated" {
		t.Fatalf("parsed event = %+v, complete %v, error %v", event, complete, err)
	}
	if _, _, err := parser.Consume("id: 5"); err != nil {
		t.Fatal(err)
	}
	if _, _, err := parser.Consume(""); err == nil {
		t.Fatal("duplicate event cursor accepted")
	}
}

func TestStoreConsumesTypedEventStream(t *testing.T) {
	store, err := newStore("http://localhost", "user", nil)
	if err != nil {
		t.Fatal(err)
	}
	store.releaseGeneration.Store(7)
	control, _ := json.Marshal(central.EventStreamControl{
		Protocol: central.ProtocolVersion, ReleaseGeneration: 7,
		Cursor: 0, Action: central.EventStreamActionReady,
	})
	if err := store.consumeSSEEvent(sseEvent{type_: "cineko.control", data: control}); err != nil {
		t.Fatal(err)
	}
	select {
	case <-store.ExecutionReady():
	default:
		t.Fatal("stream readiness did not wake the execution worker after reconnect")
	}
	resource, _ := json.Marshal(central.ClientEvent{
		Sequence: 1, ID: "event", Type: "monitor.updated",
		Resource: central.EventResource{Kind: "monitors", ID: "monitor", Revision: 2},
		Data:     json.RawMessage(`{"id":"monitor"}`),
	})
	if err := store.consumeSSEEvent(sseEvent{id: 1, type_: "monitor.updated", data: resource}); err != nil {
		t.Fatal(err)
	}
	if store.eventCursor.Load() != 1 {
		t.Fatalf("event cursor = %d", store.eventCursor.Load())
	}
	reset, _ := json.Marshal(central.EventStreamControl{
		Protocol: central.ProtocolVersion, ReleaseGeneration: 7,
		Cursor: 9, Action: central.EventStreamActionFullResync, Reason: central.EventStreamResetRetentionGap,
	})
	if err := store.consumeSSEEvent(sseEvent{type_: "cineko.control", data: reset}); err != nil {
		t.Fatal(err)
	}
	select {
	case <-store.ResyncRequired():
	default:
		t.Fatal("full resync was not surfaced")
	}
}

func TestExecutionReadyEventIsBufferedAndCoalesced(t *testing.T) {
	store, err := newStore("http://localhost", "user", nil)
	if err != nil {
		t.Fatal(err)
	}
	for sequence := int64(1); sequence <= 2; sequence++ {
		payload, _ := json.Marshal(central.ClientEvent{
			Sequence: sequence, ID: "event", Type: executionReadyEventType,
			Resource: central.EventResource{Kind: "executions", ID: "execution", Revision: sequence},
			Data:     json.RawMessage(`{"id":"execution"}`),
		})
		if err := store.consumeSSEEvent(sseEvent{id: sequence, type_: executionReadyEventType, data: payload}); err != nil {
			t.Fatal(err)
		}
	}
	select {
	case <-store.ExecutionReady():
	default:
		t.Fatal("execution event sent before waiter was attached was lost")
	}
	select {
	case <-store.ExecutionReady():
		t.Fatal("duplicate execution events were not coalesced")
	default:
	}
}

func TestEventStreamProtocolFailuresAreNotRetried(t *testing.T) {
	if isRetryableEventStreamError(errors.New("invalid protocol")) {
		t.Fatal("protocol failure is retryable")
	}
	if !isRetryableEventStreamError(eventStreamTransportError{err: errors.New("offline")}) {
		t.Fatal("transport failure is not retryable")
	}
	if !isRetryableEventStreamError(eventStreamHTTPError{status: 503}) ||
		isRetryableEventStreamError(eventStreamHTTPError{status: 409}) {
		t.Fatal("HTTP retry classification is wrong")
	}
}
