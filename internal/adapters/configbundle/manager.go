package configbundle

import (
	"archive/zip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"path"
	"path/filepath"
	"strings"
	"time"

	"github.com/cineko-org/client/internal/application"
	"github.com/cineko-org/client/internal/domain"
)

const (
	formatName       = "cineko-configuration"
	formatVersion    = 2
	maxConfigBytes   = 32 << 20
	maxBundleBytes   = 64 << 20
	maxBundleEntries = 2
)

type Manifest struct {
	Format       string    `json:"format"`
	Version      int       `json:"version"`
	CreatedAt    time.Time `json:"createdAt"`
	UserID       string    `json:"userId"`
	ConfigSHA256 string    `json:"configSha256"`
}

type Report struct {
	Path     string `json:"path"`
	UserID   string `json:"userId"`
	Presets  int    `json:"presets"`
	Monitors int    `json:"monitors"`
}

type Clock interface {
	Now() time.Time
}

type Manager struct {
	repository application.ConfigurationRepository
	clock      Clock
}

func New(repository application.ConfigurationRepository, clock Clock) (*Manager, error) {
	if repository == nil || clock == nil {
		return nil, errors.New("configuration bundle dependencies are incomplete")
	}
	return &Manager{repository: repository, clock: clock}, nil
}

func (manager *Manager) Export(ctx context.Context, userID, targetPath string) (Report, error) {
	if strings.TrimSpace(userID) == "" {
		return Report{}, errors.New("user id is required")
	}
	targetPath = ensureExtension(strings.TrimSpace(targetPath))
	if targetPath == ".cnk" {
		return Report{}, errors.New("configuration target path is required")
	}
	configuration, err := manager.repository.SnapshotConfiguration(ctx, userID)
	if err != nil {
		return Report{}, err
	}
	return manager.exportConfiguration(ctx, userID, targetPath, configuration)
}

func (manager *Manager) exportConfiguration(
	ctx context.Context,
	userID, targetPath string,
	configuration domain.Configuration,
) (Report, error) {
	configData, err := json.MarshalIndent(configuration, "", "  ")
	if err != nil {
		return Report{}, fmt.Errorf("encode configuration: %w", err)
	}
	if len(configData) > maxConfigBytes {
		return Report{}, fmt.Errorf("configuration exceeds %d bytes", maxConfigBytes)
	}
	configDigest := sha256.Sum256(configData)
	manifest := Manifest{
		Format: formatName, Version: formatVersion, CreatedAt: manager.clock.Now(), UserID: userID,
		ConfigSHA256: hex.EncodeToString(configDigest[:]),
	}
	manifestData, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return Report{}, err
	}
	if err := writeBundle(ctx, targetPath, manifestData, configData); err != nil {
		return Report{}, err
	}
	report := reportFor(targetPath, configuration)
	report.UserID = userID
	return report, nil
}

func (manager *Manager) Import(ctx context.Context, sourcePath string, userID string) (Report, error) {
	reader, err := zip.OpenReader(sourcePath)
	if err != nil {
		return Report{}, fmt.Errorf("open .cnk bundle: %w", err)
	}
	defer func() { _ = reader.Close() }()
	manifest, configuration, _, err := readBundle(reader)
	if err != nil {
		return Report{}, err
	}
	if strings.TrimSpace(userID) == "" || manifest.UserID != userID {
		return Report{}, errors.New(".cnk backup belongs to another Central user")
	}
	current, err := manager.repository.SnapshotConfiguration(ctx, userID)
	if err != nil {
		return Report{}, fmt.Errorf("read current Central data revision: %w", err)
	}
	configuration.Revision = current.Revision

	if err := manager.repository.ReplaceConfiguration(ctx, configuration); err != nil {
		return Report{}, err
	}
	report := reportFor(sourcePath, configuration)
	report.UserID = manifest.UserID
	return report, nil
}

func readBundle(reader *zip.ReadCloser) (Manifest, domain.Configuration, map[string]*zip.File, error) {
	entries, err := indexEntries(reader.File)
	if err != nil {
		return Manifest{}, domain.Configuration{}, nil, err
	}
	manifestData, err := readEntry(entries["manifest.json"], maxConfigBytes)
	if err != nil {
		return Manifest{}, domain.Configuration{}, nil, fmt.Errorf("read manifest: %w", err)
	}
	var manifest Manifest
	if err := decodeStrict(manifestData, &manifest); err != nil {
		return Manifest{}, domain.Configuration{}, nil, fmt.Errorf("decode manifest: %w", err)
	}
	if manifest.Format != formatName || manifest.Version != formatVersion {
		return Manifest{}, domain.Configuration{}, nil,
			fmt.Errorf("unsupported .cnk format %q version %d", manifest.Format, manifest.Version)
	}
	configurationData, err := readEntry(entries["config.json"], maxConfigBytes)
	if err != nil {
		return Manifest{}, domain.Configuration{}, nil, fmt.Errorf("read configuration: %w", err)
	}
	digest := sha256.Sum256(configurationData)
	if !strings.EqualFold(hex.EncodeToString(digest[:]), manifest.ConfigSHA256) {
		return Manifest{}, domain.Configuration{}, nil,
			errors.New("configuration hash does not match the manifest")
	}
	var configuration domain.Configuration
	if err := decodeStrict(configurationData, &configuration); err != nil {
		return Manifest{}, domain.Configuration{}, nil, fmt.Errorf("decode configuration: %w", err)
	}
	if err := validateConfiguration(configuration, manifest.UserID); err != nil {
		return Manifest{}, domain.Configuration{}, nil, err
	}
	return manifest, configuration, entries, nil
}

