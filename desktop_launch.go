package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"buf.build/go/protovalidate"
	centralstore "github.com/cineko-org/client/internal/adapters/storage/centralhttp"
	clientpb "github.com/cineko-org/contracts/gen/go/cineko/client"
	"google.golang.org/protobuf/encoding/protojson"
)

const maximumLaunchPayload = 16 << 10

func openDesktopStore(
	ctx context.Context,
	dataDir string,
	input io.Reader,
) (*centralstore.Store, *clientpb.LaunchContext, string, error) {
	if desktopVersion == "dev" && os.Getenv("CINEKO_DEV_DIRECT") == "1" {
		identity, err := loadOrCreateDesktopIdentity(dataDir)
		if err != nil {
			return nil, nil, "", err
		}
		store, err := centralstore.Open(ctx, centralstore.Config{
			BaseURL: os.Getenv("CINEKO_CENTRAL_URL"), UserID: os.Getenv("CINEKO_CENTRAL_USER_ID"),
			AccessToken: os.Getenv("CINEKO_CENTRAL_ACCESS_TOKEN"), InstallationID: identity.InstallationID,
		})
		if err != nil {
			return nil, nil, "", err
		}
		browserRevision := strings.TrimSpace(os.Getenv("CINEKO_BROWSER_REVISION"))
		releaseGeneration := store.ReleaseGeneration()
		launchContext := clientpb.LaunchContext_builder{
			InstallationId: &identity.InstallationID, DeviceId: &identity.DeviceID,
			ClientVersion: &desktopVersion, BrowserRevision: &browserRevision,
			ReleaseGeneration: &releaseGeneration,
		}.Build()
		return store, launchContext, "", nil
	}
	payload, err := decodeDesktopLaunchPayload(input)
	if err != nil {
		return nil, nil, "", err
	}
	launchContext := payload.GetContext()
	store, err := centralstore.OpenLaunched(ctx, payload, centralstore.LaunchOptions{
		BaseURL: os.Getenv("CINEKO_CENTRAL_URL"),
	})
	if err != nil {
		if errors.Is(err, centralstore.ErrReleaseUpdateRequired) {
			return nil, nil, "", errUpdateRequired
		}
		return nil, nil, "", err
	}
	return store, launchContext, strings.TrimSpace(os.Getenv("CINEKO_STARTUP_READY_NONCE")), nil
}

func decodeDesktopLaunchPayload(input io.Reader) (*clientpb.LaunchEnvelope, error) {
	if input == nil {
		return nil, errors.New("launcher pipe is required")
	}
	limited := io.LimitReader(input, maximumLaunchPayload+1)
	contents, err := io.ReadAll(limited)
	if err != nil {
		return nil, fmt.Errorf("read Launcher pipe: %w", err)
	}
	if len(contents) == 0 || len(contents) > maximumLaunchPayload {
		return nil, errors.New("launcher payload is missing or too large")
	}
	payload := &clientpb.LaunchEnvelope{}
	if err := (protojson.UnmarshalOptions{DiscardUnknown: false}).Unmarshal(contents, payload); err != nil {
		return nil, fmt.Errorf("decode Launcher payload: %w", err)
	}
	if err := protovalidate.Validate(payload); err != nil {
		return nil, fmt.Errorf("validate Launcher payload: %w", err)
	}
	if err := validateDesktopLaunchPayload(payload); err != nil {
		return nil, err
	}
	return payload, nil
}

func validateDesktopLaunchPayload(payload *clientpb.LaunchEnvelope) error {
	context := payload.GetContext()
	if context == nil {
		return errors.New("launcher payload is incomplete")
	}
	required := []string{
		strings.TrimSpace(payload.GetLaunchTicket()), strings.TrimSpace(context.GetInstallationId()), strings.TrimSpace(context.GetDeviceId()), strings.TrimSpace(context.GetClientVersion()),
		strings.TrimSpace(context.GetBrowserRevision()), strings.TrimSpace(context.GetPlaywrightVersion()),
	}
	for _, value := range required {
		if value == "" {
			return errors.New("launcher payload is incomplete")
		}
	}
	if context.GetReleaseGeneration() < 1 {
		return errors.New("launcher payload is incomplete")
	}
	for _, digest := range []string{
		context.GetArtifactSha256(), context.GetBrowserArtifactSha256(), context.GetPlaywrightArtifactSha256(),
	} {
		if !validDesktopArtifactDigest(digest) {
			return errors.New("launcher payload is incomplete")
		}
	}
	return nil
}

func validDesktopArtifactDigest(value string) bool {
	digest, err := hex.DecodeString(strings.TrimSpace(value))
	return err == nil && len(digest) == sha256.Size
}
