//go:build !windows

package cgv

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"time"
)

const processTreeLookupTimeout = 500 * time.Millisecond

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
	children, lookupErr := processTreeChildren(pid)
	var killErr error
	for _, child := range children {
		killErr = errors.Join(killErr, killProcess(child))
	}
	killErr = errors.Join(killErr, killProcess(pid))
	if lookupErr != nil && !errors.Is(lookupErr, errProcessTreeUnavailable) {
		killErr = errors.Join(killErr, lookupErr)
	}
	return killErr
}

var errProcessTreeUnavailable = errors.New("process tree lookup unavailable")

func processTreeChildren(root int) ([]int, error) {
	ctx, cancel := context.WithTimeout(context.Background(), processTreeLookupTimeout)
	defer cancel()
	output, err := exec.CommandContext(ctx, "ps", "-axo", "pid=,ppid=").Output() // #nosec G204 -- fixed executable and arguments.
	if err != nil {
		if errors.Is(ctx.Err(), context.DeadlineExceeded) {
			return nil, errProcessTreeUnavailable
		}
		return nil, fmt.Errorf("list browser processes: %w", err)
	}
	type relation struct{ pid, parent int }
	relations := make([]relation, 0)
	for _, line := range strings.Split(string(output), "\n") {
		fields := strings.Fields(line)
		if len(fields) != 2 {
			continue
		}
		pid, pidErr := strconv.Atoi(fields[0])
		parent, parentErr := strconv.Atoi(fields[1])
		if pidErr == nil && parentErr == nil && pid > 0 && parent > 0 {
			relations = append(relations, relation{pid: pid, parent: parent})
		}
	}
	childrenByParent := make(map[int][]int, len(relations))
	for _, relation := range relations {
		childrenByParent[relation.parent] = append(childrenByParent[relation.parent], relation.pid)
	}
	type descendant struct{ pid, depth int }
	result := make([]descendant, 0)
	queue := []descendant{{pid: root}}
	seen := map[int]struct{}{root: {}}
	for len(queue) > 0 {
		parent := queue[0]
		queue = queue[1:]
		children := childrenByParent[parent.pid]
		for _, child := range children {
			if _, exists := seen[child]; exists {
				continue
			}
			seen[child] = struct{}{}
			result = append(result, descendant{pid: child, depth: parent.depth + 1})
			queue = append(queue, descendant{pid: child, depth: parent.depth + 1})
		}
	}
	// Kill descendants before their parent so the parent cannot orphan a live
	// browser between the tree lookup and the final signal.
	sort.SliceStable(result, func(left, right int) bool { return result[left].depth > result[right].depth })
	processes := make([]int, 0, len(result))
	for _, child := range result {
		processes = append(processes, child.pid)
	}
	return processes, nil
}

func killProcess(pid int) error {
	err := syscall.Kill(pid, syscall.SIGKILL)
	if errors.Is(err, syscall.ESRCH) {
		return nil
	}
	if errors.Is(err, syscall.EPERM) {
		return fmt.Errorf("kill browser process %d: %w", pid, err)
	}
	return err
}

// rootProcessState probes the direct driver without killing it. Wait4 also
// reaps an exited child, so a returned PID is already owned by this caller's
// wait boundary and must not be waited or killed a second time.
func rootProcessState(pid int) (rootProcessStatus, error) {
	if pid <= 0 {
		return rootProcessUnknown, errors.New("browser process PID is invalid")
	}
	var status syscall.WaitStatus
	waitPID, err := syscall.Wait4(pid, &status, syscall.WNOHANG, nil)
	if errors.Is(err, syscall.ECHILD) || errors.Is(err, syscall.ESRCH) {
		return rootProcessReaped, nil
	}
	if err != nil {
		return rootProcessUnknown, fmt.Errorf("probe browser process %d: %w", pid, err)
	}
	if waitPID == 0 {
		return rootProcessLive, nil
	}
	if waitPID == pid {
		return rootProcessReaped, nil
	}
	return rootProcessUnknown, fmt.Errorf("probe browser process %d returned unexpected PID %d", pid, waitPID)
}

// waitRootProcess reaps only the direct driver child. Browser descendants are
// killed by killProcessTree but are not waited here because they are not our
// children. The caller must invoke this only after the transport owner has
// reported that it cannot perform cmd.Wait itself.
func waitRootProcess(pid int) error {
	deadline := time.Now().Add(2 * time.Second)
	for {
		var status syscall.WaitStatus
		waitPID, err := syscall.Wait4(pid, &status, syscall.WNOHANG, nil)
		if errors.Is(err, syscall.ECHILD) || errors.Is(err, syscall.ESRCH) {
			return nil
		}
		if err != nil {
			return fmt.Errorf("wait for browser process %d: %w", pid, err)
		}
		if waitPID == pid {
			return nil
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("wait for browser process %d: %w", pid, errProcessTreeUnavailable)
		}
		time.Sleep(10 * time.Millisecond)
	}
}
