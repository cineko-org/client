package centralhttp

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	clientpb "github.com/cineko-org/contracts/gen/go/cineko/client"
	commonpb "github.com/cineko-org/contracts/gen/go/cineko/common"
	servicepb "github.com/cineko-org/contracts/gen/go/cineko/service"
)

func TestPutResourceUsesServiceContractAndAppliesAuthoritativeRevision(t *testing.T) {
	t.Parallel()
	now := time.Now().UTC()
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set(releaseGenerationHeader, "17")
		switch request.URL.Path {
		case "/v1/auth/exchange":
			input := &servicepb.ExchangeTokenRequest{}
			decodeRequest(t, request, input)
			if input.GetRequest().GetUserId() != "user" || input.GetRequest().GetAccessToken() != "credential" {
				t.Errorf("exchange request = %s", protoJSON(t, input))
			}
			writeSession(t, writer, "access", "refresh", now)
		case "/v1/presets":
			input := &servicepb.PutResourceRequest{}
			decodeRequest(t, request, input)
			if input.GetMutation().GetExpectedRevision() != 0 ||
				input.GetMutation().GetCommandId() != request.Header.Get("Idempotency-Key") ||
				input.GetResource().GetPreset() == nil {
				t.Errorf("put resource request = %s", protoJSON(t, input))
			}
			stored := protoResourceWithRevision(input.GetResource(), 1)
			writeProto(t, writer, servicepb.PutResourceResponse_builder{Resource: stored}.Build())
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
	id, userID, name := "preset-1", "user", "center"
	revision := int64(0)
	resource := clientpb.Resource_builder{
		Identity: commonpb.ResourceIdentity_builder{Id: &id, Revision: &revision}.Build(),
		Preset:   clientpb.Preset_builder{Id: &id, UserId: &userID, Name: &name}.Build(),
	}.Build()
	if err := store.PutPreset(t.Context(), resource); err != nil {
		t.Fatal(err)
	}
	if resource.GetIdentity().GetRevision() != 1 {
		t.Fatalf("authoritative revision = %d", resource.GetIdentity().GetRevision())
	}
}

func TestResourceReadListAndDeleteUseServiceContracts(t *testing.T) {
	t.Parallel()
	now := time.Now().UTC()
	id, userID, name := "preset-1", "user", "center"
	revision := int64(3)
	resource := clientpb.Resource_builder{
		Identity: commonpb.ResourceIdentity_builder{Id: &id, Revision: &revision}.Build(),
		Preset:   clientpb.Preset_builder{Id: &id, UserId: &userID, Name: &name}.Build(),
	}.Build()
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set(releaseGenerationHeader, "17")
		switch {
		case request.URL.Path == "/v1/auth/exchange":
			writeSession(t, writer, "access", "refresh", now)
		case request.URL.Path == "/v1/presets/preset-1" && request.Method == http.MethodGet:
			input := &servicepb.GetResourceRequest{}
			decodeRequest(t, request, input)
			if input.GetId() != id || input.GetKind().GetPreset() == nil {
				t.Errorf("get request = %s", protoJSON(t, input))
			}
			writeProto(t, writer, servicepb.GetResourceResponse_builder{Resource: resource}.Build())
		case request.URL.Path == "/v1/presets" && request.Method == http.MethodGet:
			input := &servicepb.ListResourcesRequest{}
			decodeRequest(t, request, input)
			if input.GetKind().GetPreset() == nil {
				t.Errorf("list request = %s", protoJSON(t, input))
			}
			writeProto(t, writer, servicepb.ListResourcesResponse_builder{Resources: []*clientpb.Resource{resource}}.Build())
		case request.URL.Path == "/v1/presets/preset-1" && request.Method == http.MethodDelete:
			input := &servicepb.DeleteResourceRequest{}
			decodeRequest(t, request, input)
			if input.GetId() != id || input.GetKind().GetPreset() == nil ||
				input.GetMutation().GetExpectedRevision() != revision ||
				input.GetMutation().GetCommandId() != request.Header.Get("Idempotency-Key") {
				t.Errorf("delete request = %s", protoJSON(t, input))
			}
			writeProto(t, writer, &servicepb.DeleteResourceResponse{})
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
	if _, err := store.GetPreset(t.Context(), id); err != nil {
		t.Fatal(err)
	}
	if values, err := store.ListPresetsByUser(t.Context(), userID); err != nil || len(values) != 1 {
		t.Fatalf("ListPresetsByUser() = %d, %v", len(values), err)
	}
	if err := store.DeletePreset(t.Context(), id); err != nil {
		t.Fatal(err)
	}
}

func protoResourceWithRevision(resource *clientpb.Resource, revision int64) *clientpb.Resource {
	stored := clientpb.Resource_builder{
		Identity: commonpb.ResourceIdentity_builder{
			Id:       stringPointer(resource.GetIdentity().GetId()),
			Revision: &revision,
		}.Build(),
	}.Build()
	switch {
	case resource.GetSettings() != nil:
		stored.SetSettings(resource.GetSettings())
	case resource.GetPreset() != nil:
		stored.SetPreset(resource.GetPreset())
	case resource.GetMonitor() != nil:
		stored.SetMonitor(resource.GetMonitor())
	case resource.GetReservation() != nil:
		stored.SetReservation(resource.GetReservation())
	case resource.GetExternalOperation() != nil:
		stored.SetExternalOperation(resource.GetExternalOperation())
	case resource.GetAppEvent() != nil:
		stored.SetAppEvent(resource.GetAppEvent())
	}
	return stored
}
