package main

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
)

func desktopStartupReadyPath(dataDir, nonce string) (string, error) {
	if len(nonce) < 16 || len(nonce) > 128 {
		return "", errors.New("startup nonce is invalid")
	}
	for _, character := range nonce {
		if character != '-' && character != '_' && (character < '0' || character > '9') &&
			(character < 'A' || character > 'Z') && (character < 'a' || character > 'z') {
			return "", errors.New("startup nonce is invalid")
		}
	}
	return filepath.Join(dataDir, "runtime", "startup", nonce+".ready"), nil
}

func signalDesktopStartupReady(dataDir, nonce string) error {
	if nonce == "" {
		return nil
	}
	path, err := desktopStartupReadyPath(dataDir, nonce)
	if err != nil {
		return err
	}
	directory := filepath.Dir(path)
	for _, candidate := range []string{dataDir, filepath.Join(dataDir, "runtime"), directory} {
		info, err := os.Lstat(candidate)
		if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			return errors.New("startup directory is unavailable")
		}
	}
	info, _ := os.Stat(directory)
	if runtime.GOOS != "windows" && info.Mode().Perm()&0o077 != 0 {
		return errors.New("startup directory is not private")
	}
	temporary, err := os.CreateTemp(directory, ".ready-*")
	if err != nil {
		return fmt.Errorf("create startup marker: %w", err)
	}
	temporaryPath := temporary.Name()
	defer func() { _ = os.Remove(temporaryPath) }()
	if runtime.GOOS != "windows" {
		if err := temporary.Chmod(0o600); err != nil {
			_ = temporary.Close()
			return fmt.Errorf("secure startup marker: %w", err)
		}
	}
	if _, err := temporary.WriteString(nonce + "\n"); err != nil {
		_ = temporary.Close()
		return fmt.Errorf("write startup marker: %w", err)
	}
	if err := temporary.Sync(); err != nil {
		_ = temporary.Close()
		return fmt.Errorf("sync startup marker: %w", err)
	}
	if err := temporary.Close(); err != nil {
		return fmt.Errorf("close startup marker: %w", err)
	}
	if _, err := os.Lstat(path); err == nil {
		return errors.New("startup marker already exists")
	} else if !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("inspect startup marker: %w", err)
	}
	if err := os.Rename(temporaryPath, path); err != nil {
		return fmt.Errorf("publish startup marker: %w", err)
	}
	return nil
}
