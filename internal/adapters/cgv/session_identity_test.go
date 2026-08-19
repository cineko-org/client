package cgv

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestSessionIdentityRoundTrip(t *testing.T) {
	t.Parallel()
	config := DefaultBrowserConfig()
	config.ProfileDir = t.TempDir()
	config.UserAgentMode = UserAgentSession
	identity := testPersistentBrowserIdentity()

	if err := saveSessionIdentity(config, identity); err != nil {
		t.Fatalf("saveSessionIdentity() error = %v", err)
	}
	loaded, err := loadSessionIdentity(config)
	if err != nil {
		t.Fatalf("loadSessionIdentity() error = %v", err)
	}
	if !reflect.DeepEqual(*loaded, identity) {
		t.Fatalf("loaded identity = %#v, want %#v", *loaded, identity)
	}
	info, err := os.Stat(filepath.Join(config.ProfileDir, sessionIdentityFilename))
	if err != nil {
		t.Fatal(err)
	}
	if got := info.Mode().Perm(); got != 0o600 {
		t.Fatalf("identity mode = %o", got)
	}
}

func TestRandomizedScanIgnoresSessionIdentity(t *testing.T) {
	t.Parallel()
	config := DefaultBrowserConfig()
	config.ProfileDir = t.TempDir()
	config.UserAgentMode = UserAgentSession
	if err := saveSessionIdentity(config, testPersistentBrowserIdentity()); err != nil {
		t.Fatal(err)
	}
	config.UserAgentMode = UserAgentRandomizedScan
	identity, err := loadSessionIdentity(config)
	if err != nil || identity != nil {
		t.Fatalf("loadSessionIdentity(scan) = %#v, %v", identity, err)
	}
}

func TestInvalidSessionIdentityFailsClosed(t *testing.T) {
	t.Parallel()
	config := DefaultBrowserConfig()
	config.ProfileDir = t.TempDir()
	config.UserAgentMode = UserAgentSession
	if err := os.WriteFile(
		filepath.Join(config.ProfileDir, sessionIdentityFilename),
		[]byte(`{"version":1,"userAgent":{}}`),
		0o600,
	); err != nil {
		t.Fatal(err)
	}
	if _, err := loadSessionIdentity(config); err == nil {
		t.Fatal("loadSessionIdentity() error = nil")
	}
}

func TestSessionIdentityRejectsImpossibleARMWebGLCombination(t *testing.T) {
	if browserArchitecture() != "arm" {
		t.Skip("ARM-specific consistency rule")
	}
	identity := testPersistentBrowserIdentity()
	identity.WebGL = webGLIdentity{Vendor: "Intel Inc.", Renderer: "Intel Iris OpenGL Engine"}
	if err := identity.validate(); err == nil {
		t.Fatal("validate() error = nil")
	}
}

func testPersistentBrowserIdentity() persistentBrowserIdentity {
	return persistentBrowserIdentity{
		Version: sessionIdentityVersion,
		UserAgent: browserUserAgent{
			Value:       "Mozilla/5.0 Chrome/149.0.0.0 Safari/537.36",
			Major:       "149",
			FullVersion: "149.0.7827.55",
		},
		Metadata: userAgentBootstrapIdentity{
			Brands:          []uaBrandVersion{{Brand: "Chromium", Version: "149"}},
			FullVersionList: []uaBrandVersion{{Brand: "Chromium", Version: "149.0.7827.55"}},
			Platform:        browserUADataPlatform(),
			PlatformVersion: "",
			Architecture:    browserArchitecture(),
			Bitness:         "64",
			FormFactors:     []string{"Desktop"},
		},
		Languages: []string{"ko-KR", "ko"},
		WebGL:     testWebGLIdentity(),
	}
}

func testWebGLIdentity() webGLIdentity {
	if browserArchitecture() == "arm" {
		return webGLIdentity{Vendor: "Google Inc. (Apple)", Renderer: "ANGLE (Apple, Apple M1, OpenGL 4.1)"}
	}
	return webGLIdentity{Vendor: "Google Inc. (Intel)", Renderer: "ANGLE (Intel, OpenGL 4.1)"}
}
