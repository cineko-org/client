package centralhttp

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	clientpb "github.com/cineko-org/contracts/gen/go/cineko/client"
	commonpb "github.com/cineko-org/contracts/gen/go/cineko/common"
	servicepb "github.com/cineko-org/contracts/gen/go/cineko/service"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"
)

func TestStoreObservesReleaseGenerationOnOrdinaryResponses(t *testing.T) {
	now := time.Now().UTC()
	var generation atomic.Int64
	generation.Store(17)
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set(releaseGenerationHeader, fmt.Sprintf("%d", generation.Load()))
		switch request.URL.Path {
		case "/v1/auth/exchange":
			writeSession(t, writer, "access", "refresh", now)
		case "/v1/devices/install":
			input := &servicepb.UpsertDeviceRequest{}
			decodeRequest(t, request, input)
			writeProto(t, writer, servicepb.UpsertDeviceResponse_builder{Device: input.GetDevice()}.Build())
		default:
			http.NotFound(writer, request)
		}
	}))
	t.Cleanup(server.Close)
	store, err := Open(t.Context(), Config{
		BaseURL: server.URL, UserID: "user", AccessToken: "credential", HTTPClient: server.Client(),
	})
	if err != nil || store.ReleaseGeneration() != 17 {
		t.Fatalf("Open() generation = %d, %v", store.ReleaseGeneration(), err)
	}
	generation.Store(18)
	if _, err := store.RegisterDevice(t.Context(), clientpb.Device_builder{InstallationId: stringPointer("install")}.Build()); err != nil {
		t.Fatal(err)
	}
	select {
	case <-store.UpdateRequired():
	default:
		t.Fatal("release generation change did not request an update")
	}
}

func TestStoreObservesReleaseGenerationOnEventStreamHeartbeat(t *testing.T) {
	now := time.Now().UTC()
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set(releaseGenerationHeader, "17")
		switch request.URL.Path {
		case "/v1/auth/exchange":
			writeSession(t, writer, "access", "refresh", now)
		case "/v1/events/stream":
			input := &servicepb.StreamEventsRequest{}
			decodeRequest(t, request, input)
			if !input.HasAfterSequence() || input.GetAfterSequence() != 0 {
				t.Errorf("stream request = %s", protoJSON(t, input))
			}
			writer.Header().Set("Content-Type", "text/event-stream")
			_, _ = fmt.Fprintf(writer, "event: cineko.control\ndata: %s\n\n", protoJSON(t, servicepb.StreamEventsResponse_builder{Control: clientpb.StreamControl_builder{
				ReleaseGeneration: int64Pointer(18),
				Heartbeat:         clientpb.StreamHeartbeat_builder{Cursor: int64Pointer(0)}.Build(),
			}.Build()}.Build()))
		default:
			http.NotFound(writer, request)
		}
	}))
	t.Cleanup(server.Close)
	store, err := Open(t.Context(), Config{
		BaseURL: server.URL, UserID: "user", AccessToken: "credential", HTTPClient: server.Client(),
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), time.Second)
	defer cancel()
	if err := store.WatchEvents(ctx); err != nil {
		t.Fatal(err)
	}
	select {
	case <-store.UpdateRequired():
	default:
		t.Fatal("event stream heartbeat did not request an update")
	}
}

func TestStoreRejectsMalformedReleaseGeneration(t *testing.T) {
	store, err := newStore("http://localhost", "user", nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.observeReleaseGeneration("broken"); err == nil {
		t.Fatal("malformed release generation accepted")
	}
	if err := store.observeReleaseGeneration("0"); err == nil {
		t.Fatal("zero release generation accepted")
	}
	if err := store.observeReleaseGeneration(""); err == nil {
		t.Fatal("missing release generation accepted")
	}
}

func TestStoreRejectsCentralResponseWithoutReleaseGeneration(t *testing.T) {
	now := time.Now().UTC()
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writeSession(t, writer, "access", "refresh", now)
	}))
	t.Cleanup(server.Close)
	if _, err := Open(t.Context(), Config{
		BaseURL: server.URL, UserID: "user", AccessToken: "credential", HTTPClient: server.Client(),
	}); err == nil {
		t.Fatal("Central response without a release generation accepted")
	}
}

