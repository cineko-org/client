package centralhttp

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/cineko-org/client/internal/application"
	"github.com/cineko-org/client/internal/domain"
	central "github.com/cineko-org/contracts/v3"
)

const (
	maximumResponseBody = 8 << 20
	sessionRefreshSkew  = time.Minute
)

var errCentralUnauthorized = errors.New("central session is unauthorized")

var (
	ErrPINRateLimited        = errors.New("central PIN authentication is rate limited")
	ErrReleaseUpdateRequired = errors.New("central requires a different release generation")
)

type Config struct {
	BaseURL     string
	UserID      string
	AccessToken string
	HTTPClient  *http.Client
}

type LaunchConfig struct {
	BaseURL                  string
	LaunchTicket             string
	ClientNonce              string
	InstallationID           string
	DeviceID                 string
	ReleaseGeneration        int64
	ClientVersion            string
	ArtifactSHA256           string
	Protocol                 int
	BrowserRevision          string
	BrowserArtifactSHA256    string
	PlaywrightVersion        string
	PlaywrightArtifactSHA256 string
	HTTPClient               *http.Client
}

type Store struct {
	baseURL string
	userID  string
	client  *http.Client
	clock   func() time.Time

	authMu           sync.Mutex
	token            string
	expiresAt        time.Time
	refreshToken     string
	refreshExpiresAt time.Time

	leaseMu sync.Mutex
	leases  map[string]monitorLease

	releaseGeneration atomic.Int64
	updateRequired    chan struct{}
	updateOnce        sync.Once
	eventCursor       atomic.Int64
	resyncRequired    chan struct{}
	resyncOnce        sync.Once
	resourceChanged   chan struct{}
}

type monitorLease struct {
	owner     string
	expiresAt time.Time
}

type resourceEnvelope struct {
	Kind      string          `json:"kind"`
	ID        string          `json:"id"`
	Revision  int64           `json:"revision"`
	Data      json.RawMessage `json:"data"`
	CreatedAt time.Time       `json:"createdAt"`
	UpdatedAt time.Time       `json:"updatedAt"`
}

type resourceList struct {
	Data []resourceEnvelope `json:"data"`
}

type apiErrorEnvelope struct {
	Error struct {
		Code      string `json:"code"`
		Message   string `json:"message"`
		Retryable bool   `json:"retryable"`
		RequestID string `json:"requestId"`
	} `json:"error"`
}

func Open(ctx context.Context, config Config) (*Store, error) {
	store, err := newStore(config.BaseURL, config.UserID, config.HTTPClient)
	if err != nil {
		return nil, err
	}
	config.UserID = strings.TrimSpace(config.UserID)
	config.AccessToken = strings.TrimSpace(config.AccessToken)
	if config.UserID == "" || config.AccessToken == "" {
		return nil, errors.New("central user ID and access token are required")
	}
	var auth central.AuthExchangeResponse
	if err := store.request(ctx, http.MethodPost, "/v1/auth/exchange", false, map[string]string{
		"Content-Type": "application/json",
	}, central.AuthExchangeRequest{UserID: config.UserID, AccessToken: config.AccessToken}, &auth); err != nil {
		return nil, fmt.Errorf("authenticate with Central: %w", err)
	}
	if err := store.acceptSession(auth); err != nil {
		return nil, errors.New("central returned an invalid client session")
	}
	return store, nil
}

func OpenLaunched(ctx context.Context, config LaunchConfig) (*Store, error) {
	config = normalizeLaunchConfig(config)
	if err := validateLaunchConfig(config); err != nil {
		return nil, err
	}
	store, err := newStore(config.BaseURL, "", config.HTTPClient)
	if err != nil {
		return nil, err
	}
	store.releaseGeneration.Store(config.ReleaseGeneration)
	var auth central.AuthExchangeResponse
	if err := store.request(
		ctx,
		http.MethodPost,
		"/v1/client-sessions",
		false,
		map[string]string{"Content-Type": "application/json"},
		central.ClientSessionExchangeRequest{
			LaunchTicket: config.LaunchTicket,
			ClientNonce:  config.ClientNonce,
		},
		&auth,
	); err != nil {
		return nil, fmt.Errorf("exchange Central launch ticket: %w", err)
	}
	if store.releaseUpdateNeeded() {
		return nil, ErrReleaseUpdateRequired
	}
	store.userID = strings.TrimSpace(auth.User.ID)
	if store.userID == "" || !launchContextMatches(auth.Launch, config) || store.acceptSession(auth) != nil {
		return nil, errors.New("central returned an invalid launched client session")
	}
	return store, nil
}

func normalizeLaunchConfig(config LaunchConfig) LaunchConfig {
	config.LaunchTicket = strings.TrimSpace(config.LaunchTicket)
	config.ClientNonce = strings.TrimSpace(config.ClientNonce)
	config.InstallationID = strings.TrimSpace(config.InstallationID)
	config.DeviceID = strings.TrimSpace(config.DeviceID)
	config.ClientVersion = strings.TrimSpace(config.ClientVersion)
	config.ArtifactSHA256 = strings.ToLower(strings.TrimSpace(config.ArtifactSHA256))
	config.BrowserRevision = strings.TrimSpace(config.BrowserRevision)
	config.BrowserArtifactSHA256 = strings.ToLower(strings.TrimSpace(config.BrowserArtifactSHA256))
	config.PlaywrightVersion = strings.TrimSpace(config.PlaywrightVersion)
	config.PlaywrightArtifactSHA256 = strings.ToLower(strings.TrimSpace(config.PlaywrightArtifactSHA256))
	return config
}

