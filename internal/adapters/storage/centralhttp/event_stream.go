package centralhttp

import (
	"errors"
	"fmt"
	"strconv"
	"strings"

	"buf.build/go/protovalidate"
	clientpb "github.com/cineko-org/contracts/v3/gen/go/cineko/client"
	servicepb "github.com/cineko-org/contracts/v3/gen/go/cineko/service"
	"google.golang.org/protobuf/encoding/protojson"
)

const executionReadyEventType = "execution.ready"

type sseEvent struct {
	id    int64
	type_ string
	data  []byte
}

type sseParser struct {
	lastID    int64
	id        string
	eventType string
	data      []string
}

func newSSEParser(lastID int64) *sseParser { return &sseParser{lastID: lastID} }

func (parser *sseParser) Consume(line string) (sseEvent, bool, error) {
	if line == "" {
		if parser.eventType == "" && len(parser.data) == 0 && parser.id == "" {
			return sseEvent{}, false, nil
		}
		event := sseEvent{type_: parser.eventType, data: []byte(strings.Join(parser.data, "\n"))}
		if parser.id != "" {
			id, err := strconv.ParseInt(parser.id, 10, 64)
			if err != nil || id <= parser.lastID {
				return sseEvent{}, false, errors.New("central event stream returned a non-monotonic event ID")
			}
			event.id = id
			parser.lastID = id
		}
		parser.id, parser.eventType, parser.data = "", "", nil
		return event, true, nil
	}
	if strings.HasPrefix(line, ":") {
		return sseEvent{}, false, nil
	}
	field, value, _ := strings.Cut(line, ":")
	value = strings.TrimPrefix(value, " ")
	switch field {
	case "id":
		if strings.ContainsAny(value, "\x00\r\n") {
			return sseEvent{}, false, errors.New("central event stream returned an invalid event ID")
		}
		parser.id = value
	case "event":
		parser.eventType = value
	case "data":
		parser.data = append(parser.data, value)
	case "retry":
		// Reconnect behavior is Client-owned and bounded.
	}
	return sseEvent{}, false, nil
}

func (store *Store) consumeSSEEvent(event sseEvent) error {
	if event.type_ == "" {
		return errors.New("central event stream event type is missing")
	}
	response := &servicepb.StreamEventsResponse{}
	if err := (protojson.UnmarshalOptions{DiscardUnknown: false}).Unmarshal(event.data, response); err != nil {
		return errors.New("central event stream returned invalid response data")
	}
	if err := protovalidate.Validate(response); err != nil {
		return errors.New("central event stream response violates the contract")
	}
	if control := response.GetControl(); control != nil {
		return store.consumeSSEControl(event, control)
	}
	if payload := response.GetData(); payload != nil {
		return store.consumeSSEResource(event, payload)
	}
	return errors.New("central event stream response is empty")
}

func (store *Store) consumeSSEControl(event sseEvent, control *clientpb.StreamControl) error {
	if event.id != 0 {
		return errors.New("central control event must not carry a resource cursor")
	}
	if event.type_ != "cineko.control" {
		return errors.New("central event stream control type is invalid")
	}
	if control.GetReleaseGeneration() < 1 || !control.HasControl() {
		return errors.New("central event stream control is incompatible")
	}
	if err := store.observeReleaseGeneration(strconv.FormatInt(control.GetReleaseGeneration(), 10)); err != nil {
		return err
	}
	return store.applySSEControl(control)
}

func (store *Store) applySSEControl(control *clientpb.StreamControl) error {
	switch {
	case control.GetReady() != nil, control.GetHeartbeat() != nil:
		cursor := int64(-1)
		if ready := control.GetReady(); ready != nil {
			cursor = ready.GetCursor()
		}
		if heartbeat := control.GetHeartbeat(); heartbeat != nil {
			cursor = heartbeat.GetCursor()
		}
		if cursor < 0 || cursor != store.eventCursor.Load() {
			return errors.New("central event stream control cursor is inconsistent")
		}
		if control.GetReady() != nil {
			store.signalExecutionReady()
		}
	case control.GetRetentionGap() != nil, control.GetInvalidCursor() != nil:
		cursor := int64(-1)
		if reset := control.GetRetentionGap(); reset != nil {
			cursor = reset.GetCursor()
		}
		if reset := control.GetInvalidCursor(); reset != nil {
			cursor = reset.GetCursor()
		}
		if cursor < 0 {
			return errors.New("central event stream reset cursor is invalid")
		}
		store.eventCursor.Store(cursor)
		store.resyncOnce.Do(func() { close(store.resyncRequired) })
		store.signalResourceChanged()
	default:
		return fmt.Errorf("central event stream control %q is unsupported", control.WhichControl())
	}
	return nil
}

func (store *Store) consumeSSEResource(event sseEvent, payload *clientpb.ClientEvent) error {
	if event.id < 1 || len(event.data) == 0 {
		return errors.New("central event stream resource event is incomplete")
	}
	if !validSSEPayload(payload, event) {
		return errors.New("central event stream resource event is invalid")
	}
	store.eventCursor.Store(event.id)
	store.signalResourceChanged()
	if payload.GetExecutionReady() != nil {
		store.signalExecutionReady()
	}
	return nil
}

func validSSEPayload(payload *clientpb.ClientEvent, event sseEvent) bool {
	if payload.GetSequence() != event.id || strings.TrimSpace(payload.GetId()) == "" || strings.TrimSpace(event.type_) == "" || !payload.HasEvent() {
		return false
	}
	if resource := payload.GetUpserted(); resource != nil {
		return strings.TrimSpace(resource.GetId()) != "" && resource.GetRevision() >= 1 && resource.HasKind()
	}
	if deleted := payload.GetDeleted(); deleted != nil {
		return strings.TrimSpace(deleted.GetId()) != "" && deleted.GetRevision() >= 1 && deleted.GetKind() != nil
	}
	ready := payload.GetExecutionReady()
	return event.type_ == executionReadyEventType && ready != nil &&
		strings.TrimSpace(ready.GetCommandId()) != "" && strings.TrimSpace(ready.GetMonitorId()) != "" &&
		strings.TrimSpace(ready.GetReason()) != ""
}

func (store *Store) signalResourceChanged() {
	select {
	case store.resourceChanged <- struct{}{}:
	default:
	}
}

func (store *Store) signalExecutionReady() {
	if store.executionReady == nil {
		return
	}
	select {
	case store.executionReady <- struct{}{}:
	default:
	}
}