func validateConfiguration(configuration domain.Configuration, userID string) error {
	if strings.TrimSpace(userID) == "" {
		return errors.New("manifest user id is required")
	}
	presets, err := validatePresets(configuration.Presets, userID)
	if err != nil {
		return err
	}
	if err := validateMonitors(configuration.Monitors, presets, userID); err != nil {
		return err
	}
	return nil
}

func validatePresets(
	values []domain.Preset,
	userID string,
) (map[string]struct{}, error) {
	presets := make(map[string]struct{}, len(values))
	for _, preset := range values {
		if preset.UserID != userID {
			return nil, fmt.Errorf("preset %s belongs to another user", preset.ID)
		}
		if err := preset.Validate(nil); err != nil {
			return nil, fmt.Errorf("invalid preset: %w", err)
		}
		presets[preset.ID] = struct{}{}
	}
	return presets, nil
}

func validateMonitors(values []domain.MonitorJob, presets map[string]struct{}, userID string) error {
	for _, monitor := range values {
		if monitor.UserID != userID {
			return fmt.Errorf("monitor %s belongs to another user", monitor.ID)
		}
		if _, exists := presets[monitor.PresetID]; !exists {
			return fmt.Errorf("monitor %s refers to an unknown preset", monitor.ID)
		}
		if err := monitor.Validate(); err != nil {
			return fmt.Errorf("invalid monitor: %w", err)
		}
	}
	return nil
}

func indexEntries(files []*zip.File) (map[string]*zip.File, error) {
	if len(files) > maxBundleEntries {
		return nil, fmt.Errorf("bundle contains too many entries: %d", len(files))
	}
	entries := make(map[string]*zip.File, len(files))
	var total uint64
	for _, file := range files {
		clean := path.Clean(file.Name)
		if clean != file.Name || strings.HasPrefix(clean, "/") || strings.HasPrefix(clean, "../") || file.FileInfo().IsDir() {
			return nil, fmt.Errorf("unsafe bundle entry %q", file.Name)
		}
		if file.Mode()&os.ModeSymlink != 0 {
			return nil, fmt.Errorf("symbolic links are not allowed in .cnk bundles")
		}
		total += file.UncompressedSize64
		if total > maxBundleBytes {
			return nil, fmt.Errorf("bundle expands beyond %d bytes", maxBundleBytes)
		}
		if _, duplicate := entries[clean]; duplicate {
			return nil, fmt.Errorf("duplicate bundle entry %q", clean)
		}
		if clean != "manifest.json" && clean != "config.json" {
			return nil, fmt.Errorf("unknown bundle entry %q", clean)
		}
		entries[clean] = file
	}
	if entries["manifest.json"] == nil || entries["config.json"] == nil {
		return nil, errors.New("bundle must contain manifest.json and config.json")
	}
	return entries, nil
}

func readEntry(file *zip.File, limit int64) ([]byte, error) {
	if file == nil {
		return nil, os.ErrNotExist
	}
	if file.UncompressedSize64 > math.MaxInt64 || int64(file.UncompressedSize64) > limit {
		return nil, fmt.Errorf("entry %q exceeds %d bytes", file.Name, limit)
	}
	reader, err := file.Open()
	if err != nil {
		return nil, err
	}
	defer func() { _ = reader.Close() }()
	data, err := io.ReadAll(io.LimitReader(reader, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > limit {
		return nil, fmt.Errorf("entry %q exceeds %d bytes", file.Name, limit)
	}
	return data, nil
}

func decodeStrict(data []byte, target any) error {
	decoder := json.NewDecoder(strings.NewReader(string(data)))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return errors.New("JSON document contains trailing data")
	}
	return nil
}

func writeBundle(ctx context.Context, targetPath string, manifest, configuration []byte) error {
	if err := os.MkdirAll(filepath.Dir(targetPath), 0o700); err != nil {
		return err
	}
	temporary, err := os.CreateTemp(filepath.Dir(targetPath), ".cineko-*.cnk")
	if err != nil {
		return err
	}
	temporaryPath := temporary.Name()
	defer func() { _ = os.Remove(temporaryPath) }()
	if err := temporary.Chmod(0o600); err != nil {
		_ = temporary.Close()
		return err
	}
	archive := zip.NewWriter(temporary)
	write := func(name string, data []byte) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		entry, createErr := archive.CreateHeader(&zip.FileHeader{Name: name, Method: zip.Deflate})
		if createErr != nil {
			return createErr
		}
		_, createErr = entry.Write(data)
		return createErr
	}
	err = write("manifest.json", manifest)
	if err == nil {
		err = write("config.json", configuration)
	} else {
		_ = archive.Close()
		_ = temporary.Close()
		return err
	}
	if closeErr := archive.Close(); err == nil {
		err = closeErr
	}
	if syncErr := temporary.Sync(); err == nil {
		err = syncErr
	}
	if closeErr := temporary.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		return err
	}
	if err := os.Rename(temporaryPath, targetPath); err != nil {
		return fmt.Errorf("install .cnk bundle: %w", err)
	}
	return nil
}

func ensureExtension(value string) string {
	if strings.EqualFold(filepath.Ext(value), ".cnk") {
		return value
	}
	return value + ".cnk"
}

func reportFor(bundlePath string, configuration domain.Configuration) Report {
	userID := ""
	switch {
	case len(configuration.Presets) > 0:
		userID = configuration.Presets[0].UserID
	case len(configuration.Monitors) > 0:
		userID = configuration.Monitors[0].UserID
	}
	return Report{
		Path: bundlePath, UserID: userID, Presets: len(configuration.Presets),
		Monitors: len(configuration.Monitors),
	}
}
