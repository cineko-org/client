package centralhttp

import (
	"context"
	"errors"
	"net/http"
	"testing"

	"github.com/cineko-org/client/internal/application"
	clientpb "github.com/cineko-org/contracts/gen/go/cineko/client"
	commonpb "github.com/cineko-org/contracts/gen/go/cineko/common"
)

type ownershipRoundTripFunc func(*http.Request) (*http.Response, error)

func (function ownershipRoundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return function(request)
}

func TestPutRejectsForeignEmbeddedOwnerBeforeNetworkUse(t *testing.T) {
	t.Parallel()
	store := &Store{
		userID: "user-one",
		client: &http.Client{Transport: ownershipRoundTripFunc(func(*http.Request) (*http.Response, error) {
			t.Fatal("foreign resource reached Central")
			return nil, errors.New("unexpected request")
		})},
	}
	id, revision, userID, presetID, name := "preset", int64(0), "user-two", "preset", "foreign"
	resource := clientpb.Resource_builder{
		Identity: commonpb.ResourceIdentity_builder{Id: &id, Revision: &revision}.Build(),
		Preset:   clientpb.Preset_builder{Id: &presetID, UserId: &userID, Name: &name}.Build(),
	}.Build()
	err := store.PutPreset(context.Background(), resource)
	if !errors.Is(err, application.ErrNotFound) {
		t.Fatalf("PutPreset(foreign owner) = %v", err)
	}
}
