package logging

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestOpenPersistentWritesClientLog(t *testing.T) {
	dataDir := t.TempDir()
	closeLog, err := OpenPersistent(dataDir)
	if err != nil {
		t.Fatal(err)
	}
	Info(context.Background(), "persistent test", "event", "test.persistent")
	if err := closeLog(); err != nil {
		t.Fatal(err)
	}
	// #nosec G304 -- dataDir is an isolated test directory and the filename is fixed.
	contents, err := os.ReadFile(filepath.Join(dataDir, "client.log"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(contents), `"event":"test.persistent"`) {
		t.Fatalf("persistent log = %s", contents)
	}
}

func TestFinishPersistentRunRecordsCleanupErrorBeforeClosing(t *testing.T) {
	dir := t.TempDir()
	_, closeLog, err := OpenPersistentJournal(dir)
	if err != nil {
		t.Fatal(err)
	}
	failure := errors.New("owned browser cleanup failed")
	if err := FinishPersistentRun(context.Background(), failure, closeLog); !errors.Is(err, failure) {
		t.Fatal(err)
	}
	body, err := os.ReadFile(filepath.Join(dir, "client.log")) // #nosec G304 -- Fixed filename in the owned test TempDir.
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(body), "client.shutdown.failed") || !strings.Contains(string(body), failure.Error()) {
		t.Fatalf("missing terminal cause: %s", body)
	}
}

func TestPersistentJournalClearKeepsLoggerWritable(t *testing.T) {
	dataDir := t.TempDir()
	journal, closeLog, err := OpenPersistentJournal(dataDir)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = closeLog() }()
	Info(context.Background(), "before-clear", "event", "before-clear")
	if err := journal.Clear(); err != nil {
		t.Fatal(err)
	}
	Info(context.Background(), "after-clear", "event", "after-clear")
	contents, err := os.ReadFile(filepath.Join(dataDir, "client.log")) // #nosec G304 -- dataDir is an isolated test directory and the filename is fixed.
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(contents), "before-clear") || !strings.Contains(string(contents), "after-clear") {
		t.Fatalf("journal contents after clear = %s", contents)
	}
}
