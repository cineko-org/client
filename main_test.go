package main

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

type centralEventWatcherFake struct{ err error }

func (watcher centralEventWatcherFake) WatchEvents(context.Context) error { return watcher.err }

func TestCentralEventSupervisorSurfacesUnexpectedTermination(t *testing.T) {
	failure := make(chan error, 1)
	expected := errors.New("protocol mismatch")
	superviseCentralEvents(t.Context(), centralEventWatcherFake{err: expected}, func(err error) { failure <- err })
	select {
	case err := <-failure:
		if !errors.Is(err, expected) {
			t.Fatalf("failure = %v", err)
		}
	default:
		t.Fatal("event stream failure was not surfaced")
	}

	cancelled, cancel := context.WithCancel(t.Context())
	cancel()
	superviseCentralEvents(cancelled, centralEventWatcherFake{err: context.Canceled}, func(error) {
		t.Fatal("normal cancellation was reported as a failure")
	})
}

func TestDesktopExitCodeDocumentsLauncherUpdateSignal(t *testing.T) {
	if updateRequiredExitCode != 75 {
		t.Fatalf("documented update-required exit code = %d", updateRequiredExitCode)
	}
	if code := desktopExitCode(errUpdateRequired); code != updateRequiredExitCode {
		t.Fatalf("update-required exit code = %d", code)
	}
	if code := desktopExitCode(errors.New("failed")); code != 1 {
		t.Fatalf("failure exit code = %d", code)
	}
}

func TestDiscardLegacyLocalDomainState(t *testing.T) {
	directory := t.TempDir()
	for _, name := range []string{"cineko.sqlite", "cineko.sqlite-wal", "cineko.sqlite-shm", "settings.json"} {
		if err := os.WriteFile(filepath.Join(directory, name), []byte("obsolete"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if err := discardLegacyLocalDomainState(directory); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"cineko.sqlite", "cineko.sqlite-wal", "cineko.sqlite-shm"} {
		if _, err := os.Stat(filepath.Join(directory, name)); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("obsolete state %s remains: %v", name, err)
		}
	}
	if _, err := os.Stat(filepath.Join(directory, "settings.json")); err != nil {
		t.Fatalf("legacy settings must remain until Central migration succeeds: %v", err)
	}
	if err := discardLegacyLocalDomainState(directory); err != nil {
		t.Fatalf("idempotent cleanup failed: %v", err)
	}
}
