package cgv

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"

	"github.com/mxschmitt/playwright-go"
)

const sessionStateFileMode = 0o600

// restoreSessionState seeds a new browser process with the last authenticated
// CGV session. A missing snapshot is the normal state before the first login.
func restoreSessionState(browserContext playwright.BrowserContext, path string) error {
	if path == "" {
		return nil
	}
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("inspect CGV session state: %w", err)
	}
	if !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
		return errors.New("CGV session state is not a regular file")
	}
	if runtime.GOOS != "windows" && info.Mode().Perm()&0o077 != 0 {
		return errors.New("CGV session state permissions are not private")
	}
	contents, err := os.ReadFile(path) // #nosec G304 -- path is derived from Cineko's private data directory.
	if err != nil {
		return fmt.Errorf("read CGV session state: %w", err)
	}
	var state playwright.StorageState
	if err := json.Unmarshal(contents, &state); err != nil {
		return fmt.Errorf("decode CGV session state: %w", err)
	}
	if err := browserContext.SetStorageState(path); err != nil {
		return fmt.Errorf("restore CGV session state: %w", err)
	}
	return nil
}

// saveSessionState atomically records cookies and origin storage only after
// CGV has confirmed authentication. Failed or anonymous checks never replace it.
func (adapter *Adapter) saveSessionState() error {
	if adapter.sessionStatePath == "" {
		return nil
	}
	state, err := adapter.browserContext.StorageState(playwright.BrowserContextStorageStateOptions{
		IndexedDB: playwright.Bool(true),
	})
	if err != nil {
		return fmt.Errorf("capture CGV session state: %w", err)
	}
	contents, err := json.Marshal(state)
	if err != nil {
		return fmt.Errorf("encode CGV session state: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(adapter.sessionStatePath), 0o700); err != nil {
		return fmt.Errorf("create CGV session state directory: %w", err)
	}
	temporary, err := os.CreateTemp(filepath.Dir(adapter.sessionStatePath), ".cgv-session-*")
	if err != nil {
		return fmt.Errorf("create CGV session state: %w", err)
	}
	temporaryPath := temporary.Name()
	defer func() { _ = os.Remove(temporaryPath) }()
	if err := temporary.Chmod(sessionStateFileMode); err != nil {
		_ = temporary.Close()
		return fmt.Errorf("secure CGV session state: %w", err)
	}
	if _, err := temporary.Write(contents); err != nil {
		_ = temporary.Close()
		return fmt.Errorf("write CGV session state: %w", err)
	}
	if err := temporary.Sync(); err != nil {
		_ = temporary.Close()
		return fmt.Errorf("sync CGV session state: %w", err)
	}
	if err := temporary.Close(); err != nil {
		return fmt.Errorf("close CGV session state: %w", err)
	}
	if err := replaceFileAtomic(temporaryPath, adapter.sessionStatePath); err != nil {
		return fmt.Errorf("activate CGV session state: %w", err)
	}
	return nil
}