func validateLaunchConfig(config LaunchConfig) error {
	required := []string{
		config.LaunchTicket, config.InstallationID, config.DeviceID, config.ClientVersion,
		config.BrowserRevision, config.PlaywrightVersion,
	}
	for _, value := range required {
		if value == "" {
			return errors.New("launched client identity is incomplete")
		}
	}
	if len(config.ClientNonce) < 16 || config.ReleaseGeneration < 1 || config.Protocol != central.ProtocolVersion {
		return errors.New("launched client identity is incomplete")
	}
	for _, digest := range []string{
		config.ArtifactSHA256, config.BrowserArtifactSHA256, config.PlaywrightArtifactSHA256,
	} {
		if !validSHA256(digest) {
			return errors.New("launched client identity is incomplete")
		}
	}
	return nil
}

func launchContextMatches(context *central.ClientLaunchContext, config LaunchConfig) bool {
	return context != nil && context.InstallationID == config.InstallationID &&
		context.DeviceID == config.DeviceID && context.ClientVersion == config.ClientVersion &&
		context.ReleaseGeneration == config.ReleaseGeneration &&
		strings.ToLower(context.ArtifactSHA256) == config.ArtifactSHA256 &&
		context.Protocol == config.Protocol && context.BrowserRevision == config.BrowserRevision &&
		strings.ToLower(context.BrowserArtifactSHA256) == config.BrowserArtifactSHA256 &&
		context.PlaywrightVersion == config.PlaywrightVersion &&
		strings.ToLower(context.PlaywrightArtifactSHA256) == config.PlaywrightArtifactSHA256
}

func validSHA256(value string) bool {
	decoded, err := hex.DecodeString(value)
	return err == nil && len(decoded) == sha256.Size
}

func newStore(baseURL string, userID string, client *http.Client) (*Store, error) {
	validatedURL, err := validateBaseURL(baseURL)
	if err != nil {
		return nil, err
	}
	if client == nil {
		client = &http.Client{Timeout: 20 * time.Second}
	}
	return &Store{
		baseURL: validatedURL, userID: strings.TrimSpace(userID), client: client,
		clock: time.Now, leases: make(map[string]monitorLease), updateRequired: make(chan struct{}),
		resyncRequired:  make(chan struct{}),
		resourceChanged: make(chan struct{}, 1),
	}, nil
}

func (store *Store) UserID() string { return store.userID }

func (store *Store) ReleaseGeneration() int64 { return store.releaseGeneration.Load() }

func (store *Store) UpdateRequired() <-chan struct{} { return store.updateRequired }

func (store *Store) ResyncRequired() <-chan struct{} { return store.resyncRequired }

func (store *Store) ResourceChanged() <-chan struct{} { return store.resourceChanged }

func (store *Store) releaseUpdateNeeded() bool {
	select {
	case <-store.updateRequired:
		return true
	default:
		return false
	}
}

func (store *Store) Close() error { return nil }

func (store *Store) GetSettings(ctx context.Context, output any) (int64, error) {
	var envelope resourceEnvelope
	if err := store.request(ctx, http.MethodGet, "/v1/settings", true, nil, nil, &envelope); err != nil {
		return 0, err
	}
	if output == nil {
		return envelope.Revision, nil
	}
	if err := store.validateEmbeddedOwnership(envelope.Data); err != nil {
		return 0, err
	}
	if err := json.Unmarshal(envelope.Data, output); err != nil {
		return 0, fmt.Errorf("decode Central settings: %w", err)
	}
	return envelope.Revision, nil
}

func (store *Store) PutSettings(ctx context.Context, input any, expectedRevision int64) error {
	if expectedRevision < 0 {
		return errors.New("settings revision cannot be negative")
	}
	payload, err := json.Marshal(input)
	if err != nil {
		return fmt.Errorf("encode Central settings: %w", err)
	}
	if err := store.validateEmbeddedOwnership(payload); err != nil {
		return err
	}
	headers := map[string]string{"Content-Type": "application/json"}
	if expectedRevision > 0 {
		headers["If-Match"] = strconv.FormatInt(expectedRevision, 10)
	} else {
		headers["If-None-Match"] = "*"
	}
	headers["Idempotency-Key"] = commandID("put", "settings", "settings", expectedRevision, payload)
	var saved resourceEnvelope
	return store.request(ctx, http.MethodPut, "/v1/settings", true, headers, map[string]any{
		"id": "settings", "data": json.RawMessage(payload),
	}, &saved)
}

func (store *Store) Logout(ctx context.Context) error {
	return store.request(ctx, http.MethodPost, "/v1/auth/logout", true, nil, nil, nil)
}

func (store *Store) RegisterDevice(
	ctx context.Context,
	device central.ClientDevice,
) (central.ClientDevice, error) {
	var registered central.ClientDevice
	if err := store.request(
		ctx, http.MethodPut, "/v1/devices/"+url.PathEscape(device.InstallationID), true,
		map[string]string{"Content-Type": "application/json"}, device, &registered,
	); err != nil {
		return central.ClientDevice{}, err
	}
	return registered, nil
}