func TestEventStreamRefreshesUnauthorizedSessionOnce(t *testing.T) {
	now := time.Now().UTC()
	var refreshes atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set(releaseGenerationHeader, "17")
		switch request.URL.Path {
		case "/v1/auth/exchange":
			writeSession(t, writer, "access-1", "refresh-1", now)
		case "/v1/auth/refresh":
			refreshes.Add(1)
			writeRefreshSession(t, writer, "access-2", "refresh-2", now)
		case "/v1/events/stream":
			input := &servicepb.StreamEventsRequest{}
			decodeRequest(t, request, input)
			if !input.HasAfterSequence() || input.GetAfterSequence() != 0 {
				t.Errorf("stream request = %s", protoJSON(t, input))
			}
			if request.Header.Get("Authorization") == "Bearer access-1" {
				writer.WriteHeader(http.StatusUnauthorized)
				writeProto(t, writer, commonpb.APIErrorResponse_builder{Error: commonpb.APIError_builder{
					Code: stringPointer("unauthorized"), Message: stringPointer("expired"), RequestId: stringPointer("test-request"),
				}.Build()}.Build())
				return
			}
			writer.Header().Set("Content-Type", "text/event-stream")
			_, _ = fmt.Fprintf(writer, "event: cineko.control\ndata: %s\n\n", protoJSON(t, servicepb.StreamEventsResponse_builder{Control: clientpb.StreamControl_builder{
				ReleaseGeneration: int64Pointer(18),
				Heartbeat:         clientpb.StreamHeartbeat_builder{Cursor: int64Pointer(0)}.Build(),
			}.Build()}.Build()))
		default:
			http.NotFound(writer, request)
		}
	}))
	t.Cleanup(server.Close)
	store, err := Open(t.Context(), Config{
		BaseURL: server.URL, UserID: "user", AccessToken: "credential", HTTPClient: server.Client(),
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.WatchEvents(t.Context()); err != nil {
		t.Fatal(err)
	}
	if refreshes.Load() != 1 {
		t.Fatalf("event stream refreshes = %d", refreshes.Load())
	}
}

func TestEventStreamRejectsMissingReleaseGenerationHeader(t *testing.T) {
	now := time.Now().UTC()
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		switch request.URL.Path {
		case "/v1/auth/exchange":
			writer.Header().Set(releaseGenerationHeader, "17")
			writeSession(t, writer, "access", "refresh", now)
		case "/v1/events/stream":
			writer.Header().Set("Content-Type", "text/event-stream")
			_, _ = writer.Write([]byte(": heartbeat\n\n"))
		default:
			http.NotFound(writer, request)
		}
	}))
	t.Cleanup(server.Close)
	store, err := Open(t.Context(), Config{
		BaseURL: server.URL, UserID: "user", AccessToken: "credential", HTTPClient: server.Client(),
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.watchEventsOnce(t.Context()); err == nil {
		t.Fatal("event stream without a release generation accepted")
	}
}

func TestLaunchedStoreBindsCentralSessionToReleaseGeneration(t *testing.T) {
	now := time.Now().UTC()
	launchContext := clientpb.LaunchContext_builder{
		InstallationId:           stringPointer("install"),
		DeviceId:                 stringPointer("device"),
		ReleaseGeneration:        int64Pointer(17),
		ClientVersion:            stringPointer("2.3.4"),
		ArtifactSha256:           stringPointer("0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"),
		BrowserRevision:          stringPointer("1228"),
		BrowserArtifactSha256:    stringPointer("1123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"),
		PlaywrightVersion:        stringPointer("1.60.0"),
		PlaywrightArtifactSha256: stringPointer("2123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"),
	}.Build()
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set(releaseGenerationHeader, "17")
		if request.URL.Path != "/v1/client-sessions" {
			http.NotFound(writer, request)
			return
		}
		input := &servicepb.ExchangeSessionRequest{}
		decodeRequest(t, request, input)
		if input.GetRequest().GetLaunchTicket() != "ticket" || len(input.GetRequest().GetClientNonce()) < 16 {
			t.Errorf("session exchange = %s", protoJSON(t, input))
		}
		writeProto(t, writer, servicepb.ExchangeSessionResponse_builder{Authentication: clientpb.AuthenticationResponse_builder{
			AccessToken:      stringPointer("access"),
			ExpiresAt:        timestamppb.New(now.Add(time.Hour)),
			RefreshToken:     stringPointer("refresh"),
			RefreshExpiresAt: timestamppb.New(now.Add(24 * time.Hour)),
			User:             clientpb.User_builder{Id: stringPointer("user")}.Build(),
			Launch:           launchContext,
		}.Build()}.Build())
	}))
	t.Cleanup(server.Close)
	envelope := clientpb.LaunchEnvelope_builder{LaunchTicket: stringPointer("ticket"), Context: launchContext}.Build()
	options := LaunchOptions{BaseURL: server.URL, HTTPClient: server.Client()}
	store, err := OpenLaunched(t.Context(), envelope, options)
	if err != nil || store.ReleaseGeneration() != 17 {
		t.Fatalf("OpenLaunched() = %+v, %v", store, err)
	}
	for name, mutate := range map[string]func(*clientpb.LaunchContext){
		"browser artifact": func(value *clientpb.LaunchContext) {
			value.SetBrowserArtifactSha256("3123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef")
		},
		"playwright version": func(value *clientpb.LaunchContext) { value.SetPlaywrightVersion("1.61.0") },
		"playwright artifact": func(value *clientpb.LaunchContext) {
			value.SetPlaywrightArtifactSha256("4123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef")
		},
	} {
		t.Run(name+" mismatch", func(t *testing.T) {
			mismatched := cloneLaunchEnvelopeForTest(envelope)
			mutate(mismatched.GetContext())
			if _, err := OpenLaunched(t.Context(), mismatched, options); err == nil {
				t.Fatal("mismatched launch context accepted")
			}
		})
	}
	invalidDigest := cloneLaunchEnvelopeForTest(envelope)
	invalidDigest.GetContext().SetBrowserArtifactSha256("not-a-digest")
	if _, err := OpenLaunched(t.Context(), invalidDigest, options); err == nil {
		t.Fatal("invalid browser artifact digest accepted")
	}
	updateRequired := cloneLaunchEnvelopeForTest(envelope)
	updateRequired.GetContext().SetReleaseGeneration(18)
	if _, err := OpenLaunched(t.Context(), updateRequired, options); !errors.Is(err, ErrReleaseUpdateRequired) {
		t.Fatalf("mismatched launch generation error = %v", err)
	}
}

