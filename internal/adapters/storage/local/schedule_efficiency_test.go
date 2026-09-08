package local

import (
	"testing"
	"time"

	catalogpb "github.com/cineko-org/contracts/v3/gen/go/cineko/catalog"
)

func TestRepeatedMissingAuditoriumReadsQueueOneScan(t *testing.T) {
	store, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	queued := 0
	for range 100 {
		if _, err := store.ListAuditoriumsByTheater(t.Context(), "yongsan"); err != nil {
			t.Fatal(err)
		}
		select {
		case <-store.ScheduleRequests():
			queued++
		default:
		}
	}
	t.Logf("100 identical empty auditorium UI reads, full scan requests=%d", queued)
	if queued != 1 {
		t.Fatalf("queued %d full scans, want 1", queued)
	}
	store.scheduleRequestedAt["yongsan"] = time.Now().Add(-scheduleBootstrapRetryInterval - time.Second)
	if _, err := store.ListAuditoriumsByTheater(t.Context(), "yongsan"); err != nil {
		t.Fatal(err)
	}
	select {
	case <-store.ScheduleRequests():
	default:
		t.Fatal("bootstrap never retried after cooldown")
	}
}

func TestIdenticalEmptyCapturesDoNotRewriteCatalog(t *testing.T) {
	store, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	id := "yongsan"
	theater := catalogpb.Theater_builder{Id: &id}.Build()
	if err := store.PutScheduleCaptures(t.Context(), theater, nil); err != nil {
		t.Fatal(err)
	}
	baseline := store.catalog.GetGeneration()
	for range 100 {
		if err := store.PutScheduleCaptures(t.Context(), theater, nil); err != nil {
			t.Fatal(err)
		}
	}
	delta := store.catalog.GetGeneration() - baseline
	t.Logf("100 identical empty captures, catalog rewrites/change notifications=%d", delta)
	if delta != 0 {
		t.Fatalf("unchanged catalog rewritten %d times", delta)
	}
	theater.SetName("updated theater")
	if err := store.PutScheduleCaptures(t.Context(), theater, nil); err != nil {
		t.Fatal(err)
	}
	if store.catalog.GetGeneration() != baseline+1 {
		t.Fatal("real catalog change was discarded")
	}
}
