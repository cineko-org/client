package cgv

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func TestWindowControlOnlyForBackgroundBooking(t *testing.T) {
	for _, config := range []BrowserConfig{
		{Headless: true, StartMinimized: true}, {Headless: true}, {},
	} {
		config.ProfileDir = t.TempDir()
		if err := prepareWindowControl(config); err != nil {
			t.Fatal(err)
		}
		if _, err := os.Stat(windowControlPath(config)); !errors.Is(err, os.ErrNotExist) {
			t.Fatal("non-background browser received extension resources")
		}
		for _, arg := range persistentContextOptions(config, "").Args {
			if strings.Contains(arg, "extension") {
				t.Fatalf("unexpected extension flag: %s", arg)
			}
		}
	}
	config := BrowserConfig{ProfileDir: t.TempDir(), StartMinimized: true, RestoreSession: true}
	if err := prepareWindowControl(config); err != nil {
		t.Fatal(err)
	}
	options := persistentContextOptions(config, "")
	for _, arg := range []string{"--no-startup-window", "--load-extension=" + windowControlPath(config), "--disable-extensions-except=" + windowControlPath(config)} {
		if !slices.Contains(options.Args, arg) {
			t.Fatalf("missing %s", arg)
		}
	}
	for _, arg := range []string{"about:blank", "--disable-extensions"} {
		if !slices.Contains(options.IgnoreDefaultArgs, arg) {
			t.Fatalf("default %s not suppressed", arg)
		}
	}
	if slices.Contains(options.Args, "--restore-last-session") {
		t.Fatal("background browser restores foreground tabs")
	}
	data, err := os.ReadFile(filepath.Join(windowControlPath(config), "manifest.json"))
	if err != nil {
		t.Fatal(err)
	}
	var manifest map[string]any
	if err := json.Unmarshal(data, &manifest); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"permissions", "host_permissions", "content_scripts"} {
		if _, ok := manifest[key]; ok {
			t.Fatalf("controller must not request %s", key)
		}
	}
}

func TestWindowControlFailsClosedWithoutController(t *testing.T) {
	root := &Adapter{hideUntilPayment: true}
	if _, err := root.newBookingPage(t.Context()); err == nil {
		t.Fatal("missing controller did not fail")
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := root.newBookingPage(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancellation: %v", err)
	}
}
