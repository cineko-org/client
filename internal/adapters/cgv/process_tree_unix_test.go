//go:build !windows

package cgv

import (
	"context"
	"os/exec"
	"testing"
	"time"
)

func TestKillProcessTreeLeavesRootReapToProcessOwner(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, "sh", "-c", "sleep 30") // #nosec G204 -- fixed test command.
	if err := command.Start(); err != nil {
		t.Fatal(err)
	}
	if err := killProcessTree(command.Process.Pid); err != nil {
		t.Fatalf("killProcessTree() error = %v", err)
	}
	if err := command.Wait(); err == nil {
		t.Fatal("command.Wait() unexpectedly succeeded after the root was killed")
	}
}