func (store *Store) IssueProbeBootstrapTicket(
	ctx context.Context,
	request central.ProbeBootstrapTicketRequest,
) (central.ProbeBootstrapTicketResponse, error) {
	var response central.ProbeBootstrapTicketResponse
	if err := store.request(
		ctx,
		http.MethodPost,
		"/v1/probe-bootstrap-tickets",
		true,
		map[string]string{"Content-Type": "application/json"},
		request,
		&response,
	); err != nil {
		return central.ProbeBootstrapTicketResponse{}, err
	}
	return response, nil
}

func (store *Store) ClaimExecution(
	ctx context.Context,
	installationID string,
) (*central.ExecutionCommand, error) {
	var command central.ExecutionCommand
	if err := store.request(
		ctx, http.MethodPost, "/v1/executions:claim", true,
		map[string]string{"Content-Type": "application/json"},
		central.ExecutionClaimRequest{InstallationID: installationID}, &command,
	); err != nil {
		return nil, err
	}
	if command.ID == "" {
		return nil, nil
	}
	return &command, nil
}

func (store *Store) HeartbeatExecution(
	ctx context.Context,
	commandID string,
	leaseToken string,
) (central.ExecutionHeartbeatResponse, error) {
	var response central.ExecutionHeartbeatResponse
	if err := store.request(
		ctx, http.MethodPut, "/v1/executions/"+url.PathEscape(commandID)+"/heartbeat", true,
		map[string]string{"Content-Type": "application/json"},
		central.ExecutionHeartbeatRequest{LeaseToken: leaseToken}, &response,
	); err != nil {
		return central.ExecutionHeartbeatResponse{}, err
	}
	return response, nil
}

func (store *Store) CompleteExecution(
	ctx context.Context,
	commandID string,
	request central.ExecutionResultRequest,
) error {
	return store.request(
		ctx, http.MethodPut, "/v1/executions/"+url.PathEscape(commandID)+"/result", true,
		map[string]string{"Content-Type": "application/json"}, request, nil,
	)
}

func (store *Store) PutTheater(ctx context.Context, value domain.Theater) error {
	return store.PublishCatalogSnapshot(ctx, central.CatalogSnapshot{
		Provider: central.Provider{ID: value.ProviderID, Name: strings.ToUpper(value.ProviderID)},
		Theaters: []central.Theater{toContractTheater(value)}, ObservedAt: value.ObservedAt,
	})
}

func (store *Store) GetTheater(ctx context.Context, id string) (domain.Theater, error) {
	catalog, err := store.GetCatalog(ctx)
	if err != nil {
		return domain.Theater{}, err
	}
	for _, value := range catalog.Theaters {
		if value.ID == id {
			return fromContractTheater(value), nil
		}
	}
	return domain.Theater{}, application.ErrNotFound
}

func (store *Store) ListTheaters(ctx context.Context) ([]domain.Theater, error) {
	catalog, err := store.GetCatalog(ctx)
	if err != nil {
		return nil, err
	}
	values := make([]domain.Theater, 0, len(catalog.Theaters))
	for _, value := range catalog.Theaters {
		values = append(values, fromContractTheater(value))
	}
	return values, nil
}

func (store *Store) PutAuditorium(ctx context.Context, value domain.Auditorium) error {
	theater, err := store.GetTheater(ctx, value.TheaterID)
	if err != nil {
		return fmt.Errorf("read auditorium theater from Central: %w", err)
	}
	return store.PublishCatalogSnapshot(ctx, central.CatalogSnapshot{
		Provider:    central.Provider{ID: central.ProviderCGV, Name: "CGV"},
		Theaters:    []central.Theater{toContractTheater(theater)},
		Auditoriums: []central.Auditorium{toContractAuditorium(value)}, ObservedAt: value.ObservedAt,
	})
}

func (store *Store) GetAuditorium(ctx context.Context, id string) (domain.Auditorium, error) {
	catalog, err := store.GetCatalog(ctx)
	if err != nil {
		return domain.Auditorium{}, err
	}
	for _, value := range catalog.Auditoriums {
		if value.ID == id {
			return fromContractAuditorium(value), nil
		}
	}
	return domain.Auditorium{}, application.ErrNotFound
}

func (store *Store) ListAuditoriumsByTheater(ctx context.Context, theaterID string) ([]domain.Auditorium, error) {
	catalog, err := store.GetCatalog(ctx)
	if err != nil {
		return nil, err
	}
	values := make([]domain.Auditorium, 0)
	for _, value := range catalog.Auditoriums {
		if value.TheaterID == theaterID {
			values = append(values, fromContractAuditorium(value))
		}
	}
	return values, nil
}

