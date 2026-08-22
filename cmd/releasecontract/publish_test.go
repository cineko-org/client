package main

import (
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"buf.build/go/protovalidate"
	releasepb "github.com/cineko-org/contracts/v3/gen/go/cineko/release"
	servicepb "github.com/cineko-org/contracts/v3/gen/go/cineko/service"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"
)

func TestPublishRequestUsesExactGeneratedServiceMessage(t *testing.T) {
	for _, component := range []string{"client", "browser", "playwright"} {
		t.Run(component, func(t *testing.T) {
			path := writeTestReleaseSet(t, component)
			request, err := publishRequest(component, path)
			if err != nil {
				t.Fatal(err)
			}
			switch value := request.(type) {
			case *servicepb.PublishClientRequest:
				if component != "client" || len(value.GetReleaseSet().GetReleases()) != 3 {
					t.Fatalf("Client publish request = %+v", value)
				}
			case *servicepb.PublishBrowserRequest:
				if component != "browser" || len(value.GetReleaseSet().GetReleases()) != 3 {
					t.Fatalf("browser publish request = %+v", value)
				}
			case *servicepb.PublishPlaywrightRequest:
				if component != "playwright" || len(value.GetReleaseSet().GetReleases()) != 3 {
					t.Fatalf("Playwright publish request = %+v", value)
				}
			default:
				t.Fatalf("publish request type = %T", request)
			}
		})
	}
}

func TestPublishReleaseStrictlyDecodesGeneratedResponse(t *testing.T) {
	for _, component := range []string{"client", "browser", "playwright"} {
		t.Run(component, func(t *testing.T) {
			input, err := publishRequest(component, writeTestReleaseSet(t, component))
			if err != nil {
				t.Fatal(err)
			}
			payload, err := protojson.Marshal(input)
			if err != nil {
				t.Fatal(err)
			}
			server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
				if request.Header.Get("Authorization") != "Bearer publisher" || request.Header.Get("Content-Type") != "application/json" {
					t.Errorf("publish headers = %v", request.Header)
				}
				decoded := newPublishRequest(component)
				body, err := io.ReadAll(request.Body)
				if err != nil {
					t.Errorf("read publish request: %v", err)
				}
				if err := (protojson.UnmarshalOptions{DiscardUnknown: false}).Unmarshal(body, decoded); err != nil {
					t.Errorf("decode publish request: %v", err)
				}
				if err := protovalidate.Validate(decoded); err != nil {
					t.Errorf("validate publish request: %v", err)
				}
				writer.Header().Set("X-Cineko-Release-Generation", "31")
				_, _ = writer.Write([]byte("{}"))
			}))
			defer server.Close()
			if err := publishRelease(
				t.Context(), server.Client(), func(time.Duration) {}, server.URL, component, "publisher", payload,
			); err != nil {
				t.Fatal(err)
			}
		})
	}

	for name, payload := range map[string][]byte{"empty": nil, "unknown": []byte(`{"generation":"31"}`)} {
		t.Run(name, func(t *testing.T) {
			if err := validatePublishResponse("client", payload); err == nil {
				t.Fatalf("invalid publish response accepted: %s", payload)
			}
		})
	}
}

func newPublishRequest(component string) proto.Message {
	switch component {
	case "client":
		return &servicepb.PublishClientRequest{}
	case "browser":
		return &servicepb.PublishBrowserRequest{}
	default:
		return &servicepb.PublishPlaywrightRequest{}
	}
}

func writeTestReleaseSet(t *testing.T, component string) string {
	t.Helper()
	publishedAt := timestamppb.New(time.Date(2026, 8, 23, 1, 2, 3, 0, time.UTC))
	platforms := []struct{ platform, architecture string }{
		{"darwin", "arm64"}, {"linux", "amd64"}, {"windows", "amd64"},
	}
	var message proto.Message
	switch component {
	case "client":
		values := make([]*releasepb.ClientRelease, 0, len(platforms))
		for _, target := range platforms {
			channel, version, minimumLauncher, browser, playwright := "stable", "1.2.3", "1.0.0", "1228", "1.61.1"
			values = append(values, releasepb.ClientRelease_builder{
				Channel: &channel, Platform: &target.platform, Architecture: &target.architecture,
				Version: &version, MinimumLauncherVersion: &minimumLauncher, MinimumBrowserRevision: &browser,
				PlaywrightVersion: &playwright, Artifact: testReleaseArtifact(target),
				ProbeBootstrapPublicKeys: map[string]string{"primary": "public-key"}, PublishedAt: publishedAt,
			}.Build())
		}
		message = releasepb.ClientReleaseSet_builder{Releases: values}.Build()
	case "browser":
		values := make([]*releasepb.BrowserRelease, 0, len(platforms))
		for _, target := range platforms {
			channel, revision := "stable", "1228"
			values = append(values, releasepb.BrowserRelease_builder{
				Channel: &channel, Platform: &target.platform, Architecture: &target.architecture,
				Revision: &revision, CompatiblePlaywrightVersions: []string{"1.61.1"},
				Artifact: testReleaseArtifact(target), PublishedAt: publishedAt,
			}.Build())
		}
		message = releasepb.BrowserReleaseSet_builder{Releases: values}.Build()
	default:
		values := make([]*releasepb.PlaywrightRelease, 0, len(platforms))
		for _, target := range platforms {
			channel, version := "stable", "1.61.1"
			values = append(values, releasepb.PlaywrightRelease_builder{
				Channel: &channel, Platform: &target.platform, Architecture: &target.architecture,
				Version: &version, Artifact: testReleaseArtifact(target), PublishedAt: publishedAt,
			}.Build())
		}
		message = releasepb.PlaywrightReleaseSet_builder{Releases: values}.Build()
	}
	payload, err := protojson.Marshal(message)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), component+"-release-set.json")
	if err := os.WriteFile(path, payload, 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func testReleaseArtifact(target struct{ platform, architecture string }) *releasepb.Artifact {
	url := "https://releases.example/cineko/" + target.platform + "-" + target.architecture + ".zip"
	size := int64(10)
	digest := strings.Repeat("a", 64)
	executable := "cineko"
	return releasepb.Artifact_builder{Url: &url, Size: &size, Sha256: &digest, Executable: &executable}.Build()
}
