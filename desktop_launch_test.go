package main

import (
	"fmt"
	"strings"
	"testing"

	central "github.com/cineko-org/contracts/v3"
)

func TestDecodeDesktopLaunchPayload(t *testing.T) {
	valid := fmt.Sprintf(`{"launchTicket":"ticket","installationId":"install","deviceId":"device","releaseGeneration":17,"clientVersion":"1.0.0","artifactSha256":"0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef","protocol":%d,"browserRevision":"1234","browserArtifactSha256":"1123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef","playwrightVersion":"1.60.0","playwrightArtifactSha256":"2123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef","startupReadyNonce":"startup-0123456789abcdef"}`, central.ProtocolVersion)
	payload, err := decodeDesktopLaunchPayload(strings.NewReader(valid))
	if err != nil || payload.InstallationID != "install" || payload.ReleaseGeneration != 17 || payload.Protocol != central.ProtocolVersion {
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
	if nonce, err := desktopClientNonce(); err != nil || len(nonce) < 16 {
		t.Fatalf("desktopClientNonce() = %q, %v", nonce, err)
	}
}
