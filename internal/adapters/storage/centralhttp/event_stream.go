package centralhttp

import (
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"

	central "github.com/cineko-org/contracts/v3"
)

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
	switch event.type_ {
	case "cineko.control":
		return store.consumeSSEControl(event)
	case "":
		return errors.New("central event stream event type is missing")
	default:
		return store.consumeSSEResource(event)
	}
}

func (store *Store) consumeSSEControl(event sseEvent) error {
	if event.id != 0 {
		return errors.New("central control event must not carry a resource cursor")
	}
	var control central.EventStreamControl
	if err := json.Unmarshal(event.data, &control); err != nil {
		return errors.New("central event stream returned invalid control data")
	}
	if control.Protocol != central.ProtocolVersion || control.ReleaseGeneration < 1 || control.Cursor < 0 {
		return errors.New("central event stream control is incompatible")
	}
	if err := store.observeReleaseGeneration(strconv.FormatInt(control.ReleaseGeneration, 10)); err != nil {
		return err
	}
	return store.applySSEControl(control)
}

func (store *Store) applySSEControl(control central.EventStreamControl) error {
	switch control.Action {
	case central.EventStreamActionReady, central.EventStreamActionHeartbeat:
		if control.Reason != "" || control.Cursor != store.eventCursor.Load() {
			return errors.New("central event stream control cursor is inconsistent")
		}
	case central.EventStreamActionFullResync:
		if control.Reason != central.EventStreamResetRetentionGap &&
			control.Reason != central.EventStreamResetInvalidCursor {
			return errors.New("central event stream reset reason is invalid")
		}
		store.eventCursor.Store(control.Cursor)
		store.resyncOnce.Do(func() { close(store.resyncRequired) })
		store.signalResourceChanged()
	default:
		return fmt.Errorf("central event stream action %q is unsupported", control.Action)
	}
	return nil
}

func (store *Store) consumeSSEResource(event sseEvent) error {
	if event.id < 1 || len(event.data) == 0 {
		return errors.New("central event stream resource event is incomplete")
	}
	var payload central.ClientEvent
	if err := json.Unmarshal(event.data, &payload); err != nil || !validSSEPayload(payload, event) {
		return errors.New("central event stream resource event is invalid")
	}
	store.eventCursor.Store(event.id)
	store.signalResourceChanged()
	return nil
}

func validSSEPayload(payload central.ClientEvent, event sseEvent) bool {
	return payload.Sequence == event.id && payload.Type == event.type_ &&
		strings.TrimSpace(payload.ID) != "" && strings.TrimSpace(payload.Resource.Kind) != "" &&
		strings.TrimSpace(payload.Resource.ID) != "" && payload.Resource.Revision >= 1 && json.Valid(payload.Data)
}

func (store *Store) signalResourceChanged() {
	select {
	case store.resourceChanged <- struct{}{}:
	default:
	}
}
