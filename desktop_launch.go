package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	localstore "github.com/cineko-org/client/internal/adapters/storage/local"
	clientpb "github.com/cineko-org/contracts/v3/gen/go/cineko/client"
	"golang.org/x/mod/semver"
	"google.golang.org/protobuf/encoding/protojson"
)

const maximumLaunchPayload = 16 << 10

func openDesktopStore(
	ctx context.Context,
	dataDir string,
	input io.Reader,
) (*localstore.Store, *clientpb.LaunchContext, string, error) {
	store, err := localstore.Open(dataDir)
	if err != nil {
		return nil, nil, "", err
	}

	if desktopVersion == "dev" && os.Getenv("CINEKO_DEV_DIRECT") == "1" {
		clientVersion, err := devDirectClientVersion()
		if err != nil {
			_ = store.Close()
			return nil, nil, "", err
		}
		identity, err := loadOrCreateDesktopIdentity(dataDir)
		if err != nil {
			_ = store.Close()
			return nil, nil, "", err
		}
		launchContext := clientpb.LaunchContext_builder{
			InstallationId: &identity.InstallationID, DeviceId: &identity.DeviceID,
			ClientVersion: &clientVersion,
		}.Build()
		return store, launchContext, "", nil
	}
	payload, err := decodeDesktopLaunchPayload(input)
	if err != nil {
		_ = store.Close()
		return nil, nil, "", err
	}
	launchContext := payload.GetContext()
	_ = ctx
	return store, launchContext, strings.TrimSpace(os.Getenv("CINEKO_STARTUP_READY_NONCE")), nil
}

func devDirectClientVersion() (string, error) {
	value := strings.TrimSpace(os.Getenv("CINEKO_DEV_CLIENT_VERSION"))
	if value == "" {
		return desktopVersion, nil
	}
	canonical := value
	if !strings.HasPrefix(canonical, "v") {
		canonical = "v" + canonical
	}
	if !semver.IsValid(canonical) {
		return "", errors.New("CINEKO_DEV_CLIENT_VERSION must be a semantic version")
	}
	return value, nil
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
		strings.TrimSpace(context.GetInstallationId()), strings.TrimSpace(context.GetDeviceId()), strings.TrimSpace(context.GetClientVersion()),
	}
	for _, value := range required {
		if value == "" {
			return errors.New("launcher payload is incomplete")
		}
	}
	return nil
}
