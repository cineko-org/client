package centralhttp

import (
	"context"
	"errors"
	"net/http"
	"testing"
	"time"

	"github.com/cineko-org/client/internal/application"
	"github.com/cineko-org/client/internal/domain"
)

type ownershipRoundTripFunc func(*http.Request) (*http.Response, error)

func (function ownershipRoundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return function(request)
}

func TestEmbeddedResourceOwnershipRejectsForeignOrEmptyOwners(t *testing.T) {
	t.Parallel()
	store := &Store{userID: "user-one"}
	for _, payload := range [][]byte{
		[]byte(`{"userId":"user-two"}`),
		[]byte(`{"userId":""}`),
		[]byte(`{"userId":"   "}`),
	} {
		if err := store.validateEmbeddedOwnership(payload); !errors.Is(err, application.ErrNotFound) {
			t.Errorf("validateEmbeddedOwnership(%s) = %v", payload, err)
		}
	}
	for _, payload := range [][]byte{
		[]byte(`{"userId":"user-one"}`),
		[]byte(`{"id":"shared-theater"}`),
	} {
		if err := store.validateEmbeddedOwnership(payload); err != nil {
			t.Errorf("validateEmbeddedOwnership(%s) = %v", payload, err)
		}
	}
	if err := store.validateEmbeddedOwnership([]byte(`{`)); err == nil {
		t.Fatal("malformed ownership payload accepted")
	}
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
	err := store.PutPreset(context.Background(), domain.Preset{
		ID: "preset", UserID: "user-two", Name: "foreign", TheaterID: "theater",
		AuditoriumID: "auditorium", SeatCount: 1,
	})
	if !errors.Is(err, application.ErrNotFound) {
		t.Fatalf("PutPreset(foreign owner) = %v", err)
	}
}

func TestReplaceConfigurationValidatesEveryOwnerBeforeDeleting(t *testing.T) {
	t.Parallel()
	store := &Store{
		userID: "user-one",
		client: &http.Client{Transport: ownershipRoundTripFunc(func(*http.Request) (*http.Response, error) {
			t.Fatal("invalid replacement performed a network mutation")
			return nil, errors.New("unexpected request")
		})},
	}
	err := store.ReplaceConfiguration(context.Background(), domain.Configuration{
		Presets: []domain.Preset{{
			ID: "preset", UserID: "user-two", Name: "foreign", TheaterID: "theater",
			AuditoriumID: "auditorium", SeatCount: 1, CreatedAt: time.Now(),
		}},
	})
	if !errors.Is(err, application.ErrNotFound) {
		t.Fatalf("ReplaceConfiguration(foreign owner) = %v", err)
	}
}
