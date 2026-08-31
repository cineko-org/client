package logging

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"sort"
	"strings"
	"time"
)

const (
	defaultQueryLimit = 200
	maximumQueryLimit = 1000
	maximumScanBytes  = 16 << 20
	maximumLogLine    = 1 << 20
)

type Query struct {
	MinimumLevel slog.Level
	Scenario     string
	Limit        int
	Since        time.Time
}

type Entry struct {
	Sequence  int64          `json:"sequence"`
	Time      string         `json:"time"`
	Level     string         `json:"level"`
	Message   string         `json:"message"`
	Event     string         `json:"event"`
	Scenario  string         `json:"scenario"`
	Operation string         `json:"operation"`
	Outcome   string         `json:"outcome"`
	Expected  string         `json:"expected,omitempty"`
	Observed  string         `json:"observed,omitempty"`
	Error     string         `json:"error,omitempty"`
	RequestID string         `json:"request_id,omitempty"`
	Fields    map[string]any `json:"fields,omitempty"`
}

type Aggregate struct {
	Level     string `json:"level"`
	Event     string `json:"event"`
	Scenario  string `json:"scenario"`
	Operation string `json:"operation"`
	Count     int    `json:"count"`
	LastTime  string `json:"last_time"`
	LastError string `json:"last_error,omitempty"`
}

type Snapshot struct {
	Entries      []Entry     `json:"entries"`
	Aggregates   []Aggregate `json:"aggregates"`
	Matching     int         `json:"matching"`
	Warnings     int         `json:"warnings"`
	Errors       int         `json:"errors"`
	InvalidLines int         `json:"invalid_lines"`
	ScannedBytes int64       `json:"scanned_bytes"`
	Truncated    bool        `json:"truncated"`
	StartedAt    string      `json:"started_at,omitempty"`
}

func ParseMinimumLevel(value string) (slog.Level, error) {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "", "warn", "warning":
		return slog.LevelWarn, nil
	case "debug":
		return slog.LevelDebug, nil
	case "error":
		return slog.LevelError, nil
	case "info":
		return slog.LevelInfo, nil
	default:
		return 0, fmt.Errorf("unsupported log level %q", value)
	}
}

// ReadSnapshot reads only the newest bounded portion of the append-only JSONL
// journal. Entries are returned newest first while aggregates cover every
// matching event in the scanned window, not merely the visible page.
func ReadSnapshot(path string, query Query) (snapshot Snapshot, resultErr error) {
	path = strings.TrimSpace(path)
	if path == "" {
		return Snapshot{}, errors.New("log path is required")
	}
	limit := normalizedQueryLimit(query.Limit)
	file, err := os.Open(path) // #nosec G304 -- path is the application-owned journal configured at startup.
	if errors.Is(err, os.ErrNotExist) {
		return Snapshot{Entries: []Entry{}, Aggregates: []Aggregate{}}, nil
	}
	if err != nil {
		return Snapshot{}, fmt.Errorf("open client log: %w", err)
	}
	defer func() {
		resultErr = errors.Join(resultErr, file.Close())
	}()
	info, err := file.Stat()
	if err != nil {
		return Snapshot{}, fmt.Errorf("stat client log: %w", err)
	}
	offset := boundedLogOffset(info.Size())
	if _, err := file.Seek(offset, io.SeekStart); err != nil {
		return Snapshot{}, fmt.Errorf("seek client log: %w", err)
	}
	return scanSnapshot(file, offset, info.Size(), query, limit)
}

func normalizedQueryLimit(limit int) int {
	if limit <= 0 {
		return defaultQueryLimit
	}
	if limit > maximumQueryLimit {
		return maximumQueryLimit
	}
	return limit
}

func boundedLogOffset(size int64) int64 {
	if size > maximumScanBytes {
		return size - maximumScanBytes
	}
	return 0
}

func scanSnapshot(file *os.File, offset, size int64, query Query, limit int) (Snapshot, error) {
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 64<<10), maximumLogLine)
	if offset > 0 {
		// The bounded tail normally begins in the middle of a JSON object.
		// Discard that fragment before decoding complete JSONL records.
		_ = scanner.Scan()
	}
	snapshot := Snapshot{
		Entries:   make([]Entry, 0, limit),
		Truncated: offset > 0, ScannedBytes: size - offset,
	}
	if !query.Since.IsZero() {
		snapshot.StartedAt = query.Since.Format(time.RFC3339Nano)
	}
	aggregates := make(map[string]*Aggregate)
	scenarioFilter := strings.TrimSpace(query.Scenario)
	var sequence int64
	for scanner.Scan() {
		sequence++
		entry, level, ok := decodeSnapshotEntry(sequence, scanner.Bytes())
		if !ok {
			snapshot.InvalidLines++
			continue
		}
		if !query.Since.IsZero() {
			entryTime, err := time.Parse(time.RFC3339Nano, entry.Time)
			if err != nil || entryTime.Before(query.Since) {
				continue
			}
		}
		if level < query.MinimumLevel || (scenarioFilter != "" && entry.Scenario != scenarioFilter) {
			continue
		}
		recordSnapshotEntry(&snapshot, aggregates, entry, level, limit)
	}
	if err := scanner.Err(); err != nil {
		return Snapshot{}, fmt.Errorf("scan client log: %w", err)
	}
	return finalizeSnapshot(snapshot, aggregates), nil
}

func decodeSnapshotEntry(sequence int64, payload []byte) (Entry, slog.Level, bool) {
	var raw map[string]any
	if err := json.Unmarshal(payload, &raw); err != nil {
		return Entry{}, 0, false
	}
	entry, level := normalizeEntry(sequence, raw)
	return entry, level, true
}

