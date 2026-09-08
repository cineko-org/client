package networkcapture

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"time"
)

type persistedRateLimit struct {
	Until    time.Time `json:"until"`
	Failures int       `json:"failures"`
}

func newPersistentRateLimitGate(path string, logger *slog.Logger) (*RateLimitGate, error) {
	gate := NewRateLimitGate()
	gate.statePath, gate.logger = path, logger
	file, err := os.Open(path)
	if errors.Is(err, os.ErrNotExist) {
		return gate, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read provider cooldown: %w", err)
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(file, (1<<20)+1))
	if err != nil {
		return nil, err
	}
	if len(data) > 1<<20 {
		return nil, errors.New("provider cooldown file exceeds 1 MiB")
	}
	var states map[string]persistedRateLimit
	if err := json.Unmarshal(data, &states); err != nil {
		return nil, fmt.Errorf("decode provider cooldown: %w", err)
	}
	for key, state := range states {
		if key == "" || state.Until.IsZero() || state.Failures < 1 || state.Failures > 1000000 {
			return nil, errors.New("invalid provider cooldown state")
		}
		// Keep expired entries too: after restart just one recovery probe is
		// permitted, and repeated 429 responses keep their backoff history.
		gate.states[key] = &rateLimitState{blockedUntil: state.Until, failures: state.Failures}
	}
	return gate, nil
}

// Caller holds gate.mu. Only circuit transitions write, never routine traffic.
func (gate *RateLimitGate) persistLocked() {
	if gate.statePath == "" {
		return
	}
	states := make(map[string]persistedRateLimit, len(gate.states))
	for key, state := range gate.states {
		states[key] = persistedRateLimit{Until: state.blockedUntil, Failures: state.failures}
	}
	data, err := json.Marshal(states)
	if err == nil {
		err = writeRateLimitState(gate.statePath, data)
	}
	if err != nil && gate.logger != nil {
		gate.logger.Error("Provider cooldown persistence failed", "event", "browser.network.rate_limit.persist.failed",
			"scenario", "booking_monitoring", "outcome", "failed", "error", err)
	}
}

func writeRateLimitState(path string, data []byte) error {
	file, err := os.CreateTemp(filepath.Dir(path), ".rate-limit-*")
	if err != nil {
		return err
	}
	defer os.Remove(file.Name())
	if _, err := file.Write(data); err != nil {
		file.Close()
		return err
	}
	if err := file.Sync(); err != nil {
		file.Close()
		return err
	}
	if err := file.Close(); err != nil {
		return err
	}
	return os.Rename(file.Name(), path)
}