func (store *Store) PutSeatMap(ctx context.Context, value domain.SeatMap) error {
	layout, err := json.Marshal(struct {
		Seats  []domain.Seat        `json:"seats"`
		Zones  []domain.LayoutZone  `json:"zones"`
		Blocks []domain.LayoutBlock `json:"blocks"`
	}{Seats: value.Seats, Zones: value.Zones, Blocks: value.Blocks})
	if err != nil {
		return fmt.Errorf("encode seat map layout: %w", err)
	}
	var decoded any
	if err := json.Unmarshal(layout, &decoded); err != nil {
		return fmt.Errorf("normalize seat map layout: %w", err)
	}
	layout, err = json.Marshal(decoded)
	if err != nil {
		return fmt.Errorf("encode normalized seat map layout: %w", err)
	}
	digest := sha256.Sum256(layout)
	layoutHash := hex.EncodeToString(digest[:])
	version := central.SeatMapVersion{
		ID:           central.SeatMapVersionID(value.AuditoriumID, layoutHash),
		AuditoriumID: value.AuditoriumID, LayoutHash: layoutHash,
		Capacity: len(value.Seats), Layout: layout,
		ObservedAt: value.ObservedAt,
	}
	return store.request(ctx, http.MethodPut,
		"/v1/catalog/seat-map-versions/"+url.PathEscape(version.ID), true,
		map[string]string{
			"Content-Type":    "application/json",
			"Idempotency-Key": version.ID,
		}, version, nil)
}

func (store *Store) GetSeatMap(ctx context.Context, auditoriumID string) (domain.SeatMap, error) {
	var version central.SeatMapVersion
	if err := store.request(ctx, http.MethodGet,
		"/v1/catalog/auditoriums/"+url.PathEscape(auditoriumID)+"/seat-map", true,
		nil, nil, &version); err != nil {
		return domain.SeatMap{}, err
	}
	var layout struct {
		Seats  []domain.Seat        `json:"seats"`
		Zones  []domain.LayoutZone  `json:"zones"`
		Blocks []domain.LayoutBlock `json:"blocks"`
	}
	if err := json.Unmarshal(version.Layout, &layout); err != nil {
		return domain.SeatMap{}, fmt.Errorf("decode Central seat map layout: %w", err)
	}
	return domain.SeatMap{
		AuditoriumID: version.AuditoriumID, Version: version.LayoutHash,
		Seats: layout.Seats, Zones: layout.Zones, Blocks: layout.Blocks,
		ObservedAt: version.ObservedAt,
	}, nil
}

func (store *Store) RequestSeatMapBackfill(ctx context.Context, auditoriumID string) error {
	return store.request(ctx, http.MethodPost,
		"/v1/catalog/auditoriums/"+url.PathEscape(auditoriumID)+"/seat-map:request", true,
		nil, nil, nil)
}

func (store *Store) PutPreset(ctx context.Context, value domain.Preset) error {
	return store.put(ctx, "presets", value.ID, value)
}

func (store *Store) GetPreset(ctx context.Context, id string) (domain.Preset, error) {
	return getValue[domain.Preset](ctx, store, "presets", id)
}

func (store *Store) ListPresetsByUser(ctx context.Context, userID string) ([]domain.Preset, error) {
	if err := store.owns(userID); err != nil {
		return nil, err
	}
	return listValues[domain.Preset](ctx, store, "presets")
}

func (store *Store) DeletePreset(ctx context.Context, id string) error {
	return store.delete(ctx, "presets", id)
}

func (store *Store) PutMonitor(ctx context.Context, value domain.MonitorJob) error {
	return store.put(ctx, "monitors", value.ID, value)
}

func (store *Store) GetMonitor(ctx context.Context, id string) (domain.MonitorJob, error) {
	return getValue[domain.MonitorJob](ctx, store, "monitors", id)
}

func (store *Store) ListMonitorsByUser(ctx context.Context, userID string) ([]domain.MonitorJob, error) {
	if err := store.owns(userID); err != nil {
		return nil, err
	}
	return listValues[domain.MonitorJob](ctx, store, "monitors")
}

func (store *Store) DeleteMonitor(ctx context.Context, id string) error {
	return store.delete(ctx, "monitors", id)
}

func (store *Store) AcquireMonitor(
	ctx context.Context,
	id string,
	owner string,
	now time.Time,
	ttl time.Duration,
) (domain.MonitorJob, error) {
	store.leaseMu.Lock()
	defer store.leaseMu.Unlock()
	if lease, exists := store.leases[id]; exists && lease.owner != owner && lease.expiresAt.After(now) {
		return domain.MonitorJob{}, application.ErrConflict
	}
	monitor, err := store.GetMonitor(ctx, id)
	if err != nil {
		return domain.MonitorJob{}, err
	}
	store.leases[id] = monitorLease{owner: owner, expiresAt: now.Add(ttl)}
	return monitor, nil
}

func (store *Store) RenewMonitor(
	_ context.Context,
	id string,
	owner string,
	now time.Time,
	ttl time.Duration,
) error {
	store.leaseMu.Lock()
	defer store.leaseMu.Unlock()
	lease, exists := store.leases[id]
	if !exists || lease.owner != owner || !lease.expiresAt.After(now) {
		return application.ErrConflict
	}
	store.leases[id] = monitorLease{owner: owner, expiresAt: now.Add(ttl)}
	return nil
}

func (store *Store) ReleaseMonitor(_ context.Context, id string, owner string) error {
	store.leaseMu.Lock()
	defer store.leaseMu.Unlock()
	if lease, exists := store.leases[id]; exists && lease.owner == owner {
		delete(store.leases, id)
	}
	return nil
}

