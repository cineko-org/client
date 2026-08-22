package centralhttp

import (
	"bufio"
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
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

	"buf.build/go/protovalidate"
	"github.com/cineko-org/client/internal/application"
	catalogpb "github.com/cineko-org/contracts/v3/gen/go/cineko/catalog"
	clientpb "github.com/cineko-org/contracts/v3/gen/go/cineko/client"
	commonpb "github.com/cineko-org/contracts/v3/gen/go/cineko/common"
	executionpb "github.com/cineko-org/contracts/v3/gen/go/cineko/execution"
	seatmappb "github.com/cineko-org/contracts/v3/gen/go/cineko/seatmap"
	servicepb "github.com/cineko-org/contracts/v3/gen/go/cineko/service"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
)

const (
	maximumResponseBody     = 8 << 20
	sessionRefreshSkew      = time.Minute
	releaseGenerationHeader = "X-Cineko-Release-Generation"
)

var errCentralUnauthorized = errors.New("central session is unauthorized")

var (
	ErrPINRateLimited        = errors.New("central PIN authentication is rate limited")
	ErrReleaseUpdateRequired = errors.New("central requires a different release generation")
)

type Config struct {
	BaseURL        string
	UserID         string
	AccessToken    string
	InstallationID string
	HTTPClient     *http.Client
}

type LaunchOptions struct {
	BaseURL    string
	HTTPClient *http.Client
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

	releaseGeneration atomic.Int64
	updateRequired    chan struct{}
	updateOnce        sync.Once
	eventCursor       atomic.Int64
	resyncRequired    chan struct{}
	resyncOnce        sync.Once
	resourceChanged   chan struct{}
	executionReady    chan struct{}
}

type centralAPIError struct {
	status    int
	code      string
	retryable bool
}

func mutationIdentity(commandID string, expectedRevision int64) *commonpb.MutationIdentity {
	return commonpb.MutationIdentity_builder{
		CommandId:        &commandID,
		ExpectedRevision: &expectedRevision,
	}.Build()
}

func resourceKind(kind string) (*clientpb.ResourceKind, error) {
	switch kind {
	case "settings":
		return clientpb.ResourceKind_builder{Settings: &clientpb.SettingsResource{}}.Build(), nil
	case "presets":
		return clientpb.ResourceKind_builder{Preset: &clientpb.PresetResource{}}.Build(), nil
	case "monitors":
		return clientpb.ResourceKind_builder{Monitor: &clientpb.MonitorResource{}}.Build(), nil
	case "reservations":
		return clientpb.ResourceKind_builder{Reservation: &clientpb.ReservationResource{}}.Build(), nil
	case "external-operations":
		return clientpb.ResourceKind_builder{ExternalOperation: &clientpb.ExternalOperationResource{}}.Build(), nil
	case "app-events":
		return clientpb.ResourceKind_builder{AppEvent: &clientpb.AppEventResource{}}.Build(), nil
	default:
		return nil, fmt.Errorf("unsupported Central resource kind %q", kind)
	}
}

func (failure centralAPIError) Error() string {
	if failure.code == "" {
		return fmt.Sprintf("central request failed with HTTP %d", failure.status)
	}
	return fmt.Sprintf("central request failed: %s", failure.code)
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
	credentials := &clientpb.TokenExchangeRequest{}
	credentials.SetUserId(config.UserID)
	credentials.SetAccessToken(config.AccessToken)
	request := servicepb.ExchangeTokenRequest_builder{Request: credentials}.Build()
	response := &servicepb.ExchangeTokenResponse{}
	if err := store.request(ctx, http.MethodPost, "/v1/auth/exchange", false, map[string]string{
		"Content-Type": "application/json",
	}, request, response); err != nil {
		return nil, fmt.Errorf("authenticate with Central: %w", err)
	}
	auth := response.GetAuthentication()
	if err := store.acceptSession(auth); err != nil {
		return nil, errors.New("central returned an invalid client session")
	}
	return store, nil
}

