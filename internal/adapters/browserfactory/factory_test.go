package browserfactory

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/cineko-org/client/internal/adapters/cgv"
	"github.com/cineko-org/client/internal/adapters/egress"
)

type warmInitializerLifecycleStub struct {
	closeErr  error
	waitErr   error
	needsKill bool
	killCalls atomic.Int32
	waitCalls atomic.Int32
}

func (stub *warmInitializerLifecycleStub) CloseWithError() error { return stub.closeErr }
func (stub *warmInitializerLifecycleStub) ProcessNeedsForcedReap() bool {
	return stub.needsKill
}
func (stub *warmInitializerLifecycleStub) KillProcessTree() error {
	stub.killCalls.Add(1)
	return nil
}
func (stub *warmInitializerLifecycleStub) WaitProcess() error {
	stub.waitCalls.Add(1)
	return stub.waitErr
}

func TestFactoryRequiresEgressManager(t *testing.T) {
	t.Parallel()
	if _, err := New(cgv.DefaultBrowserConfig(), nil); err == nil {
		t.Fatal("New() error = nil")
	}
}

func TestFactoryUsesThreeIsolatedSlotsAndUserIsolatedSessionProfiles(t *testing.T) {
	t.Parallel()
	manager, err := egress.New(egress.Config{})
	if err != nil {
		t.Fatal(err)
	}
	config := cgv.DefaultBrowserConfig()
	config.ProfileDir = filepath.Join(t.TempDir(), "profiles")
	if err := os.MkdirAll(config.ProfileDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(config.ProfileDir, "authenticated-cookie"), []byte("secret"), 0o600); err != nil {
		t.Fatal(err)
	}
	factory, err := New(config, manager)
	if err != nil {
		t.Fatal(err)
	}
	defer factory.Close()
	if got := cap(factory.slots); got != 3 {
		t.Fatalf("browser capacity = %d", got)
	}
	if got := cap(factory.sessions); got != 1 {
		t.Fatalf("session capacity = %d", got)
	}
	first, cleanup, err := factory.profileForTask(Task{Purpose: egress.PurposeScan}, 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(first); err != nil {
		t.Fatalf("scan profile: %v", err)
	}
	if _, err := os.Stat(filepath.Join(first, "authenticated-cookie")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("scan inherited account profile state: %v", err)
	}
	cleanup()
	if _, err := os.Stat(first); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("scan profile was not removed: %v", err)
	}
	sessionA, cleanupA, err := factory.profileForTask(Task{Purpose: egress.PurposeSession, SessionKey: "user-a"}, 0)
	if err != nil {
		t.Fatal(err)
	}
	sessionAAfterRestart, cleanupAAfterRestart, err := factory.profileForTask(
		Task{Purpose: egress.PurposeSession, SessionKey: "user-a"}, 2,
	)
	if err != nil {
		t.Fatal(err)
	}
	sessionB, cleanupB, err := factory.profileForTask(Task{Purpose: egress.PurposeSession, SessionKey: "user-b"}, 0)
	if err != nil {
		t.Fatal(err)
	}
	if sessionA != sessionAAfterRestart || sessionA == sessionB || sessionA == config.ProfileDir {
		t.Fatalf("session profiles = %q, %q, %q", sessionA, sessionAAfterRestart, sessionB)
	}
	if cleanupA != nil || cleanupAAfterRestart != nil || cleanupB != nil {
		t.Fatal("persistent session profile received a cleanup callback")
	}
	if strings.Contains(sessionA, "user-a") || strings.Contains(sessionB, "user-b") {
		t.Fatalf("session profile exposes raw key: %q, %q", sessionA, sessionB)
	}
	for _, path := range []string{sessionA, sessionB} {
		if relative, err := filepath.Rel(filepath.Join(config.ProfileDir+"-tasks", "sessions"), path); err != nil ||
			relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
			t.Fatalf("session profile escaped isolation root: %q (%q, %v)", path, relative, err)
		}
	}
	marker := filepath.Join(sessionB, "cookie")
	if err := os.WriteFile(marker, []byte("user-b"), 0o600); err != nil {
		t.Fatal(err)
	}
	scan, cleanupScan, err := factory.profileForTask(Task{Purpose: egress.PurposeScan}, 1)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(scan, "temporary"), []byte("scan"), 0o600); err != nil {
		t.Fatal(err)
	}
	cleanupScan()
	contents, err := os.ReadFile(marker) // #nosec G304 -- marker is built under t.TempDir.
	if err != nil || string(contents) != "user-b" {
		t.Fatalf("scan cleanup changed another user's profile: %q, %v", contents, err)
	}
}