func (store *Store) PutReservation(ctx context.Context, value domain.Reservation) error {
	return store.put(ctx, "reservations", value.ID, value)
}

func (store *Store) GetReservation(ctx context.Context, id string) (domain.Reservation, error) {
	return getValue[domain.Reservation](ctx, store, "reservations", id)
}

func (store *Store) ListReservationsByUser(ctx context.Context, userID string) ([]domain.Reservation, error) {
	if err := store.owns(userID); err != nil {
		return nil, err
	}
	return listValues[domain.Reservation](ctx, store, "reservations")
}

func (store *Store) PutExternalOperation(ctx context.Context, value domain.ExternalOperation) error {
	return store.put(ctx, "external-operations", value.ID, value)
}

func (store *Store) PublishCatalogSnapshot(ctx context.Context, value central.CatalogSnapshot) error {
	return store.request(ctx, http.MethodPost, "/v1/catalog/snapshots", true,
		map[string]string{
			"Content-Type":    "application/json",
			"Idempotency-Key": catalogSnapshotKey(value),
		}, value, nil)
}

func (store *Store) GetCatalog(ctx context.Context) (central.CatalogIndex, error) {
	var catalog central.CatalogIndex
	if err := store.request(ctx, http.MethodGet, "/v1/catalog", true, nil, nil, &catalog); err != nil {
		return central.CatalogIndex{}, err
	}
	return catalog, nil
}

func catalogSnapshotKey(value central.CatalogSnapshot) string {
	payload, _ := json.Marshal(value)
	digest := sha256.Sum256(payload)
	return "catalog-snapshot-" + hex.EncodeToString(digest[:])
}

func toContractTheater(value domain.Theater) central.Theater {
	return central.Theater{
		ID: value.ID, ProviderID: value.ProviderID, SourceKey: value.SourceKey,
		Region: value.Region, Name: value.Name,
	}
}

func fromContractTheater(value central.Theater) domain.Theater {
	return domain.Theater{
		ID: value.ID, ProviderID: value.ProviderID, SourceKey: value.SourceKey,
		Region: value.Region, Name: value.Name,
	}
}

func toContractAuditorium(value domain.Auditorium) central.Auditorium {
	return central.Auditorium{
		ID: value.ID, TheaterID: value.TheaterID, SourceKey: value.SourceKey,
		Name: value.Name, ScreenTypes: value.ScreenTypes, Capacity: value.Capacity,
		SeatMapVersion: value.SeatMapVersion,
	}
}

func fromContractAuditorium(value central.Auditorium) domain.Auditorium {
	return domain.Auditorium{
		ID: value.ID, TheaterID: value.TheaterID, SourceKey: value.SourceKey,
		Name: value.Name, ScreenTypes: value.ScreenTypes, Capacity: value.Capacity,
		SeatMapVersion: value.SeatMapVersion,
	}
}

func (store *Store) PutAppEvent(ctx context.Context, value domain.AppEvent) error {
	return store.put(ctx, "app-events", value.ID, value)
}

func (store *Store) ListAppEvents(ctx context.Context, userID string, limit int) ([]domain.AppEvent, error) {
	if err := store.owns(userID); err != nil {
		return nil, err
	}
	values, err := listValues[domain.AppEvent](ctx, store, "app-events")
	if err != nil {
		return nil, err
	}
	if limit > 0 && len(values) > limit {
		values = values[:limit]
	}
	return values, nil
}

func (store *Store) MarkAppEventsRead(ctx context.Context, userID string, at time.Time) error {
	values, err := store.ListAppEvents(ctx, userID, 0)
	if err != nil {
		return err
	}
	for _, value := range values {
		if value.ReadAt == nil {
			value.ReadAt = &at
			if err := store.PutAppEvent(ctx, value); err != nil {
				return err
			}
		}
	}
	return nil
}

func (store *Store) DeleteAppEvents(ctx context.Context, userID string) error {
	values, err := store.ListAppEvents(ctx, userID, 0)
	if err != nil {
		return err
	}
	for _, value := range values {
		if err := store.delete(ctx, "app-events", value.ID); err != nil {
			return err
		}
	}
	return nil
}

func (store *Store) DeleteAppEventsBefore(ctx context.Context, cutoff time.Time) error {
	values, err := store.ListAppEvents(ctx, store.userID, 0)
	if err != nil {
		return err
	}
	for _, value := range values {
		if value.CreatedAt.Before(cutoff) {
			if err := store.delete(ctx, "app-events", value.ID); err != nil {
				return err
			}
		}
	}
	return nil
}

func (store *Store) RecoverInterruptedWork(context.Context, time.Time) ([]domain.AppEvent, error) {
	return []domain.AppEvent{}, nil
}

func (store *Store) owns(userID string) error {
	if userID != store.userID {
		return application.ErrNotFound
	}
	return nil
}

