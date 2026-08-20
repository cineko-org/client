package configbundle

import (
	"archive/zip"
	"context"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/cineko-org/client/internal/domain"
	"github.com/cineko-org/client/internal/testsupport/memoryrepo"
)

type fixedClock struct{ now time.Time }

func (clock fixedClock) Now() time.Time { return clock.now }

func TestBundleRoundTripIncludesOnlyUserConfiguration(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	now := time.Date(2026, time.August, 9, 7, 0, 0, 0, time.UTC)
	source := openStore(t)
	configuration := validConfiguration(now)
	if err := source.ReplaceConfiguration(ctx, configuration); err != nil {
		t.Fatalf("seed configuration: %v", err)
	}
	bundlePath := filepath.Join(t.TempDir(), "my-config.cnk")
	exporter, _ := New(source, fixedClock{now})
	report, err := exporter.Export(ctx, "user-1", bundlePath)
	if err != nil {
		t.Fatalf("Export() error = %v", err)
	}
	if report.Presets != 1 || report.Monitors != 1 {
		t.Fatalf("export report = %+v", report)
	}
	assertUserOnlyBundle(t, bundlePath)

	target := openStore(t)
	importer, _ := New(target, fixedClock{now})
	if _, err := importer.Import(ctx, bundlePath, "user-2"); err == nil {
		t.Fatal("Import() accepted another Central user's backup")
	}
	imported, err := importer.Import(ctx, bundlePath, "user-1")
	if err != nil {
		t.Fatalf("Import() error = %v", err)
	}
	if imported.Presets != 1 || imported.Monitors != 1 {
		t.Fatalf("import report = %+v", imported)
	}
	loaded, err := target.SnapshotConfiguration(ctx, "user-1")
	if err != nil {
		t.Fatalf("SnapshotConfiguration() error = %v", err)
	}
	if len(loaded.Presets) != 1 || len(loaded.Monitors) != 1 {
		t.Fatalf("loaded configuration = %+v", loaded)
	}
}

func assertUserOnlyBundle(t *testing.T, bundlePath string) {
	t.Helper()
	reader, err := zip.OpenReader(bundlePath)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := reader.Close(); err != nil {
			t.Errorf("close bundle: %v", err)
		}
	})
	if len(reader.File) != 2 || reader.File[0].Name != "manifest.json" || reader.File[1].Name != "config.json" {
		t.Fatalf("bundle entries = %+v", reader.File)
	}
	config, err := reader.File[1].Open()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := config.Close(); err != nil {
			t.Errorf("close configuration: %v", err)
		}
	})
	payload, err := io.ReadAll(config)
	if err != nil {
		t.Fatal(err)
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(payload, &fields); err != nil {
		t.Fatal(err)
	}
	if len(fields) != 2 || fields["presets"] == nil || fields["monitors"] == nil {
		t.Fatalf("configuration fields = %v", fields)
	}
}

func TestBundleImportRejectsPathTraversal(t *testing.T) {
	t.Parallel()
	bundlePath := filepath.Join(t.TempDir(), "unsafe.cnk")
	// #nosec G304 -- bundlePath is rooted in the test's private temporary directory.
	file, err := os.Create(bundlePath)
	if err != nil {
		t.Fatal(err)
	}
	archive := zip.NewWriter(file)
	entry, _ := archive.Create("../outside")
	_, _ = entry.Write([]byte("bad"))
	_ = archive.Close()
	_ = file.Close()
	store := openStore(t)
	manager, _ := New(store, fixedClock{time.Now()})
	if _, err := manager.Import(context.Background(), bundlePath, "user-1"); err == nil {
		t.Fatal("Import() accepted a path traversal entry")
	}
}

func validConfiguration(now time.Time) domain.Configuration {
	preset := domain.Preset{
		ID: "preset-1", UserID: "user-1", Name: "중앙", TheaterID: "theater-1", AuditoriumID: "aud-1",
		SeatCount: 1, SeatPreference: domain.SeatPreference{CandidateSeats: []string{"A1"}, Adjacency: domain.SeatAdjacencyRequired}, CreatedAt: now, UpdatedAt: now,
	}
	monitor := domain.MonitorJob{
		ID: "monitor-1", UserID: "user-1", PresetID: preset.ID, MovieID: "movie_1", Movie: "오디세이",
		TargetDates: []string{"2026-08-10"}, PollInterval: 5 * time.Second,
		Status: domain.MonitorPending, CreatedAt: now, UpdatedAt: now,
	}
	return domain.Configuration{Presets: []domain.Preset{preset}, Monitors: []domain.MonitorJob{monitor}}
}

func openStore(t *testing.T) *memoryrepo.Repository {
	t.Helper()
	return memoryrepo.New()
}
