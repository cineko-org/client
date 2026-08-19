package main

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	centralstore "github.com/cineko-org/client/internal/adapters/storage/centralhttp"
	central "github.com/cineko-org/contracts/v3"
)

const maximumLaunchPayload = 16 << 10

type desktopLaunchPayload struct {
	LaunchTicket             string `json:"launchTicket"`
	InstallationID           string `json:"installationId"`
	DeviceID                 string `json:"deviceId"`
	ReleaseGeneration        int64  `json:"releaseGeneration"`
	ClientVersion            string `json:"clientVersion"`
	ArtifactSHA256           string `json:"artifactSha256"`
	Protocol                 int    `json:"protocol"`
	BrowserRevision          string `json:"browserRevision"`
	BrowserArtifactSHA256    string `json:"browserArtifactSha256"`
	PlaywrightVersion        string `json:"playwrightVersion"`
	PlaywrightArtifactSHA256 string `json:"playwrightArtifactSha256"`
}

type desktopRuntimeIdentity struct {
	desktopIdentity
	ReleaseGeneration int64
	ClientVersion     string
	BrowserRevision   string
}

func openDesktopStore(
	ctx context.Context,
	dataDir string,
	input io.Reader,
) (*centralstore.Store, desktopRuntimeIdentity, error) {
	if desktopVersion == "dev" && os.Getenv("CINEKO_DEV_DIRECT") == "1" {
		store, err := centralstore.Open(ctx, centralstore.Config{
			BaseURL: os.Getenv("CINEKO_CENTRAL_URL"), UserID: os.Getenv("CINEKO_CENTRAL_USER_ID"),
			AccessToken: os.Getenv("CINEKO_CENTRAL_ACCESS_TOKEN"),
		})
		if err != nil {
			return nil, desktopRuntimeIdentity{}, err
		}
		identity, err := loadOrCreateDesktopIdentity(dataDir)
		if err != nil {
			_ = store.Close()
			return nil, desktopRuntimeIdentity{}, err
		}
		return store, desktopRuntimeIdentity{
			desktopIdentity: identity, ClientVersion: desktopVersion,
			ReleaseGeneration: store.ReleaseGeneration(),
			BrowserRevision:   strings.TrimSpace(os.Getenv("CINEKO_BROWSER_REVISION")),
		}, nil
	}
	payload, err := decodeDesktopLaunchPayload(input)
	if err != nil {
		return nil, desktopRuntimeIdentity{}, err
	}
	nonce, err := desktopClientNonce()
	if err != nil {
		return nil, desktopRuntimeIdentity{}, err
	}
	store, err := centralstore.OpenLaunched(ctx, centralstore.LaunchConfig{
		BaseURL: os.Getenv("CINEKO_CENTRAL_URL"), LaunchTicket: payload.LaunchTicket, ClientNonce: nonce,
		InstallationID: payload.InstallationID, DeviceID: payload.DeviceID,
		ReleaseGeneration: payload.ReleaseGeneration,
		ClientVersion:     payload.ClientVersion, ArtifactSHA256: payload.ArtifactSHA256,
		Protocol: payload.Protocol, BrowserRevision: payload.BrowserRevision,
		BrowserArtifactSHA256: payload.BrowserArtifactSHA256,
		PlaywrightVersion:     payload.PlaywrightVersion, PlaywrightArtifactSHA256: payload.PlaywrightArtifactSHA256,
	})
	if err != nil {
		if errors.Is(err, centralstore.ErrReleaseUpdateRequired) {
			return nil, desktopRuntimeIdentity{}, errUpdateRequired
		}
		return nil, desktopRuntimeIdentity{}, err
	}
	return store, desktopRuntimeIdentity{
		desktopIdentity: desktopIdentity{
			InstallationID: payload.InstallationID,
			DeviceID:       payload.DeviceID,
		},
		ReleaseGeneration: payload.ReleaseGeneration,
		ClientVersion:     payload.ClientVersion, BrowserRevision: payload.BrowserRevision,
	}, nil
}

func decodeDesktopLaunchPayload(input io.Reader) (desktopLaunchPayload, error) {
	if input == nil {
		return desktopLaunchPayload{}, errors.New("launcher pipe is required")
	}
	limited := io.LimitReader(input, maximumLaunchPayload+1)
	contents, err := io.ReadAll(limited)
	if err != nil {
		return desktopLaunchPayload{}, fmt.Errorf("read Launcher pipe: %w", err)
	}
	if len(contents) == 0 || len(contents) > maximumLaunchPayload {
		return desktopLaunchPayload{}, errors.New("launcher payload is missing or too large")
	}
	var payload desktopLaunchPayload
	decoder := json.NewDecoder(bytes.NewReader(contents))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&payload); err != nil {
		return desktopLaunchPayload{}, fmt.Errorf("decode Launcher payload: %w", err)
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return desktopLaunchPayload{}, errors.New("launcher pipe must contain one JSON value")
	}
	payload = normalizeDesktopLaunchPayload(payload)
	if err := validateDesktopLaunchPayload(payload); err != nil {
		return desktopLaunchPayload{}, err
	}
	return payload, nil
}

func normalizeDesktopLaunchPayload(payload desktopLaunchPayload) desktopLaunchPayload {
	payload.LaunchTicket = strings.TrimSpace(payload.LaunchTicket)
	payload.InstallationID = strings.TrimSpace(payload.InstallationID)
	payload.DeviceID = strings.TrimSpace(payload.DeviceID)
	payload.ClientVersion = strings.TrimSpace(payload.ClientVersion)
	payload.ArtifactSHA256 = strings.ToLower(strings.TrimSpace(payload.ArtifactSHA256))
	payload.BrowserRevision = strings.TrimSpace(payload.BrowserRevision)
	payload.BrowserArtifactSHA256 = strings.ToLower(strings.TrimSpace(payload.BrowserArtifactSHA256))
	payload.PlaywrightVersion = strings.TrimSpace(payload.PlaywrightVersion)
	payload.PlaywrightArtifactSHA256 = strings.ToLower(strings.TrimSpace(payload.PlaywrightArtifactSHA256))
	return payload
}

func validateDesktopLaunchPayload(payload desktopLaunchPayload) error {
	required := []string{
		payload.LaunchTicket, payload.InstallationID, payload.DeviceID, payload.ClientVersion,
		payload.BrowserRevision, payload.PlaywrightVersion,
	}
	for _, value := range required {
		if value == "" {
			return errors.New("launcher payload is incomplete")
		}
	}
	if payload.ReleaseGeneration < 1 || payload.Protocol != central.ProtocolVersion {
		return errors.New("launcher payload is incomplete")
	}
	for _, digest := range []string{
		payload.ArtifactSHA256, payload.BrowserArtifactSHA256, payload.PlaywrightArtifactSHA256,
	} {
		if !validDesktopArtifactDigest(digest) {
			return errors.New("launcher payload is incomplete")
		}
	}
	return nil
}

func validDesktopArtifactDigest(value string) bool {
	decoded, err := hex.DecodeString(strings.TrimSpace(value))
	return err == nil && len(decoded) == sha256.Size
}

func desktopClientNonce() (string, error) {
	buffer := make([]byte, 24)
	if _, err := rand.Read(buffer); err != nil {
		return "", fmt.Errorf("generate client nonce: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(buffer), nil
}
