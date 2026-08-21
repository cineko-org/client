package centralhttp

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
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

func TestEncodeRequestBodyRejectsContractViolation(t *testing.T) {
	t.Parallel()

	_, err := encodeRequestBody(clientpb.TokenExchangeRequest_builder{}.Build())
	if err == nil || !strings.Contains(err.Error(), "validate Central request") {
		t.Fatalf("encodeRequestBody() error = %v", err)
	}
}

func TestStoreRefreshesExpiredSessionOnceAcrossConcurrentRequests(t *testing.T) {
	now := time.Now().UTC()
	var refreshes atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set(releaseGenerationHeader, "17")
		switch request.URL.Path {
		case "/v1/auth/exchange":
			writeSession(t, writer, "access-1", "refresh-1", now)
		case "/v1/auth/refresh":
			input := &servicepb.RefreshTokenRequest{}
			decodeRequest(t, request, input)
			if input.GetRequest().GetRefreshToken() != "refresh-1" {
				t.Errorf("refresh token = %q", input.GetRequest().GetRefreshToken())
			}
			refreshes.Add(1)
			writeRefreshSession(t, writer, "access-2", "refresh-2", now)
		case "/v1/devices/install":
			if request.Header.Get("Authorization") != "Bearer access-2" {
				t.Errorf("authorization = %q", request.Header.Get("Authorization"))
			}
			input := &servicepb.UpsertDeviceRequest{}
			decodeRequest(t, request, input)
			writeProto(t, writer, servicepb.UpsertDeviceResponse_builder{Device: input.GetDevice()}.Build())
		default:
			http.NotFound(writer, request)
		}
	}))
	t.Cleanup(server.Close)
	store, err := Open(context.Background(), Config{
		BaseURL: server.URL, UserID: "user", AccessToken: "credential", HTTPClient: server.Client(),
	})
	if err != nil {
		t.Fatal(err)
	}
	store.clock = func() time.Time { return now }
	store.authMu.Lock()
	store.expiresAt = now
	store.authMu.Unlock()

	const workers = 8
	start := make(chan struct{})
	errors := make(chan error, workers)
	var group sync.WaitGroup
	for range workers {
		group.Add(1)
		go func() {
			defer group.Done()
			<-start
			_, registerErr := store.RegisterDevice(context.Background(), clientpb.Device_builder{
				InstallationId: stringPointer("install"), UserId: stringPointer("user"), DeviceId: stringPointer("device"),
			}.Build())
			errors <- registerErr
		}()
	}
	close(start)
	group.Wait()
	close(errors)
	for err := range errors {
		if err != nil {
			t.Fatal(err)
		}
	}
	if refreshes.Load() != 1 {
		t.Fatalf("refresh calls = %d", refreshes.Load())
	}
}

func TestStoreRefreshesAndRetriesAfterUnauthorizedResponse(t *testing.T) {
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
		case "/v1/devices/install":
			if request.Header.Get("Authorization") == "Bearer access-1" {
				writer.WriteHeader(http.StatusUnauthorized)
				writeProto(t, writer, commonpb.APIErrorResponse_builder{Error: commonpb.APIError_builder{
					Code: stringPointer("unauthorized"), Message: stringPointer("expired"), RequestId: stringPointer("test-request"),
				}.Build()}.Build())
				return
			}
			input := &servicepb.UpsertDeviceRequest{}
			decodeRequest(t, request, input)
			writeProto(t, writer, servicepb.UpsertDeviceResponse_builder{Device: input.GetDevice()}.Build())
		default:
			http.NotFound(writer, request)
		}
	}))
	t.Cleanup(server.Close)
	store, err := Open(context.Background(), Config{
		BaseURL: server.URL, UserID: "user", AccessToken: "credential", HTTPClient: server.Client(),
	})
	if err != nil {
		t.Fatal(err)
	}
	store.clock = func() time.Time { return now }
	if _, err := store.RegisterDevice(context.Background(), clientpb.Device_builder{InstallationId: stringPointer("install")}.Build()); err != nil {
		t.Fatal(err)
	}
	if refreshes.Load() != 1 {
		t.Fatalf("refresh calls = %d", refreshes.Load())
	}
}

func writeSession(t *testing.T, writer http.ResponseWriter, access string, refresh string, now time.Time) {
	t.Helper()
	writeProto(t, writer, servicepb.ExchangeTokenResponse_builder{Authentication: authentication(access, refresh, now)}.Build())
}

func writeRefreshSession(t *testing.T, writer http.ResponseWriter, access string, refresh string, now time.Time) {
	t.Helper()
	writeProto(t, writer, servicepb.RefreshTokenResponse_builder{Authentication: authentication(access, refresh, now)}.Build())
}

func authentication(access string, refresh string, now time.Time) *clientpb.AuthenticationResponse {
	return clientpb.AuthenticationResponse_builder{
		AccessToken:      stringPointer(access),
		ExpiresAt:        timestamppb.New(now.Add(time.Hour)),
		RefreshToken:     stringPointer(refresh),
		RefreshExpiresAt: timestamppb.New(now.Add(24 * time.Hour)),
		User:             clientpb.User_builder{Id: stringPointer("user")}.Build(),
	}.Build()
}

func writeProto(t *testing.T, writer http.ResponseWriter, message proto.Message) {
	t.Helper()
	writer.Header().Set("Content-Type", "application/json")
	encoded, err := (protojson.MarshalOptions{UseProtoNames: false}).Marshal(message)
	if err != nil {
		t.Errorf("encode protobuf response: %v", err)
		return
	}
	if _, err := writer.Write(encoded); err != nil {
		t.Errorf("write protobuf response: %v", err)
	}
}

func decodeRequest(t *testing.T, request *http.Request, value proto.Message) {
	t.Helper()
	contents, err := io.ReadAll(request.Body)
	if err != nil {
		t.Errorf("read request: %v", err)
		return
	}
	if err := (protojson.UnmarshalOptions{DiscardUnknown: false}).Unmarshal(contents, value); err != nil {
		t.Errorf("decode request: %v", err)
	}
}

func stringPointer(value string) *string { return &value }
