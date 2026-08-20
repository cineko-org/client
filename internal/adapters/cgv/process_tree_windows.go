//go:build windows

package cgv

import (
	"errors"
	"fmt"
	"os/exec"
	"strconv"
	"strings"
)

func killProcessTree(pid int) error {
	if pid <= 0 {
		return errors.New("browser process PID is invalid")
	}
	command := exec.Command("taskkill", "/PID", strconv.Itoa(pid), "/T", "/F")
	if output, err := command.CombinedOutput(); err != nil {
		if strings.Contains(strings.ToLower(string(output)), "not found") || strings.Contains(strings.ToLower(string(output)), "no running instance") {
			return nil
		}
		return fmt.Errorf("kill browser process tree %d: %w (%s)", pid, err, output)
	}
	return nil
}
