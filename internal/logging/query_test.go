package logging

import (
	"log/slog"
	"os"
	"path/filepath"
	"testing"
)

func TestReadSnapshotFiltersAndAggregatesUnexpectedEvents(t *testing.T) {
	path := filepath.Join(t.TempDir(), "client.log")
	contents := `{"time":"2026-08-24T01:00:00Z","level":"INFO","msg":"started","event":"cgv.catalog.started"}
{"time":"2026-08-24T01:00:01Z","level":"WARN","msg":"poster missing","event":"cgv.catalog.poster.missing","scenario":"poster_collection","operation":"capture","expected":"3","observed":"2"}
{"time":"2026-08-24T01:00:02Z","level":"ERROR","msg":"seat failed","event":"cgv.booking.seat.failed","scenario":"seat_selection","operation":"hold","error":"changed"}
{"time":"2026-08-24T01:00:03Z","level":"WARN","msg":"poster missing again","event":"cgv.catalog.poster.missing","scenario":"poster_collection","operation":"capture","error":"one missing"}
{"time":"2026-08-24T01:00:04Z","level":"ERROR","msg":"Browser network request completed","event":"browser.network.request.completed","error":"net::ERR_BLOCKED_BY_CLIENT.Inspector"}
{"time":"2026-08-24T01:00:05Z","level":"ERROR","msg":"http.client.request.completed","event":"http.client.request.completed","error":"net::ERR_ABORTED"}
`
	if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
		t.Fatal(err)
	}
	snapshot, err := ReadSnapshot(path, Query{MinimumLevel: slog.LevelWarn, Limit: 2})
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.Matching != 3 || snapshot.Warnings != 2 || snapshot.Errors != 1 || len(snapshot.Entries) != 2 || !snapshot.Truncated {
		t.Fatalf("snapshot = %+v", snapshot)
	}
	if snapshot.Entries[0].Event != "cgv.catalog.poster.missing" || snapshot.Entries[1].Event != "cgv.booking.seat.failed" {
		t.Fatalf("newest entries = %+v", snapshot.Entries)
	}
	if len(snapshot.Aggregates) != 2 || snapshot.Aggregates[0].Count != 2 || snapshot.Aggregates[0].Scenario != "poster_collection" {
		t.Fatalf("aggregates = %+v", snapshot.Aggregates)
	}

	errorsOnly, err := ReadSnapshot(path, Query{MinimumLevel: slog.LevelError, Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	if errorsOnly.Matching != 1 || len(errorsOnly.Entries) != 1 || errorsOnly.Entries[0].Level != "ERROR" {
		t.Fatalf("errors-only snapshot = %+v", errorsOnly)
	}
}

func TestReadSnapshotInfersScenarioAndAllowsMissingJournal(t *testing.T) {
	path := filepath.Join(t.TempDir(), "client.log")
	missing, err := ReadSnapshot(path, Query{MinimumLevel: slog.LevelWarn})
	if err != nil || missing.Entries == nil || missing.Aggregates == nil {
		t.Fatalf("missing journal = %+v, %v", missing, err)
	}
	if err := os.WriteFile(path, []byte(`{"time":"now","level":"ERROR","msg":"monitor.execution.failed"}`+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	snapshot, err := ReadSnapshot(path, Query{MinimumLevel: slog.LevelWarn})
	if err != nil {
		t.Fatal(err)
	}
	if len(snapshot.Entries) != 1 || snapshot.Entries[0].Scenario != "booking_monitoring" {
		t.Fatalf("inferred snapshot = %+v", snapshot)
	}
}