func (store *Store) put(ctx context.Context, kind string, id string, value any) error {
	payload, err := resourcePayload(value)
	if err != nil {
		return fmt.Errorf("encode Central %s resource: %w", kind, err)
	}
	if err := store.validateEmbeddedOwnership(payload); err != nil {
		return err
	}
	headers := map[string]string{"Content-Type": "application/json"}
	method, path := http.MethodPut, resourcePath(kind, id)
	revision := observedRevision(value)
	if revision == 0 {
		method, path = http.MethodPost, "/v1/"+url.PathEscape(kind)
		headers["If-None-Match"] = "*"
	} else {
		headers["If-Match"] = strconv.FormatInt(revision, 10)
	}
	headers["Idempotency-Key"] = commandID("put", kind, id, revision, payload)
	body := map[string]any{"id": id, "data": json.RawMessage(payload)}
	var saved resourceEnvelope
	if err := store.request(ctx, method, path, true, headers, body, &saved); err != nil {
		return err
	}
	return nil
}

func (store *Store) delete(ctx context.Context, kind string, id string) error {
	current, err := store.getEnvelope(ctx, kind, id)
	if errors.Is(err, application.ErrNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	var deleted resourceEnvelope
	return store.request(ctx, http.MethodDelete, resourcePath(kind, id), true, map[string]string{
		"Idempotency-Key": commandID("delete", kind, id, current.Revision, nil),
		"If-Match":        strconv.FormatInt(current.Revision, 10),
	}, nil, &deleted)
}

func (store *Store) getEnvelope(ctx context.Context, kind string, id string) (resourceEnvelope, error) {
	var envelope resourceEnvelope
	err := store.request(ctx, http.MethodGet, resourcePath(kind, id), true, nil, nil, &envelope)
	return envelope, err
}

func (store *Store) listEnvelopes(ctx context.Context, kind string) ([]resourceEnvelope, error) {
	var response resourceList
	if err := store.request(ctx, http.MethodGet, "/v1/"+url.PathEscape(kind), true, nil, nil, &response); err != nil {
		return nil, err
	}
	return response.Data, nil
}

func (store *Store) request(
	ctx context.Context,
	method string,
	path string,
	authenticated bool,
	headers map[string]string,
	input any,
	output any,
) error {
	if !authenticated {
		return store.doRequest(ctx, method, path, "", headers, input, output)
	}
	token, err := store.sessionToken(ctx, false)
	if err != nil {
		return err
	}
	err = store.doRequest(ctx, method, path, token, headers, input, output)
	if !errors.Is(err, errCentralUnauthorized) {
		return err
	}
	token, err = store.sessionToken(ctx, true)
	if err != nil {
		return err
	}
	return store.doRequest(ctx, method, path, token, headers, input, output)
}

func (store *Store) doRequest(
	ctx context.Context,
	method string,
	path string,
	token string,
	headers map[string]string,
	input any,
	output any,
) error {
	var body io.Reader
	if input != nil {
		encoded, err := json.Marshal(input)
		if err != nil {
			return fmt.Errorf("encode Central request: %w", err)
		}
		body = bytes.NewReader(encoded)
	}
	endpoint := store.baseURL + path
	request, err := http.NewRequestWithContext(ctx, method, endpoint, body)
	if err != nil {
		return fmt.Errorf("create Central request: %w", err)
	}
	request.Header.Set("X-Cineko-Protocol", strconv.Itoa(central.ProtocolVersion))
	if token != "" {
		request.Header.Set("Authorization", "Bearer "+token)
	}
	for name, value := range headers {
		request.Header.Set(name, value)
	}
	response, err := store.client.Do(request)
	if err != nil {
		return fmt.Errorf("send Central request: %w", err)
	}
	defer func() { _ = response.Body.Close() }()
	if err := store.observeReleaseGeneration(response.Header.Get(central.ReleaseGenerationHeader)); err != nil {
		return err
	}
	contents, err := io.ReadAll(io.LimitReader(response.Body, maximumResponseBody+1))
	if err != nil {
		return fmt.Errorf("read Central response: %w", err)
	}
	if len(contents) > maximumResponseBody {
		return errors.New("central response exceeds size limit")
	}
	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		return decodeAPIError(response.StatusCode, contents)
	}
	if output == nil || len(contents) == 0 {
		return nil
	}
	if err := json.Unmarshal(contents, output); err != nil {
		return fmt.Errorf("decode Central response: %w", err)
	}
	return nil
}

func (store *Store) observeReleaseGeneration(value string) error {
	value = strings.TrimSpace(value)
	if value == "" {
		return errors.New("central response is missing its release generation")
	}
	generation, err := strconv.ParseInt(value, 10, 64)
	if err != nil || generation < 1 {
		return errors.New("central returned an invalid release generation")
	}
	expected := store.releaseGeneration.Load()
	if expected == 0 {
		if store.releaseGeneration.CompareAndSwap(0, generation) {
			return nil
		}
		expected = store.releaseGeneration.Load()
	}
	if generation != expected {
		store.updateOnce.Do(func() { close(store.updateRequired) })
	}
	return nil
}

