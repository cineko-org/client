package main

import (
	"strings"
	"testing"
	"time"

	releasepb "github.com/cineko-org/contracts/v3/gen/go/cineko/release"
	"google.golang.org/protobuf/types/known/timestamppb"
)

func TestValidateRuntimeReleaseCompatibility(t *testing.T) {
	channel, platform, architecture := "stable", "darwin", "arm64"
	clientVersion, launcherVersion, browserRevision, playwrightVersion := "2.8.0", "1.4.0", "1234", "1.62.1"
	publishedAt := timestamppb.New(time.Date(2026, time.August, 24, 0, 0, 0, 0, time.UTC))
	artifact := func(name string) *releasepb.Artifact {
		url, size, digest := "https://cdn.example/cineko/releases/"+name, int64(1), strings.Repeat("a", 64)
		return releasepb.Artifact_builder{Url: &url, Size: &size, Sha256: &digest, Executable: &name}.Build()
	}
	client := releasepb.ClientRelease_builder{
		Channel: &channel, Platform: &platform, Architecture: &architecture, Version: &clientVersion,
		MinimumLauncherVersion: &launcherVersion, MinimumBrowserRevision: &browserRevision,
		PlaywrightVersion: &playwrightVersion, Artifact: artifact("client"), PublishedAt: publishedAt,
	}.Build()
	browser := releasepb.BrowserRelease_builder{
		Channel: &channel, Platform: &platform, Architecture: &architecture, Revision: &browserRevision,
		CompatiblePlaywrightVersions: []string{playwrightVersion}, Artifact: artifact("browser"), PublishedAt: publishedAt,
	}.Build()
	playwright := releasepb.PlaywrightRelease_builder{
		Channel: &channel, Platform: &platform, Architecture: &architecture, Version: &playwrightVersion,
		Artifact: artifact("playwright"), PublishedAt: publishedAt,
	}.Build()
	runtimeRelease := releasepb.RuntimeRelease_builder{Client: client, Browser: browser, Playwright: playwright}.Build()
	if err := validateRuntimeRelease(runtimeRelease); err != nil {
		t.Fatalf("compatible runtime rejected: %v", err)
	}

	client.SetPlaywrightVersion("different")
	if err := validateRuntimeRelease(runtimeRelease); err == nil {
		t.Fatal("incompatible runtime accepted")
	}
}
