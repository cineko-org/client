package logging

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/cineko-org/probe/v2/networkcapture"
)

func TestCooldownSurvivesFullStoreRestartAndLogClear(t *testing.T) {
	path := t.TempDir()
	first, err := networkcapture.NewStore(path, nil)
	if err != nil {
		t.Fatal(err)
	}
	want := first.RateLimit().Observe429("cgv.co.kr", []networkcapture.Header{{Name: "Retry-After", Value: "120"}})
	if err := first.Clear(); err != nil {
		t.Fatal(err)
	}
	// A new Store has no shared Go pointers with the first process's state.
	second, err := networkcapture.NewStore(path, nil)
	if err != nil {
		t.Fatal(err)
	}
	for range 100 {
		allowed, got := second.RateLimit().Allow("cgv.co.kr")
		if allowed || !got.BlockedUntil.Equal(want.BlockedUntil) {
			t.Fatalf("lost cooldown: allowed=%v state=%+v", allowed, got)
		}
	}
	t.Log("full Store reconstruction after log clear: 100 attempted admissions, allowed=0, original Retry-After deadline retained")
}

func TestExpiredCooldownRestartsHalfOpenAndPersistsRecovery(t *testing.T) {
	path := t.TempDir()
	// A valid previously expired state, without waiting or hitting a provider.
	if err := os.WriteFile(filepath.Join(path, "rate-limit.json"), []byte(`{"cgv.co.kr":{"until":"2026-01-01T00:00:00Z","failures":3}}`), 0600); err != nil {
		t.Fatal(err)
	}
	store, err := networkcapture.NewStore(path, nil)
	if err != nil {
		t.Fatal(err)
	}
	if allowed, _ := store.RateLimit().Allow("cgv.co.kr"); !allowed {
		t.Fatal("recovery probe blocked")
	}
	for range 100 {
		if allowed, _ := store.RateLimit().Allow("cgv.co.kr"); allowed {
			t.Fatal("more than one half-open admission")
		}
	}
	if !store.RateLimit().ObserveSuccess("cgv.co.kr") {
		t.Fatal("recovery not recognized")
	}
	restarted, err := networkcapture.NewStore(path, nil)
	if err != nil {
		t.Fatal(err)
	}
	if blocked, _ := restarted.RateLimit().Blocked("cgv.co.kr"); blocked {
		t.Fatal("successful recovery not persisted")
	}
}

func TestCorruptCooldownDoesNotSilentlyStartUnrestricted(t *testing.T) {
	path := t.TempDir()
	if err := os.WriteFile(filepath.Join(path, "rate-limit.json"), []byte("broken"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := networkcapture.NewStore(path, nil); err == nil {
		t.Fatal("corrupt state silently discarded")
	}
}

func TestOrdinaryTrafficDoesNotRewriteCooldownFile(t *testing.T) {
	path := t.TempDir()
	store, err := networkcapture.NewStore(path, nil)
	if err != nil {
		t.Fatal(err)
	}
	store.RateLimit().Observe429("cgv.co.kr", []networkcapture.Header{{Name: "Retry-After", Value: "60"}})
	file := filepath.Join(path, "rate-limit.json")
	stamp := time.Now().Add(-time.Hour)
	if err := os.Chtimes(file, stamp, stamp); err != nil {
		t.Fatal(err)
	}
	for range 1000 {
		store.RateLimit().Allow("other.host")
		store.RateLimit().ObserveSuccess("other.host")
	}
	info, err := os.Stat(file)
	if err != nil {
		t.Fatal(err)
	}
	if info.ModTime().Sub(stamp).Abs() > time.Millisecond {
		t.Fatal("routine traffic rewrote cooldown")
	}
}