// WatchEvents follows Central's existing user event stream. Release changes are
// carried by the response header and heartbeat comments, so this does not add a
// version polling loop or a second update-only connection.
func (store *Store) WatchEvents(ctx context.Context) error {
	backoff := 250 * time.Millisecond
	for {
		err := store.watchEventsOnce(ctx)
		if err == nil {
			select {
			case <-store.updateRequired:
				return nil
			default:
				continue
			}
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if !isRetryableEventStreamError(err) {
			return err
		}
		wait := jitteredEventBackoff(backoff)
		if backoff < 8*time.Second {
			backoff *= 2
		}
		select {
		case <-store.updateRequired:
			return nil
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(wait):
		}
	}
}

type eventStreamTransportError struct{ err error }

func (value eventStreamTransportError) Error() string { return value.err.Error() }
func (value eventStreamTransportError) Unwrap() error { return value.err }

type eventStreamHTTPError struct{ status int }

func (value eventStreamHTTPError) Error() string {
	return fmt.Sprintf("Central event stream failed with HTTP %d", value.status)
}

func isRetryableEventStreamError(err error) bool {
	var transport eventStreamTransportError
	var response eventStreamHTTPError
	return errors.As(err, &transport) || (errors.As(err, &response) && response.status >= 500)
}

func jitteredEventBackoff(base time.Duration) time.Duration {
	// Each reconnect spreads over [0.75x, 1.25x]. The clock-derived input is
	// only contention jitter; it is not security-sensitive.
	nanoseconds := time.Now().UnixNano() & 0xffff
	factor := 0.75 + (float64(nanoseconds)/65535.0)*0.5
	return time.Duration(float64(base) * factor)
}

func (store *Store) watchEventsOnce(ctx context.Context) error {
	token, err := store.sessionToken(ctx, false)
	if err != nil {
		return err
	}
	err = store.watchEventsWithToken(ctx, token)
	if !errors.Is(err, errCentralUnauthorized) {
		return err
	}
	token, err = store.sessionToken(ctx, true)
	if err != nil {
		return err
	}
	return store.watchEventsWithToken(ctx, token)
}

func (store *Store) watchEventsWithToken(ctx context.Context, token string) error {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, store.baseURL+"/v1/events/stream", nil)
	if err != nil {
		return fmt.Errorf("create Central event stream request: %w", err)
	}
	request.Header.Set(central.ProtocolHeader, central.ProtocolHeaderValue())
	request.Header.Set("Authorization", "Bearer "+token)
	if cursor := store.eventCursor.Load(); cursor > 0 {
		request.Header.Set("Last-Event-ID", strconv.FormatInt(cursor, 10))
	}
	streamClient := *store.client
	streamClient.Timeout = 0
	response, err := streamClient.Do(request)
	if err != nil {
		return eventStreamTransportError{err: fmt.Errorf("open Central event stream: %w", err)}
	}
	defer func() { _ = response.Body.Close() }()
	if err := store.validateEventStreamResponse(response); err != nil {
		return err
	}
	return store.consumeEventStream(response.Body)
}

func (store *Store) validateEventStreamResponse(response *http.Response) error {
	if err := store.observeReleaseGeneration(response.Header.Get(central.ReleaseGenerationHeader)); err != nil {
		return err
	}
	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		contents, readErr := io.ReadAll(io.LimitReader(response.Body, maximumResponseBody+1))
		if readErr != nil {
			return fmt.Errorf("read Central event stream error: %w", readErr)
		}
		if response.StatusCode == http.StatusUnauthorized {
			return decodeAPIError(response.StatusCode, contents)
		}
		return eventStreamHTTPError{status: response.StatusCode}
	}
	mediaType, _, mediaErr := mime.ParseMediaType(response.Header.Get("Content-Type"))
	if mediaErr != nil || !strings.EqualFold(mediaType, "text/event-stream") {
		return errors.New("central event stream returned an unexpected content type")
	}
	return nil
}

func (store *Store) consumeEventStream(body io.Reader) error {
	scanner := bufio.NewScanner(body)
	scanner.Buffer(make([]byte, 4096), maximumResponseBody)
	parser := newSSEParser(store.eventCursor.Load())
	for scanner.Scan() {
		event, complete, err := parser.Consume(scanner.Text())
		if err != nil {
			return err
		}
		if complete {
			if err := store.consumeSSEEvent(event); err != nil {
				return err
			}
		}
		select {
		case <-store.updateRequired:
			return nil
		default:
		}
	}
	if err := scanner.Err(); err != nil {
		return eventStreamTransportError{err: fmt.Errorf("read Central event stream: %w", err)}
	}
	return eventStreamTransportError{err: io.EOF}
}

func (store *Store) sessionToken(ctx context.Context, forceRefresh bool) (string, error) {
	store.authMu.Lock()
	defer store.authMu.Unlock()
	now := store.clock()
	if !forceRefresh && store.token != "" && store.expiresAt.After(now.Add(sessionRefreshSkew)) {
		return store.token, nil
	}
	if store.refreshToken == "" || !store.refreshExpiresAt.After(now) {
		return "", errCentralUnauthorized
	}
	var auth central.AuthExchangeResponse
	if err := store.doRequest(
		ctx,
		http.MethodPost,
		"/v1/auth/refresh",
		"",
		map[string]string{"Content-Type": "application/json"},
		central.AuthRefreshRequest{RefreshToken: store.refreshToken},
		&auth,
	); err != nil {
		return "", fmt.Errorf("refresh Central session: %w", err)
	}
	if err := store.acceptSessionLocked(auth, now); err != nil {
		return "", err
	}
	return store.token, nil
}

func (store *Store) acceptSession(auth central.AuthExchangeResponse) error {
	store.authMu.Lock()
	defer store.authMu.Unlock()
	return store.acceptSessionLocked(auth, store.clock())
}