func cloneLaunchEnvelopeForTest(value *clientpb.LaunchEnvelope) *clientpb.LaunchEnvelope {
	cloned, ok := proto.Clone(value).(*clientpb.LaunchEnvelope)
	if !ok {
		panic("cloned launch envelope has an unexpected Proto type")
	}
	return cloned
}

func TestStoreReadsAndWritesCentralSettings(t *testing.T) {
	now := time.Now().UTC()
	revision := int64(4)
	settings := clientpb.Settings_builder{Network: clientpb.NetworkSettings_builder{Direct: clientpb.DirectNetwork_builder{}.Build()}.Build()}.Build()
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set(releaseGenerationHeader, "17")
		switch {
		case request.URL.Path == "/v1/auth/exchange":
			writeSession(t, writer, "access", "refresh", now)
		case request.URL.Path == "/v1/settings" && request.Method == http.MethodGet:
			input := &servicepb.GetResourceRequest{}
			decodeRequest(t, request, input)
			if input.GetId() != "settings" || input.GetKind().GetSettings() == nil {
				t.Errorf("get settings request = %s", protoJSON(t, input))
			}
			writeProto(t, writer, servicepb.GetResourceResponse_builder{Resource: settingsResource(settings, revision)}.Build())
		case request.URL.Path == "/v1/settings" && request.Method == http.MethodPut:
			if request.Header.Get("If-Match") != "4" || request.Header.Get("If-None-Match") != "" || request.Header.Get("Idempotency-Key") == "" {
				t.Errorf("settings mutation headers = %v", request.Header)
			}
			input := &servicepb.PutResourceRequest{}
			decodeRequest(t, request, input)
			if input.GetMutation().GetExpectedRevision() != 4 || input.GetMutation().GetCommandId() != request.Header.Get("Idempotency-Key") {
				t.Errorf("settings mutation = %s", protoJSON(t, input.GetMutation()))
			}
			settings = input.GetResource().GetSettings()
			revision++
			writeProto(t, writer, servicepb.PutResourceResponse_builder{Resource: settingsResource(settings, revision)}.Build())
		default:
			http.NotFound(writer, request)
		}
	}))
	t.Cleanup(server.Close)
	store, err := Open(t.Context(), Config{BaseURL: server.URL, UserID: "user", AccessToken: "credential", HTTPClient: server.Client()})
	if err != nil {
		t.Fatal(err)
	}
	current := &clientpb.Settings{}
	settingsRevision, err := store.GetSettings(t.Context(), current)
	if err != nil || settingsRevision != 4 || current.GetNetwork() == nil {
		t.Fatalf("GetSettings() = %s, revision %d, %v", current, settingsRevision, err)
	}
	if err := store.PutSettings(t.Context(), clientpb.Settings_builder{Network: current.GetNetwork()}.Build(), settingsRevision); err != nil {
		t.Fatal(err)
	}
}

