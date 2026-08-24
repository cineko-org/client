package main

import (
	"errors"
	"testing"
)

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
