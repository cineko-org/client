package centralhttp

import (
	"context"
	"errors"
	"net"
	"testing"

	clientpb "github.com/cineko-org/contracts/gen/go/cineko/client"
	servicepb "github.com/cineko-org/contracts/gen/go/cineko/service"
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
	control := []byte(protoJSON(t, servicepb.StreamEventsResponse_builder{Control: clientpb.StreamControl_builder{
		ReleaseGeneration: int64Pointer(7),
		Ready:             clientpb.StreamReady_builder{Cursor: int64Pointer(0)}.Build(),
	}.Build()}.Build()))
	if err := store.consumeSSEEvent(sseEvent{type_: "cineko.control", data: control}); err != nil {
		t.Fatal(err)
	}
	select {
	case <-store.ExecutionReady():
	default:
		t.Fatal("stream readiness did not wake the execution worker after reconnect")
	}
	resource := []byte(protoJSON(t, servicepb.StreamEventsResponse_builder{Data: clientpb.ClientEvent_builder{
		Sequence: int64Pointer(1), Id: stringPointer("event"),
		Upserted: clientpb.EventResource_builder{
			Id: stringPointer("monitor"), Revision: int64Pointer(2), Monitor: clientpb.Monitor_builder{}.Build(),
		}.Build(),
	}.Build()}.Build()))
	if err := store.consumeSSEEvent(sseEvent{id: 1, type_: "monitor.updated", data: resource}); err != nil {
		t.Fatal(err)
	}
	if store.eventCursor.Load() != 1 {
		t.Fatalf("event cursor = %d", store.eventCursor.Load())
	}
	reset := []byte(protoJSON(t, servicepb.StreamEventsResponse_builder{Control: clientpb.StreamControl_builder{
		ReleaseGeneration: int64Pointer(7),
		RetentionGap:      clientpb.RetentionGap_builder{Cursor: int64Pointer(9)}.Build(),
	}.Build()}.Build()))
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
		payload := []byte(protoJSON(t, servicepb.StreamEventsResponse_builder{Data: clientpb.ClientEvent_builder{
			Sequence: int64Pointer(sequence), Id: stringPointer("event"),
			ExecutionReady: clientpb.ExecutionReady_builder{
				CommandId: stringPointer("command"), MonitorId: stringPointer("monitor"), Reason: stringPointer("updated"),
			}.Build(),
		}.Build()}.Build()))
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

func TestEventStreamControlFailuresAreNotRetried(t *testing.T) {
	if isRetryableEventStreamError(errors.New("invalid control")) {
		t.Fatal("control failure is retryable")
	}
	if !isRetryableEventStreamError(eventStreamTransportError{err: errors.New("offline")}) {
		t.Fatal("transport failure is not retryable")
	}
	if !isRetryableEventStreamError(eventStreamHTTPError{status: 503}) ||
		isRetryableEventStreamError(eventStreamHTTPError{status: 409}) {
		t.Fatal("HTTP retry classification is wrong")
	}
}