func OpenLaunched(ctx context.Context, envelope *clientpb.LaunchEnvelope, options LaunchOptions) (*Store, error) {
	if err := validateLaunchEnvelope(envelope); err != nil {
		return nil, err
	}
	store, err := newStore(options.BaseURL, "", options.HTTPClient)
	if err != nil {
		return nil, err
	}
	store.releaseGeneration.Store(envelope.GetContext().GetReleaseGeneration())
	clientNonce, err := launchClientNonce()
	if err != nil {
		return nil, err
	}
	launch := &clientpb.SessionExchangeRequest{}
	launch.SetLaunchTicket(envelope.GetLaunchTicket())
	launch.SetClientNonce(clientNonce)
	launchRequest := servicepb.ExchangeSessionRequest_builder{Request: launch}.Build()
	launchResponse := &servicepb.ExchangeSessionResponse{}
	if err := store.request(
		ctx,
		http.MethodPost,
		"/v1/client-sessions",
		false,
		map[string]string{"Content-Type": "application/json"},
		launchRequest, launchResponse,
	); err != nil {
		return nil, fmt.Errorf("exchange Central launch ticket: %w", err)
	}
	if store.releaseUpdateNeeded() {
		return nil, ErrReleaseUpdateRequired
	}
	auth := launchResponse.GetAuthentication()
	store.userID = strings.TrimSpace(auth.GetUser().GetId())
	if store.userID == "" || !proto.Equal(auth.GetLaunch(), envelope.GetContext()) || store.acceptSession(auth) != nil {
		return nil, errors.New("central returned an invalid launched client session")
	}
	return store, nil
}

func validateLaunchEnvelope(envelope *clientpb.LaunchEnvelope) error {
	if envelope == nil {
		return errors.New("launched client identity is incomplete")
	}
	if err := protovalidate.Validate(envelope); err != nil {
		return fmt.Errorf("validate launched client identity: %w", err)
	}
	launchContext := envelope.GetContext()
	required := []string{
		envelope.GetLaunchTicket(), launchContext.GetInstallationId(), launchContext.GetDeviceId(),
		launchContext.GetClientVersion(), launchContext.GetBrowserRevision(), launchContext.GetPlaywrightVersion(),
	}
	for _, value := range required {
		if strings.TrimSpace(value) == "" {
			return errors.New("launched client identity is incomplete")
		}
	}
	if launchContext.GetReleaseGeneration() < 1 {
		return errors.New("launched client identity is incomplete")
	}
	for _, digest := range []string{
		launchContext.GetArtifactSha256(), launchContext.GetBrowserArtifactSha256(), launchContext.GetPlaywrightArtifactSha256(),
	} {
		if !validSHA256(digest) {
			return errors.New("launched client identity is incomplete")
		}
	}
	return nil
}

