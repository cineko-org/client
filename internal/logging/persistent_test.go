package logging

import (
	"context"
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
