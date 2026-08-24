package main

import (
	"strings"
	"testing"

	clientpb "github.com/cineko-org/contracts/v3/gen/go/cineko/client"
	"google.golang.org/protobuf/encoding/protojson"
)

func TestDecodeDesktopLaunchPayload(t *testing.T) {
	message := clientpb.LaunchEnvelope_builder{
		Context: clientpb.LaunchContext_builder{
			InstallationId: launchStringPointer("install"),
			DeviceId:       launchStringPointer("device"),
			ClientVersion:  launchStringPointer("1.0.0"),
		}.Build(),
	}.Build()
	encoded, err := (protojson.MarshalOptions{UseProtoNames: false}).Marshal(message)
	if err != nil {
		t.Fatal(err)
	}
	valid := string(encoded)
	payload, err := decodeDesktopLaunchPayload(strings.NewReader(valid))
	if err != nil || payload.GetContext().GetInstallationId() != "install" || payload.GetContext().GetClientVersion() != "1.0.0" {
		t.Fatalf("decodeDesktopLaunchPayload() = %+v, %v", payload, err)
	}
	for name, input := range map[string]string{
		"empty":      "",
		"unknown":    strings.TrimSuffix(valid, "}") + `,"extra":true}`,
		"trailing":   valid + `{}`,
		"incomplete": `{"context":{"installationId":"install"}}`,
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

func TestDevDirectClientVersion(t *testing.T) {
	t.Setenv("CINEKO_DEV_CLIENT_VERSION", "")
	if value, err := devDirectClientVersion(); err != nil || value != desktopVersion {
		t.Fatalf("default dev direct version = %q, %v", value, err)
	}
	t.Setenv("CINEKO_DEV_CLIENT_VERSION", "0.0.1")
	if value, err := devDirectClientVersion(); err != nil || value != "0.0.1" {
		t.Fatalf("configured dev direct version = %q, %v", value, err)
	}
	t.Setenv("CINEKO_DEV_CLIENT_VERSION", "not-a-version")
	if _, err := devDirectClientVersion(); err == nil {
		t.Fatal("invalid dev direct version accepted")
	}
}

func launchStringPointer(value string) *string { return &value }
