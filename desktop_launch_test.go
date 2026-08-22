package main

import (
	"strings"
	"testing"

	clientpb "github.com/cineko-org/contracts/v3/gen/go/cineko/client"
	"google.golang.org/protobuf/encoding/protojson"
)

func TestDecodeDesktopLaunchPayload(t *testing.T) {
	message := clientpb.LaunchEnvelope_builder{
		LaunchTicket: launchStringPointer("ticket"),
		Context: clientpb.LaunchContext_builder{
			InstallationId:           launchStringPointer("install"),
			DeviceId:                 launchStringPointer("device"),
			ReleaseGeneration:        launchInt64Pointer(17),
			ClientVersion:            launchStringPointer("1.0.0"),
			ArtifactSha256:           launchStringPointer("0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"),
			BrowserRevision:          launchStringPointer("1234"),
			BrowserArtifactSha256:    launchStringPointer("1123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"),
			PlaywrightVersion:        launchStringPointer("1.60.0"),
			PlaywrightArtifactSha256: launchStringPointer("2123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"),
		}.Build(),
	}.Build()
	encoded, err := (protojson.MarshalOptions{UseProtoNames: false}).Marshal(message)
	if err != nil {
		t.Fatal(err)
	}
	valid := string(encoded)
	payload, err := decodeDesktopLaunchPayload(strings.NewReader(valid))
	if err != nil || payload.GetContext().GetInstallationId() != "install" || payload.GetContext().GetReleaseGeneration() != 17 {
		t.Fatalf("decodeDesktopLaunchPayload() = %+v, %v", payload, err)
	}
	for name, input := range map[string]string{
		"empty":      "",
		"unknown":    strings.TrimSuffix(valid, "}") + `,"extra":true}`,
		"trailing":   valid + `{}`,
		"incomplete": `{"launchTicket":"ticket"}`,
		"oversize":   strings.Repeat("x", maximumLaunchPayload+1),
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := decodeDesktopLaunchPayload(strings.NewReader(input)); err == nil {
				t.Fatal("invalid Launcher payload accepted")
			}
		})
	}
	if _, err := decodeDesktopLaunchPayload(nil); err == nil {
		t.Fatal("nil Launcher pipe accepted")
	}
}

func launchStringPointer(value string) *string { return &value }
func launchInt64Pointer(value int64) *int64    { return &value }