func recordSnapshotEntry(snapshot *Snapshot, aggregates map[string]*Aggregate, entry Entry, level slog.Level, limit int) {
	snapshot.Matching++
	if level >= slog.LevelError {
		snapshot.Errors++
	} else if level >= slog.LevelWarn {
		snapshot.Warnings++
	}
	key := strings.Join([]string{entry.Level, entry.Event, entry.Scenario, entry.Operation}, "\x00")
	aggregate := aggregates[key]
	if aggregate == nil {
		aggregate = &Aggregate{
			Level: entry.Level, Event: entry.Event, Scenario: entry.Scenario, Operation: entry.Operation,
		}
		aggregates[key] = aggregate
	}
	aggregate.Count++
	aggregate.LastTime = entry.Time
	if entry.Error != "" {
		aggregate.LastError = entry.Error
	}
	if len(snapshot.Entries) == limit {
		copy(snapshot.Entries, snapshot.Entries[1:])
		snapshot.Entries[len(snapshot.Entries)-1] = entry
		snapshot.Truncated = true
		return
	}
	snapshot.Entries = append(snapshot.Entries, entry)
}

func finalizeSnapshot(snapshot Snapshot, aggregates map[string]*Aggregate) Snapshot {
	for left, right := 0, len(snapshot.Entries)-1; left < right; left, right = left+1, right-1 {
		snapshot.Entries[left], snapshot.Entries[right] = snapshot.Entries[right], snapshot.Entries[left]
	}
	snapshot.Aggregates = make([]Aggregate, 0, len(aggregates))
	for _, aggregate := range aggregates {
		snapshot.Aggregates = append(snapshot.Aggregates, *aggregate)
	}
	sort.Slice(snapshot.Aggregates, func(left, right int) bool {
		if snapshot.Aggregates[left].Count != snapshot.Aggregates[right].Count {
			return snapshot.Aggregates[left].Count > snapshot.Aggregates[right].Count
		}
		return snapshot.Aggregates[left].LastTime > snapshot.Aggregates[right].LastTime
	})
	return snapshot
}

func normalizeEntry(sequence int64, raw map[string]any) (Entry, slog.Level) {
	level := parsedRecordLevel(stringValue(raw["level"]))
	message := stringValue(raw["msg"])
	event := stringValue(raw["event"])
	if event == "" {
		event = message
	}
	if outcome := historicalExpectedNetworkOutcome(event, stringValue(raw["error"])); outcome != "" {
		// Older clients recorded intentional resource filters and navigation
		// cancellations as ERROR. Treat those historical entries as expected so
		// the operations page is useful without deleting the append-only journal.
		level = slog.LevelInfo
		raw["outcome"] = outcome
	}
	scenario := stringValue(raw["scenario"])
	if scenario == "" {
		scenario = inferredScenario(event)
	}
	entry := Entry{
		Sequence: sequence,
		Time:     stringValue(raw["time"]), Level: strings.ToUpper(level.String()),
		Message: message, Event: event, Scenario: scenario,
		Operation: stringValue(raw["operation"]), Outcome: stringValue(raw["outcome"]),
		Expected: stringValue(raw["expected"]), Observed: stringValue(raw["observed"]),
		Error: stringValue(raw["error"]), RequestID: stringValue(raw["request_id"]),
		Fields: make(map[string]any),
	}
	for key, value := range raw {
		switch key {
		case "time", "level", "msg", "event", "scenario", "operation", "outcome", "expected", "observed", "error", "request_id":
			continue
		default:
			entry.Fields[key] = value
		}
	}
	if len(entry.Fields) == 0 {
		entry.Fields = nil
	}
	return entry, level
}

func historicalExpectedNetworkOutcome(event, rawError string) string {
	if event != "browser.network.request.completed" && event != "http.client.request.completed" {
		return ""
	}
	reason := strings.ToUpper(rawError)
	switch {
	case strings.Contains(reason, "ERR_BLOCKED_BY_CLIENT"), strings.Contains(reason, "BLOCKEDBYCLIENT"):
		return "blocked"
	case strings.Contains(reason, "ERR_ABORTED"):
		return "canceled"
	default:
		return ""
	}
}

func parsedRecordLevel(value string) slog.Level {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "error":
		return slog.LevelError
	case "warn", "warning":
		return slog.LevelWarn
	case "debug":
		return slog.LevelDebug
	default:
		return slog.LevelInfo
	}
}

func stringValue(value any) string {
	if value == nil {
		return ""
	}
	if text, ok := value.(string); ok {
		return strings.TrimSpace(text)
	}
	encoded, err := json.Marshal(value)
	if err == nil {
		return string(encoded)
	}
	return fmt.Sprint(value)
}

func inferredScenario(event string) string {
	event = strings.ToLower(strings.TrimSpace(event))
	switch {
	case strings.Contains(event, "poster"):
		return "poster_collection"
	case containsAny(event, "catalog", "movie", "theater"):
		return "catalog_collection"
	case containsAny(event, "schedule", "auditorium"):
		return "schedule_collection"
	case strings.Contains(event, "monitor"):
		return "booking_monitoring"
	case strings.Contains(event, "seat"):
		return "seat_selection"
	case containsAny(event, "booking", "payment", "reservation"):
		return "booking"
	case strings.HasPrefix(event, "http."), containsAny(event, "network", "proxy"):
		return "network"
	default:
		return "system"
	}
}

func containsAny(value string, candidates ...string) bool {
	for _, candidate := range candidates {
		if strings.Contains(value, candidate) {
			return true
		}
	}
	return false
}
