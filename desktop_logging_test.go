package main

import (
	"strings"
	"testing"

	"github.com/cineko-org/client/internal/logging"
)

func TestDesktopAppRecordsStructuredClientWarnings(t *testing.T) {
	var output strings.Builder
	restore := logging.SetOutput(&output)
	defer restore()
	app := &DesktopApp{}
	if err := app.RecordClientLog(`{"level":"warn","event":"ui.seat_map.empty","scenario":"seat_selection","operation":"render","expected":"seat map","observed":"empty","fields":{"route":"/presets"}}`); err != nil {
		t.Fatal(err)
	}
	value := output.String()
	if !strings.Contains(value, `"level":"WARN"`) || !strings.Contains(value, `"scenario":"seat_selection"`) || !strings.Contains(value, `"route":"/presets"`) {
		t.Fatalf("log = %s", value)
	}
}

func TestDesktopAppRejectsInformationalClientEvents(t *testing.T) {
	app := &DesktopApp{}
	if err := app.RecordClientLog(`{"level":"info","event":"ui.rendered"}`); err == nil {
		t.Fatal("expected informational client event to be rejected")
	}
}
