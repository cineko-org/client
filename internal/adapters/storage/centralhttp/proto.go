package centralhttp

import (
	"errors"
	"fmt"
	"time"

	clientpb "github.com/cineko-org/contracts/v3/gen/go/cineko/client"
	commonpb "github.com/cineko-org/contracts/v3/gen/go/cineko/common"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"
)

func timestamp(value time.Time) *timestamppb.Timestamp {
	if value.IsZero() {
		return nil
	}
	return timestamppb.New(value)
}

func resourceIdentity(id string, revision int64) *commonpb.ResourceIdentity {
	return commonpb.ResourceIdentity_builder{Id: &id, Revision: &revision}.Build()
}

// resourceFor builds the generated persistence envelope used by Central. The
// resource body is intentionally kept as the generated Proto message; there
// is no intermediate DTO or JSON map at this boundary.
func resourceFor(kind, id string, revision int64, body proto.Message) (*clientpb.Resource, error) {
	resource := clientpb.Resource_builder{Identity: resourceIdentity(id, revision)}
	switch value := body.(type) {
	case *clientpb.Settings:
		if kind != "settings" {
			return nil, fmt.Errorf("resource kind %q does not accept settings", kind)
		}
		resource.Settings = value
	case *clientpb.Preset:
		if kind != "presets" {
			return nil, fmt.Errorf("resource kind %q does not accept presets", kind)
		}
		resource.Preset = value
	case *clientpb.Monitor:
		if kind != "monitors" {
			return nil, fmt.Errorf("resource kind %q does not accept monitors", kind)
		}
		resource.Monitor = value
	case *clientpb.Reservation:
		if kind != "reservations" {
			return nil, fmt.Errorf("resource kind %q does not accept reservations", kind)
		}
		resource.Reservation = value
	case *clientpb.ExternalOperation:
		if kind != "external-operations" {
			return nil, fmt.Errorf("resource kind %q does not accept external operations", kind)
		}
		resource.ExternalOperation = value
	case *clientpb.AppEvent:
		if kind != "app-events" {
			return nil, fmt.Errorf("resource kind %q does not accept app events", kind)
		}
		resource.AppEvent = value
	default:
		return nil, fmt.Errorf("unsupported Central resource kind %q: %T", kind, body)
	}
	return resource.Build(), nil
}

func validateResourceKind(kind string, resource *clientpb.Resource) error {
	if resource == nil {
		return errors.New("resource is required")
	}
	switch kind {
	case "settings":
		if resource.GetSettings() == nil {
			return errors.New("settings resource is required")
		}
	case "presets":
		if resource.GetPreset() == nil {
			return errors.New("preset resource is required")
		}
	case "monitors":
		if resource.GetMonitor() == nil {
			return errors.New("monitor resource is required")
		}
	case "reservations":
		if resource.GetReservation() == nil {
			return errors.New("reservation resource is required")
		}
	case "external-operations":
		if resource.GetExternalOperation() == nil {
			return errors.New("external operation resource is required")
		}
	case "app-events":
		if resource.GetAppEvent() == nil {
			return errors.New("app event resource is required")
		}
	default:
		return fmt.Errorf("unsupported Central resource kind %q", kind)
	}
	return nil
}

func resourceOwner(resource *clientpb.Resource) string {
	if resource == nil {
		return ""
	}
	switch {
	case resource.GetPreset() != nil:
		return resource.GetPreset().GetUserId()
	case resource.GetMonitor() != nil:
		return resource.GetMonitor().GetUserId()
	case resource.GetReservation() != nil:
		return resource.GetReservation().GetUserId()
	case resource.GetExternalOperation() != nil:
		return resource.GetExternalOperation().GetUserId()
	case resource.GetAppEvent() != nil:
		return resource.GetAppEvent().GetUserId()
	default:
		return ""
	}
}