func TestSessionProfileRejectsEmptyAndPathLikeKeys(t *testing.T) {
	t.Parallel()
	manager, err := egress.New(egress.Config{})
	if err != nil {
		t.Fatal(err)
	}
	config := cgv.DefaultBrowserConfig()
	config.ProfileDir = filepath.Join(t.TempDir(), "profiles")
	factory, err := New(config, manager)
	if err != nil {
		t.Fatal(err)
	}
	defer factory.Close()
	if _, _, err := factory.profileForTask(Task{Purpose: egress.PurposeSession, SessionKey: " \t\n"}, 0); err == nil {
		t.Fatal("empty session key error = nil")
	}
	malicious := filepath.Join("..", "..", "another-user")
	profile, cleanup, err := factory.profileForTask(Task{Purpose: egress.PurposeSession, SessionKey: malicious}, 0)
	if err != nil {
		t.Fatal(err)
	}
	if cleanup != nil || strings.Contains(profile, "another-user") || !strings.HasPrefix(
		profile,
		filepath.Join(config.ProfileDir+"-tasks", "sessions")+string(filepath.Separator),
	) {
		t.Fatalf("unsafe session profile = %q, cleanup=%v", profile, cleanup != nil)
	}
}

func TestWarmProfileIsPerSlotAndDoesNotExposeUserKey(t *testing.T) {
	t.Parallel()
	base := filepath.Join(t.TempDir(), "chrome-profile")
	first, err := warmProfileForTask(base, "user-a", 1)
	if err != nil {
		t.Fatal(err)
	}
	second, err := warmProfileForTask(base, "user-a", 2)
	if err != nil {
		t.Fatal(err)
	}
	if first == second || strings.Contains(first, "user-a") || strings.Contains(second, "user-a") {
		t.Fatalf("warm profiles are not isolated/redacted: %q %q", first, second)
	}
	root := filepath.Join(base+"-tasks", "warm")
	for _, profile := range []string{first, second} {
		relative, err := filepath.Rel(root, profile)
		if err != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
			t.Fatalf("warm profile escaped isolation root: %q", profile)
		}
	}
}

func TestWarmInitializerNonTransportCloseErrorDoesNotKill(t *testing.T) {
	profileDir := t.TempDir()
	initErr := errors.New("authentication failed")
	closeErr := errors.New("browser context close failed")
	lifecycle := &warmInitializerLifecycleStub{closeErr: closeErr}

	err := cleanupWarmInitializerFailure(lifecycle, profileDir, initErr)
	if !errors.Is(err, initErr) || !errors.Is(err, closeErr) {
		t.Fatalf("cleanup error = %v, want initializer and close errors", err)
	}
	if got := lifecycle.killCalls.Load(); got != 0 {
		t.Fatalf("kill calls = %d, want 0 for non-transport close error", got)
	}
	if got := lifecycle.waitCalls.Load(); got != 1 {
		t.Fatalf("wait calls = %d, want 1", got)
	}
	if _, statErr := os.Stat(profileDir); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("failed warm profile still exists: %v", statErr)
	}
}

func TestSessionProfileReportsDirectoryCreationFailure(t *testing.T) {
	t.Parallel()
	manager, err := egress.New(egress.Config{})
	if err != nil {
		t.Fatal(err)
	}
	config := cgv.DefaultBrowserConfig()
	config.ProfileDir = filepath.Join(t.TempDir(), "profiles")
	if err := os.WriteFile(config.ProfileDir+"-tasks", []byte("not-a-directory"), 0o600); err != nil {
		t.Fatal(err)
	}
	factory, err := New(config, manager)
	if err != nil {
		t.Fatal(err)
	}
	defer factory.Close()
	if _, _, err := factory.profileForTask(
		Task{Purpose: egress.PurposeSession, SessionKey: "user"},
		0,
	); err == nil || !strings.Contains(err.Error(), "create browser session profile") {
		t.Fatalf("profile creation error = %v", err)
	}
}