func launchClientNonce() (string, error) {
	value := make([]byte, 24)
	if _, err := rand.Read(value); err != nil {
		return "", fmt.Errorf("generate launch client nonce: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(value), nil
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
		clock: time.Now, updateRequired: make(chan struct{}),
		resyncRequired:  make(chan struct{}),
		resourceChanged: make(chan struct{}, 1), executionReady: make(chan struct{}, 1),
	}, nil
}

func (store *Store) UserID() string { return store.userID }

func (store *Store) ReleaseGeneration() int64 { return store.releaseGeneration.Load() }

func (store *Store) UpdateRequired() <-chan struct{} { return store.updateRequired }

func (store *Store) ResyncRequired() <-chan struct{} { return store.resyncRequired }

func (store *Store) ResourceChanged() <-chan struct{} { return store.resourceChanged }

func (store *Store) ExecutionReady() <-chan struct{} { return store.executionReady }

func (store *Store) releaseUpdateNeeded() bool {
	select {
	case <-store.updateRequired:
		return true
	default:
		return false
	}
}

func (store *Store) Close() error { return nil }

func (store *Store) GetSettings(ctx context.Context, output *clientpb.Settings) (int64, error) {
	kind, err := resourceKind("settings")
	if err != nil {
		return 0, err
	}
	id := "settings"
	request := servicepb.GetResourceRequest_builder{Kind: kind, Id: &id}.Build()
	response := &servicepb.GetResourceResponse{}
	if err := store.request(ctx, http.MethodGet, "/v1/settings", true,
		map[string]string{"Content-Type": "application/json"}, request, response); err != nil {
		return 0, err
	}
	resource := response.GetResource()
	if resource == nil || resource.GetSettings() == nil || resource.GetIdentity() == nil {
		return 0, errors.New("central returned an invalid settings resource")
	}
	if output != nil {
		proto.Reset(output)
		proto.Merge(output, resource.GetSettings())
	}
	return resource.GetIdentity().GetRevision(), nil
}

func (store *Store) PutSettings(ctx context.Context, input *clientpb.Settings, expectedRevision int64) error {
	if expectedRevision < 0 {
		return errors.New("settings revision cannot be negative")
	}
	if input == nil {
		return errors.New("settings are required")
	}
	resource, err := resourceFor("settings", "settings", expectedRevision, input)
	if err != nil {
		return fmt.Errorf("encode Central settings: %w", err)
	}
	payload, err := protojson.MarshalOptions{UseProtoNames: false}.Marshal(resource)
	if err != nil {
		return fmt.Errorf("encode Central settings: %w", err)
	}
	headers := map[string]string{"Content-Type": "application/json"}
	if expectedRevision > 0 {
		headers["If-Match"] = strconv.FormatInt(expectedRevision, 10)
	} else {
		headers["If-None-Match"] = "*"
	}
	command := commandID("put", "settings", "settings", expectedRevision, payload)
	headers["Idempotency-Key"] = command
	request := servicepb.PutResourceRequest_builder{
		Mutation: mutationIdentity(command, expectedRevision),
		Resource: resource,
	}.Build()
	response := &servicepb.PutResourceResponse{}
	if err := store.request(ctx, http.MethodPut, "/v1/settings", true, headers, request, response); err != nil {
		return err
	}
	if response.GetResource() == nil || response.GetResource().GetSettings() == nil || response.GetResource().GetIdentity() == nil {
		return errors.New("central returned an invalid settings mutation response")
	}
	return nil
}

func (store *Store) Logout(ctx context.Context) error {
	return store.request(ctx, http.MethodPost, "/v1/auth/logout", true,
		map[string]string{"Content-Type": "application/json"},
		&servicepb.LogoutRequest{}, &servicepb.LogoutResponse{})
}

func (store *Store) RegisterDevice(
	ctx context.Context,
	device *clientpb.Device,
) (*clientpb.Device, error) {
	request := servicepb.UpsertDeviceRequest_builder{Device: device}.Build()
	response := &servicepb.UpsertDeviceResponse{}
	if err := store.request(
		ctx, http.MethodPut, "/v1/devices/"+url.PathEscape(device.GetInstallationId()), true,
		map[string]string{"Content-Type": "application/json"}, request, response,
	); err != nil {
		return nil, err
	}
	if response.GetDevice() == nil {
		return nil, errors.New("central returned an invalid device response")
	}
	return response.GetDevice(), nil
}

func (store *Store) IssueProbeBootstrapTicket(
	ctx context.Context,
	request *clientpb.ProbeBootstrapTicketRequest,
) (*clientpb.ProbeBootstrapTicketResponse, error) {
	serviceRequest := servicepb.CreateProbeBootstrapTicketRequest_builder{Request: request}.Build()
	response := &servicepb.CreateProbeBootstrapTicketResponse{}
	if err := store.request(
		ctx,
		http.MethodPost,
		"/v1/probe-bootstrap-tickets",
		true,
		map[string]string{"Content-Type": "application/json"},
		serviceRequest,
		response,
	); err != nil {
		return nil, err
	}
	if response.GetResponse() == nil {
		return nil, errors.New("central returned an invalid probe bootstrap response")
	}
	return response.GetResponse(), nil
}

func (store *Store) ClaimExecution(
	ctx context.Context,
	installationID string,
) (*executionpb.Command, error) {
	response := &executionpb.ClaimResponse{}
	request := executionpb.ClaimRequest_builder{InstallationId: &installationID}.Build()
	if err := store.request(
		ctx, http.MethodPost, "/v1/executions:claim", true,
		map[string]string{"Content-Type": "application/json"},
		request, response,
	); err != nil {
		return nil, err
	}
	return response.GetCommand(), nil
}

func (*Store) ExecutionClaimRetryable(err error) bool {
	if err == nil || errors.Is(err, errCentralUnauthorized) {
		return false
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return true
	}
	var apiFailure centralAPIError
	if errors.As(err, &apiFailure) {
		return apiFailure.retryable || apiFailure.status >= http.StatusInternalServerError
	}
	var networkFailure net.Error
	return errors.As(err, &networkFailure)
}

func (store *Store) HeartbeatExecution(
	ctx context.Context,
	commandID string,
	leaseToken string,
) (*executionpb.HeartbeatResponse, error) {
	response := &executionpb.HeartbeatResponse{}
	request := executionpb.HeartbeatRequest_builder{CommandId: &commandID, LeaseToken: &leaseToken}.Build()
	if err := store.request(
		ctx, http.MethodPut, "/v1/executions/"+url.PathEscape(commandID)+"/heartbeat", true,
		map[string]string{"Content-Type": "application/json"},
		request, response,
	); err != nil {
		return nil, err
	}
	return response, nil
}

func (store *Store) CompleteExecution(
	ctx context.Context,
	commandID string,
	request *executionpb.ResultRequest,
) error {
	if request == nil {
		return errors.New("execution result request is required")
	}
	request = proto.CloneOf(request)
	request.SetCommandId(commandID)
	serviceRequest := servicepb.CompleteRequest_builder{Result: request}.Build()
	return store.request(
		ctx, http.MethodPut, "/v1/executions/"+url.PathEscape(commandID)+"/result", true,
		map[string]string{"Content-Type": "application/json"}, serviceRequest, &servicepb.CompleteResponse{},
	)
}

func (store *Store) GetTheater(ctx context.Context, id string) (*catalogpb.Theater, error) {
	catalog, err := store.GetCatalog(ctx)
	if err != nil {
		return nil, err
	}
	for _, value := range catalog.GetTheaters() {
		if value.GetId() == id {
			return value, nil
		}
	}
	return nil, application.ErrNotFound
}

func (store *Store) ListTheaters(ctx context.Context) ([]*catalogpb.Theater, error) {
	catalog, err := store.GetCatalog(ctx)
	if err != nil {
		return nil, err
	}
	values := append([]*catalogpb.Theater(nil), catalog.GetTheaters()...)
	return values, nil
}

func (store *Store) GetAuditorium(ctx context.Context, id string) (*catalogpb.Auditorium, error) {
	catalog, err := store.GetCatalog(ctx)
	if err != nil {
		return nil, err
	}
	for _, value := range catalog.GetAuditoriums() {
		if value.GetId() == id {
			return value, nil
		}
	}
	return nil, application.ErrNotFound
}

func (store *Store) ListAuditoriumsByTheater(ctx context.Context, theaterID string) ([]*catalogpb.Auditorium, error) {
	catalog, err := store.GetCatalog(ctx)
	if err != nil {
		return nil, err
	}
	values := make([]*catalogpb.Auditorium, 0)
	for _, value := range catalog.GetAuditoriums() {
		if value.GetTheaterId() == theaterID {
			values = append(values, value)
		}
	}
	return values, nil
}

func (store *Store) GetSeatMap(ctx context.Context, auditoriumID string) (*seatmappb.Snapshot, error) {
	resolution, err := store.ResolveSeatMap(ctx, auditoriumID)
	if err != nil {
		return nil, err
	}
	if snapshot := resolution.GetSnapshot(); snapshot != nil {
		return snapshot, nil
	}
	return nil, application.ErrNotFound
}

// ResolveSeatMap asks Central for its current layout without exposing whether
// Central used stored data or scheduled a Probe capture.
func (store *Store) ResolveSeatMap(ctx context.Context, auditoriumID string) (*seatmappb.Resolution, error) {
	request := servicepb.ResolveSeatMapRequest_builder{AuditoriumId: &auditoriumID}.Build()
	response := &servicepb.ResolveSeatMapResponse{}
	if err := store.request(ctx, http.MethodPost,
		"/v1/catalog/auditoriums/"+url.PathEscape(auditoriumID)+"/seat-map:resolve", true,
		map[string]string{"Content-Type": "application/json"}, request, response); err != nil {
		return nil, err
	}
	resolution := response.GetResolution()
	if resolution == nil || resolution.GetState() == nil {
		return nil, errors.New("central returned an invalid seat-map resolution")
	}
	return resolution, nil
}

// SubmitLiveSeatObservation reports one atomic authenticated provider read.
// Central requires the generated mutation command ID as the HTTP idempotency
// key and owns normalization, deduplication, and durable history.
func (store *Store) SubmitLiveSeatObservation(
	ctx context.Context,
	input *servicepb.SubmitLiveSeatObservationRequest,
) (*servicepb.SubmitLiveSeatObservationResponse, error) {
	commandID := input.GetMutation().GetCommandId()
	response := &servicepb.SubmitLiveSeatObservationResponse{}
	if err := store.request(
		ctx,
		http.MethodPost,
		"/v1/catalog/live-seat-observations",
		true,
		map[string]string{"Content-Type": "application/json", "Idempotency-Key": commandID},
		input,
		response,
	); err != nil {
		return nil, err
	}
	return response, nil
}

func (store *Store) PutPreset(ctx context.Context, resource *clientpb.Resource) error {
	return store.put(ctx, "presets", resource)
}

func (store *Store) GetPreset(ctx context.Context, id string) (*clientpb.Resource, error) {
	return store.getResource(ctx, "presets", id)
}

func (store *Store) ListPresetsByUser(ctx context.Context, userID string) ([]*clientpb.Resource, error) {
	if err := store.owns(userID); err != nil {
		return nil, err
	}
	return store.listResources(ctx, "presets")
}

func (store *Store) DeletePreset(ctx context.Context, id string) error {
	return store.delete(ctx, "presets", id)
}

func (store *Store) PutMonitor(ctx context.Context, resource *clientpb.Resource) error {
	return store.put(ctx, "monitors", resource)
}

func (store *Store) GetMonitor(ctx context.Context, id string) (*clientpb.Resource, error) {
	return store.getResource(ctx, "monitors", id)
}

func (store *Store) ListMonitorsByUser(ctx context.Context, userID string) ([]*clientpb.Resource, error) {
	if err := store.owns(userID); err != nil {
		return nil, err
	}
	return store.listResources(ctx, "monitors")
}

func (store *Store) DeleteMonitor(ctx context.Context, id string) error {
	return store.delete(ctx, "monitors", id)
}

func (store *Store) PutReservation(ctx context.Context, resource *clientpb.Resource) error {
	return store.put(ctx, "reservations", resource)
}

func (store *Store) GetReservation(ctx context.Context, id string) (*clientpb.Resource, error) {
	return store.getResource(ctx, "reservations", id)
}

func (store *Store) ListReservationsByUser(ctx context.Context, userID string) ([]*clientpb.Resource, error) {
	if err := store.owns(userID); err != nil {
		return nil, err
	}
	return store.listResources(ctx, "reservations")
}

func (store *Store) PutExternalOperation(ctx context.Context, resource *clientpb.Resource) error {
	return store.put(ctx, "external-operations", resource)
}

func (store *Store) GetCatalog(ctx context.Context) (*catalogpb.CatalogIndex, error) {
	response := &servicepb.GetCatalogResponse{}
	if err := store.request(ctx, http.MethodGet, "/v1/catalog", true,
		map[string]string{"Content-Type": "application/json"}, &servicepb.GetCatalogRequest{}, response); err != nil {
		return nil, err
	}
	if response.GetCatalog() == nil {
		return nil, errors.New("central returned an invalid catalog response")
	}
	return response.GetCatalog(), nil
}

func (store *Store) PutAppEvent(ctx context.Context, resource *clientpb.Resource) error {
	return store.put(ctx, "app-events", resource)
}

func (store *Store) ListAppEvents(ctx context.Context, userID string, limit int) ([]*clientpb.Resource, error) {
	if err := store.owns(userID); err != nil {
		return nil, err
	}
	values, err := store.listResources(ctx, "app-events")
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
		if value.GetAppEvent() != nil && value.GetAppEvent().GetReadAt() == nil {
			value.GetAppEvent().SetReadAt(timestamp(at))
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
		if err := store.delete(ctx, "app-events", value.GetIdentity().GetId()); err != nil {
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
		createdAt := value.GetIdentity().GetCreatedAt()
		if createdAt != nil && createdAt.AsTime().Before(cutoff) {
			if err := store.delete(ctx, "app-events", value.GetIdentity().GetId()); err != nil {
				return err
			}
		}
	}
	return nil
}

func (store *Store) RecoverInterruptedWork(context.Context, time.Time) ([]*clientpb.Resource, error) {
	return []*clientpb.Resource{}, nil
}

func (store *Store) owns(userID string) error {
	if userID != store.userID {
		return application.ErrNotFound
	}
	return nil
}

func (store *Store) put(ctx context.Context, kind string, resource *clientpb.Resource) error {
	if resource == nil || resource.GetIdentity() == nil {
		return fmt.Errorf("encode Central %s resource: resource and identity are required", kind)
	}
	id := resource.GetIdentity().GetId()
	if id == "" {
		return fmt.Errorf("encode Central %s resource: resource ID is required", kind)
	}
	if err := validateResourceKind(kind, resource); err != nil {
		return err
	}
	if owner := resourceOwner(resource); owner != "" {
		if err := store.owns(owner); err != nil {
			return err
		}
	}
	destination := resource
	revision := resource.GetIdentity().GetRevision()
	resource = proto.CloneOf(resource)
	payload, err := protojson.MarshalOptions{UseProtoNames: false}.Marshal(resource)
	if err != nil {
		return fmt.Errorf("encode Central %s resource: %w", kind, err)
	}
	headers := map[string]string{"Content-Type": "application/json"}
	method, path := http.MethodPut, resourcePath(kind, id)
	if revision == 0 {
		method, path = http.MethodPost, "/v1/"+url.PathEscape(kind)
		headers["If-None-Match"] = "*"
	} else {
		headers["If-Match"] = strconv.FormatInt(revision, 10)
	}
	command := commandID("put", kind, id, revision, payload)
	headers["Idempotency-Key"] = command
	request := servicepb.PutResourceRequest_builder{
		Mutation: mutationIdentity(command, revision),
		Resource: resource,
	}.Build()
	response := &servicepb.PutResourceResponse{}
	if err := store.request(ctx, method, path, true, headers, request, response); err != nil {
		return err
	}
	if response.GetResource() == nil || response.GetResource().GetIdentity() == nil {
		return errors.New("central returned an invalid resource mutation response")
	}
	proto.Reset(destination)
	proto.Merge(destination, response.GetResource())
	return nil
}

func (store *Store) delete(ctx context.Context, kind string, id string) error {
	current, err := store.getResource(ctx, kind, id)
	if errors.Is(err, application.ErrNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	identity := current.GetIdentity()
	command := commandID("delete", kind, id, identity.GetRevision(), nil)
	resourceType, err := resourceKind(kind)
	if err != nil {
		return err
	}
	request := servicepb.DeleteResourceRequest_builder{
		Mutation: mutationIdentity(command, identity.GetRevision()),
		Kind:     resourceType,
		Id:       &id,
	}.Build()
	return store.request(ctx, http.MethodDelete, resourcePath(kind, id), true, map[string]string{
		"Content-Type":    "application/json",
		"Idempotency-Key": command,
		"If-Match":        strconv.FormatInt(identity.GetRevision(), 10),
	}, request, &servicepb.DeleteResourceResponse{})
}

func (store *Store) getResource(ctx context.Context, kind string, id string) (*clientpb.Resource, error) {
	resourceType, err := resourceKind(kind)
	if err != nil {
		return nil, err
	}
	request := servicepb.GetResourceRequest_builder{Kind: resourceType, Id: &id}.Build()
	response := &servicepb.GetResourceResponse{}
	if err := store.request(ctx, http.MethodGet, resourcePath(kind, id), true,
		map[string]string{"Content-Type": "application/json"}, request, response); err != nil {
		return nil, err
	}
	if response.GetResource() == nil {
		return nil, errors.New("central returned an invalid resource response")
	}
	return response.GetResource(), nil
}

func (store *Store) listResources(ctx context.Context, kind string) ([]*clientpb.Resource, error) {
	resourceType, err := resourceKind(kind)
	if err != nil {
		return nil, err
	}
	request := servicepb.ListResourcesRequest_builder{Kind: resourceType}.Build()
	response := &servicepb.ListResourcesResponse{}
	if err := store.request(ctx, http.MethodGet, "/v1/"+url.PathEscape(kind), true,
		map[string]string{"Content-Type": "application/json"}, request, response); err != nil {
		return nil, err
	}
	return response.GetResources(), nil
}

func (store *Store) request(
	ctx context.Context,
	method string,
	path string,
	authenticated bool,
	headers map[string]string,
	input proto.Message,
	output proto.Message,
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
	input proto.Message,
	output proto.Message,
) error {
	body, err := encodeRequestBody(input)
	if err != nil {
		return err
	}
	endpoint := store.baseURL + path
	request, err := http.NewRequestWithContext(ctx, method, endpoint, body)
	if err != nil {
		return fmt.Errorf("create Central request: %w", err)
	}
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
	if err := store.observeReleaseGeneration(response.Header.Get(releaseGenerationHeader)); err != nil {
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
	if output == nil {
		return nil
	}
	if len(contents) == 0 {
		return errors.New("central returned an empty protobuf response")
	}
	if err := (protojson.UnmarshalOptions{DiscardUnknown: false}).Unmarshal(contents, output); err != nil {
		return fmt.Errorf("decode Central response: %w", err)
	}
	if err := protovalidate.Validate(output); err != nil {
		return fmt.Errorf("validate Central response: %w", err)
	}
	return nil
}

func encodeRequestBody(input proto.Message) (io.Reader, error) {
	if input == nil {
		return nil, nil
	}
	if err := protovalidate.Validate(input); err != nil {
		return nil, fmt.Errorf("validate Central request: %w", err)
	}
	encoded, err := (protojson.MarshalOptions{UseProtoNames: false}).Marshal(input)
	if err != nil {
		return nil, fmt.Errorf("encode Central request: %w", err)
	}
	return bytes.NewReader(encoded), nil
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
// carried by the response header and generated control messages, so this does
// not add a polling loop or a second update-only connection.
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
	afterSequence := store.eventCursor.Load()
	input := servicepb.StreamEventsRequest_builder{AfterSequence: &afterSequence}.Build()
	body, err := encodeRequestBody(input)
	if err != nil {
		return err
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, store.baseURL+"/v1/events/stream", body)
	if err != nil {
		return fmt.Errorf("create Central event stream request: %w", err)
	}
	request.Header.Set("Authorization", "Bearer "+token)
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Accept", "text/event-stream")
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
	if err := store.observeReleaseGeneration(response.Header.Get(releaseGenerationHeader)); err != nil {
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
	credentials := &clientpb.TokenRefreshRequest{}
	credentials.SetRefreshToken(store.refreshToken)
	request := servicepb.RefreshTokenRequest_builder{Request: credentials}.Build()
	response := &servicepb.RefreshTokenResponse{}
	if err := store.doRequest(
		ctx,
		http.MethodPost,
		"/v1/auth/refresh",
		"",
		map[string]string{"Content-Type": "application/json"},
		request, response,
	); err != nil {
		return "", fmt.Errorf("refresh Central session: %w", err)
	}
	if err := store.acceptSessionLocked(response.GetAuthentication(), now); err != nil {
		return "", err
	}
	return store.token, nil
}

func (store *Store) acceptSession(auth *clientpb.AuthenticationResponse) error {
	store.authMu.Lock()
	defer store.authMu.Unlock()
	return store.acceptSessionLocked(auth, store.clock())
}

func (store *Store) acceptSessionLocked(auth *clientpb.AuthenticationResponse, now time.Time) error {
	if auth == nil || strings.TrimSpace(auth.GetAccessToken()) == "" || strings.TrimSpace(auth.GetRefreshToken()) == "" ||
		auth.GetUser().GetId() != store.userID || auth.GetExpiresAt() == nil || auth.GetRefreshExpiresAt() == nil ||
		!auth.GetExpiresAt().AsTime().After(now) || !auth.GetRefreshExpiresAt().AsTime().After(auth.GetExpiresAt().AsTime()) {
		return errors.New("invalid Central client session")
	}
	store.token = auth.GetAccessToken()
	store.expiresAt = auth.GetExpiresAt().AsTime()
	store.refreshToken = auth.GetRefreshToken()
	store.refreshExpiresAt = auth.GetRefreshExpiresAt().AsTime()
	return nil
}

func decodeAPIError(status int, contents []byte) error {
	response := &commonpb.APIErrorResponse{}
	if err := (protojson.UnmarshalOptions{DiscardUnknown: false}).Unmarshal(contents, response); err != nil {
		return centralAPIError{status: status, retryable: status >= http.StatusInternalServerError}
	}
	if err := protovalidate.Validate(response); err != nil || response.GetError() == nil || response.GetError().GetCode() == "" {
		return centralAPIError{status: status, retryable: status >= http.StatusInternalServerError}
	}
	failure := response.GetError()
	switch failure.GetCode() {
	case "not_found":
		return application.ErrNotFound
	case "revision_conflict", "idempotency_conflict":
		return application.ErrConflict
	case "unauthorized":
		return fmt.Errorf("%w: %s", errCentralUnauthorized, failure.GetMessage())
	case "rate_limited":
		return fmt.Errorf("%w: %s", ErrPINRateLimited, failure.GetMessage())
	default:
		return centralAPIError{status: status, code: failure.GetCode(), retryable: failure.GetRetryable()}
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
