//go:build !windows

package cgv

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"strconv"
	"strings"
	"syscall"
	"time"
)

func killProcessTree(pid int) error {
	if pid <= 0 {
		return errors.New("browser process PID is invalid")
	}
	if !processExists(pid) {
		return nil
	}
	descendants, err := processDescendants(pid)
	if err != nil {
		return err
	}
	// Kill descendants first, then the driver root. The exact PID is always
	// validated as numeric and never passed through a shell.
	for index := len(descendants) - 1; index >= 0; index-- {
		_ = syscall.Kill(descendants[index], syscall.SIGKILL)
	}
	if !processExists(pid) {
		return nil
	}
	if err := syscall.Kill(pid, syscall.SIGKILL); err != nil && !errors.Is(err, syscall.ESRCH) {
		return fmt.Errorf("kill browser driver %d: %w", pid, err)
	}
	return nil
}

func processDescendants(root int) ([]int, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, "ps", "-axo", "pid=,ppid=")
	output, err := command.Output()
	if err != nil {
		return nil, fmt.Errorf("inspect browser process tree: %w", err)
	}
	children := make(map[int][]int)
	for _, line := range strings.Split(string(output), "\n") {
		fields := strings.Fields(line)
		if len(fields) != 2 {
			continue
		}
		child, childErr := strconv.Atoi(fields[0])
		parent, parentErr := strconv.Atoi(fields[1])
		if childErr == nil && parentErr == nil && child > 0 && parent > 0 {
			children[parent] = append(children[parent], child)
		}
	}
	result := make([]int, 0)
	var visit func(int)
	visit = func(parent int) {
		for _, child := range children[parent] {
			result = append(result, child)
			visit(child)
		}
	}
	visit(root)
	return result, nil
}

func processExists(pid int) bool {
	if pid <= 0 {
		return false
	}
	err := syscall.Kill(pid, 0)
	return err == nil || errors.Is(err, syscall.EPERM)
}