func TestTaskBrowserIdentityPolicy(t *testing.T) {
	t.Parallel()
	base := cgv.DefaultBrowserConfig()
	session := browserConfigForTask(base, Task{Purpose: egress.PurposeSession, Headless: true})
	if !session.RestoreSession || session.BlockResources || session.UserAgentMode != cgv.UserAgentSession || session.Headless || !session.StartMinimized {
		t.Fatalf("session browser config = %+v", session)
	}
	scan := browserConfigForTask(base, Task{Purpose: egress.PurposeScan})
	if scan.RestoreSession || !scan.BlockResources || scan.UserAgentMode != cgv.UserAgentRandomizedScan {
		t.Fatalf("scan browser config = %+v", scan)
	}
}

func TestSessionLeaseStaysFixedAcrossBrowserRestarts(t *testing.T) {
	t.Parallel()
	manager, err := egress.New(egress.Config{Proxies: []egress.Proxy{{Server: "http://proxy.test:8080"}}})
	if err != nil {
		t.Fatal(err)
	}
	factory, err := New(cgv.DefaultBrowserConfig(), manager)
	if err != nil {
		t.Fatal(err)
	}
	first, shared, err := factory.leaseForTask(context.Background(), manager, egress.PurposeSession)
	if err != nil || !shared {
		t.Fatalf("first session lease = %p, %t, %v", first, shared, err)
	}
	second, shared, err := factory.leaseForTask(context.Background(), manager, egress.PurposeSession)
	if err != nil || !shared || first != second {
		t.Fatalf("second session lease = %p, %t, %v; first = %p", second, shared, err, first)
	}
	scan, shared, err := factory.leaseForTask(context.Background(), manager, egress.PurposeScan)
	if err != nil || shared || scan == first {
		t.Fatalf("scan lease = %p, %t, %v", scan, shared, err)
	}
	_ = scan.Close()
	factory.Close()
	if context.Cause(first.Context()) == nil {
		t.Fatal("factory close left the session proxy lease active")
	}
}

func TestClosedFactoryRejectsTasksWithoutStartingPlaywright(t *testing.T) {
	t.Parallel()
	manager, err := egress.New(egress.Config{})
	if err != nil {
		t.Fatal(err)
	}
	factory, err := New(cgv.DefaultBrowserConfig(), manager)
	if err != nil {
		t.Fatal(err)
	}
	factory.Close()
	if _, err := factory.Open(context.Background(), Task{Purpose: egress.PurposeSession}); !errors.Is(err, ErrClosed) {
		t.Fatalf("Open() error = %v", err)
	}
	if _, err := factory.Open(nil, Task{Purpose: egress.PurposeSession}); err == nil { //nolint:staticcheck // verifies the nil boundary
		t.Fatal("Open(nil) error = nil")
	}
	factory.Close()
	if err := factory.ConfigureEgress(egress.Config{}); !errors.Is(err, ErrClosed) {
		t.Fatalf("ConfigureEgress() error = %v", err)
	}
}

func TestFactoryReconfiguresFutureEgress(t *testing.T) {
	t.Parallel()
	manager, err := egress.New(egress.Config{})
	if err != nil {
		t.Fatal(err)
	}
	factory, err := New(cgv.DefaultBrowserConfig(), manager)
	if err != nil {
		t.Fatal(err)
	}
	defer factory.Close()
	if err := factory.ConfigureEgress(egress.Config{SoxyURL: "https://soxy.test"}); err == nil {
		t.Fatal("ConfigureEgress(invalid) error = nil")
	}
	if err := factory.ConfigureEgress(egress.Config{}); err != nil {
		t.Fatalf("ConfigureEgress(direct) error = %v", err)
	}
	configured, err := factory.currentEgress()
	if err != nil || configured == manager {
		t.Fatalf("currentEgress() = %p, %v", configured, err)
	}
}
