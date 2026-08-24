package logging

import (
	"context"
	"errors"
	"sort"
	"strings"
)

// ClientEvent is the structured warning/error contract shared by the Wails
// bridge and the local Web UI endpoint.
type ClientEvent struct {
	Level     string         `json:"level"`
	Event     string         `json:"event"`
	Scenario  string         `json:"scenario"`
	Operation string         `json:"operation"`
	Expected  string         `json:"expected"`
	Observed  string         `json:"observed"`
	Fields    map[string]any `json:"fields"`
}

func (event ClientEvent) Record(ctx context.Context) error {
	event.Level = strings.ToLower(strings.TrimSpace(event.Level))
	event.Event = strings.TrimSpace(event.Event)
	if event.Event == "" || (event.Level != "warn" && event.Level != "error") {
		return errors.New("client log level must be warn or error and event is required")
	}
	attributes := []any{
		"event", event.Event,
		"scenario", strings.TrimSpace(event.Scenario),
		"operation", strings.TrimSpace(event.Operation),
		"outcome", "unexpected",
		"expected", strings.TrimSpace(event.Expected),
		"observed", strings.TrimSpace(event.Observed),
	}
	fields := make(map[string]any, len(event.Fields))
	keys := make([]string, 0, len(event.Fields))
	for originalKey, value := range event.Fields {
		key := strings.TrimSpace(originalKey)
		if key == "" || reservedClientEventKey(key) {
			continue
		}
		if _, exists := fields[key]; !exists {
			keys = append(keys, key)
		}
		fields[key] = value
	}
	sort.Strings(keys)
	for _, key := range keys {
		attributes = append(attributes, key, fields[key])
	}
	if event.Level == "error" {
		Error(ctx, event.Event, attributes...)
	} else {
		Warn(ctx, event.Event, attributes...)
	}
	return nil
}

func reservedClientEventKey(key string) bool {
	switch key {
	case "time", "level", "msg", "service", "event", "scenario", "operation", "outcome", "expected", "observed":
		return true
	default:
		return false
	}
}
