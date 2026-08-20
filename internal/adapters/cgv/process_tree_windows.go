//go:build windows

package cgv

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"strconv"
	"strings"
	"time"

	"golang.org/x/sys/windows"
)

type rootProcessStatus uint8

const (
	rootProcessUnknown rootProcessStatus = iota
	rootProcessLive
	rootProcessReaped
)

func killProcessTree(pid int) error {
	if pid <= 0 {
		return errors.New("browser process PID is invalid")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	output, err := exec.CommandContext(ctx, "taskkill", "/PID", strconv.Itoa(pid), "/T", "/F").CombinedOutput() // #nosec G204 -- fixed executable and arguments.
	if errors.Is(ctx.Err(), context.DeadlineExceeded) {
		return fmt.Errorf("kill browser process tree %d timed out", pid)
	}
	if err == nil {
		return nil
	}
	if strings.Contains(strings.ToLower(string(output)), "not found") {
		return nil
	}
	return fmt.Errorf("kill browser process tree %d: %w", pid, err)
}

// rootProcessState probes the direct driver without killing it. A signaled
// process cannot safely be treated as a live PID; the already-open handle is
// the Windows wait boundary, so no second PID lookup is needed.
func rootProcessState(pid int) (rootProcessStatus, error) {
	if pid <= 0 {
		return rootProcessUnknown, errors.New("browser process PID is invalid")
	}
	handle, err := windows.OpenProcess(windows.SYNCHRONIZE|windows.PROCESS_QUERY_LIMITED_INFORMATION, false, uint32(pid))
	if err != nil {
		if errors.Is(err, windows.ERROR_INVALID_PARAMETER) {
			return rootProcessReaped, nil
		}
		return rootProcessUnknown, fmt.Errorf("probe browser process %d: %w", pid, err)
	}
	event, waitErr := windows.WaitForSingleObject(handle, 0)
	closeErr := windows.CloseHandle(handle)
	if waitErr != nil {
		return rootProcessUnknown, fmt.Errorf("probe browser process %d: %w", pid, waitErr)
	}
	if closeErr != nil {
		return rootProcessUnknown, fmt.Errorf("close browser process %d handle: %w", pid, closeErr)
	}
	switch event {
	case uint32(windows.WAIT_TIMEOUT):
		return rootProcessLive, nil
	case uint32(windows.WAIT_OBJECT_0):
		return rootProcessReaped, nil
	default:
		return rootProcessUnknown, fmt.Errorf("probe browser process %d returned wait status %d", pid, event)
	}
}

// waitRootProcess waits only for the direct driver handle. Browser descendants
// are terminated by taskkill but are not waited because they are not children.
func waitRootProcess(pid int) error {
	handle, err := windows.OpenProcess(windows.SYNCHRONIZE|windows.PROCESS_QUERY_LIMITED_INFORMATION, false, uint32(pid))
	if err != nil {
		if errors.Is(err, windows.ERROR_INVALID_PARAMETER) {
			return nil
		}
		return fmt.Errorf("open browser process %d: %w", pid, err)
	}
	event, err := windows.WaitForSingleObject(handle, 2_000)
	closeErr := windows.CloseHandle(handle)
	if err != nil {
		return fmt.Errorf("wait for browser process %d: %w", pid, err)
	}
	if closeErr != nil {
		return fmt.Errorf("close browser process %d handle: %w", pid, closeErr)
	}
	if event != uint32(windows.WAIT_OBJECT_0) {
		return fmt.Errorf("wait for browser process %d: timeout", pid)
	}
	return nil
}