func TestStoreCreatesCentralSettingsWithExclusivePrecondition(t *testing.T) {
	now := time.Now().UTC()
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set(releaseGenerationHeader, "17")
		switch {
		case request.URL.Path == "/v1/auth/exchange":
			writeSession(t, writer, "access", "refresh", now)
		case request.URL.Path == "/v1/settings" && request.Method == http.MethodPut:
			if request.Header.Get("If-None-Match") != "*" || request.Header.Get("If-Match") != "" {
				t.Errorf("settings create precondition = %v", request.Header)
			}
			input := &servicepb.PutResourceRequest{}
			decodeRequest(t, request, input)
			if input.GetMutation().GetExpectedRevision() != 0 || input.GetMutation().GetCommandId() != request.Header.Get("Idempotency-Key") {
				t.Errorf("settings create mutation = %s", protoJSON(t, input.GetMutation()))
			}
			writeProto(t, writer, servicepb.PutResourceResponse_builder{Resource: settingsResource(clientpb.Settings_builder{}.Build(), 1)}.Build())
		default:
			http.NotFound(writer, request)
		}
	}))
	t.Cleanup(server.Close)
	store, err := Open(t.Context(), Config{
		BaseURL: server.URL, UserID: "user", AccessToken: "credential", HTTPClient: server.Client(),
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.PutSettings(t.Context(), clientpb.Settings_builder{Network: clientpb.NetworkSettings_builder{Direct: clientpb.DirectNetwork_builder{}.Build()}.Build()}.Build(), 0); err != nil {
		t.Fatal(err)
	}
}

func settingsResource(settings *clientpb.Settings, revision int64) *clientpb.Resource {
	id := "settings"
	return clientpb.Resource_builder{
		Identity: commonpb.ResourceIdentity_builder{Id: &id, Revision: &revision}.Build(),
		Settings: settings,
	}.Build()
}

func protoJSON(t *testing.T, message proto.Message) string {
	t.Helper()
	encoded, err := (protojson.MarshalOptions{UseProtoNames: false}).Marshal(message)
	if err != nil {
		t.Fatalf("marshal protobuf fixture: %v", err)
	}
	return string(encoded)
}

func int64Pointer(value int64) *int64 { return &value }