func (store *Store) acceptSessionLocked(auth central.AuthExchangeResponse, now time.Time) error {
	if strings.TrimSpace(auth.AccessToken) == "" || strings.TrimSpace(auth.RefreshToken) == "" ||
		auth.User.ID != store.userID || !auth.ExpiresAt.After(now) ||
		!auth.RefreshExpiresAt.After(auth.ExpiresAt) {
		return errors.New("invalid Central client session")
	}
	store.token = auth.AccessToken
	store.expiresAt = auth.ExpiresAt
	store.refreshToken = auth.RefreshToken
	store.refreshExpiresAt = auth.RefreshExpiresAt
	return nil
}

func getValue[T any](ctx context.Context, store *Store, kind string, id string) (T, error) {
	var zero T
	envelope, err := store.getEnvelope(ctx, kind, id)
	if err != nil {
		return zero, err
	}
	if err := store.validateEmbeddedOwnership(envelope.Data); err != nil {
		return zero, err
	}
	var value T
	if err := json.Unmarshal(envelope.Data, &value); err != nil {
		return zero, fmt.Errorf("decode Central %s resource: %w", kind, err)
	}
	setObservedRevision(&value, envelope.Revision)
	return value, nil
}

func listValues[T any](ctx context.Context, store *Store, kind string) ([]T, error) {
	envelopes, err := store.listEnvelopes(ctx, kind)
	if err != nil {
		return nil, err
	}
	values := make([]T, len(envelopes))
	for index, envelope := range envelopes {
		if err := store.validateEmbeddedOwnership(envelope.Data); err != nil {
			return nil, err
		}
		if err := json.Unmarshal(envelope.Data, &values[index]); err != nil {
			return nil, fmt.Errorf("decode Central %s resource: %w", kind, err)
		}
		setObservedRevision(&values[index], envelope.Revision)
	}
	return values, nil
}

func observedRevision(value any) int64 {
	switch typed := value.(type) {
	case domain.Preset:
		return typed.Revision
	case domain.MonitorJob:
		return typed.Revision
	default:
		return 0
	}
}

func resourcePayload(value any) ([]byte, error) {
	switch typed := value.(type) {
	case domain.Preset:
		typed.Revision = 0
		return json.Marshal(typed)
	case domain.MonitorJob:
		typed.Revision = 0
		return json.Marshal(typed)
	default:
		return json.Marshal(value)
	}
}

func setObservedRevision(value any, revision int64) {
	switch typed := value.(type) {
	case *domain.Preset:
		typed.Revision = revision
	case *domain.MonitorJob:
		typed.Revision = revision
	}
}

func (store *Store) validateEmbeddedOwnership(payload []byte) error {
	var identity struct {
		UserID *string `json:"userId"`
	}
	if err := json.Unmarshal(payload, &identity); err != nil {
		return errors.New("central resource ownership is invalid")
	}
	if identity.UserID == nil {
		return nil
	}
	if strings.TrimSpace(*identity.UserID) == "" || strings.TrimSpace(*identity.UserID) != store.userID {
		// Treat an inconsistent embedded owner as absent. The authenticated
		// Central namespace is authoritative and a poisoned resource must never
		// be persisted or consumed under a different user.
		return application.ErrNotFound
	}
	return nil
}

func decodeAPIError(status int, contents []byte) error {
	var envelope apiErrorEnvelope
	if err := json.Unmarshal(contents, &envelope); err != nil || envelope.Error.Code == "" {
		return fmt.Errorf("central request failed with HTTP %d", status)
	}
	switch envelope.Error.Code {
	case "not_found":
		return application.ErrNotFound
	case "revision_conflict", "idempotency_conflict":
		return application.ErrConflict
	case "unauthorized":
		return fmt.Errorf("%w: %s", errCentralUnauthorized, envelope.Error.Message)
	case "rate_limited":
		return fmt.Errorf("%w: %s", ErrPINRateLimited, envelope.Error.Message)
	default:
		return fmt.Errorf("central %s: %s", envelope.Error.Code, envelope.Error.Message)
	}
}

func validateBaseURL(value string) (string, error) {
	parsed, err := url.Parse(strings.TrimSpace(value))
	if err != nil || parsed.Host == "" || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" {
		return "", errors.New("central URL is invalid")
	}
	if parsed.Scheme != "https" && (parsed.Scheme != "http" || !isLoopbackHost(parsed.Hostname())) {
		return "", errors.New("central URL must use HTTPS outside loopback development")
	}
	return strings.TrimRight(parsed.String(), "/"), nil
}

func isLoopbackHost(host string) bool {
	if strings.EqualFold(host, "localhost") {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

func resourcePath(kind string, id string) string {
	return "/v1/" + url.PathEscape(kind) + "/" + url.PathEscape(id)
}

func commandID(operation string, kind string, id string, revision int64, payload []byte) string {
	digest := sha256.Sum256(bytes.Join([][]byte{
		[]byte(operation), []byte(kind), []byte(id), []byte(strconv.FormatInt(revision, 10)), payload,
	}, []byte{0}))
	return "command_" + base64.RawURLEncoding.EncodeToString(digest[:])
}
