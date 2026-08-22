package main

import (
	"errors"
	"fmt"
	"strings"

	"buf.build/go/protovalidate"
	clientpb "github.com/cineko-org/contracts/v3/gen/go/cineko/client"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
)

const maximumDesktopProtoJSON = 1 << 20

func desktopErrorEvent(userID, kind, message string) *clientpb.AppEvent {
	return clientpb.AppEvent_builder{
		UserId:  &userID,
		Kind:    &kind,
		Message: &message,
		Error:   clientpb.EventError_builder{}.Build(),
	}.Build()
}

func desktopSuccessEvent(userID, kind, message string) *clientpb.AppEvent {
	return clientpb.AppEvent_builder{
		UserId:  &userID,
		Kind:    &kind,
		Message: &message,
		Success: clientpb.EventSuccess_builder{}.Build(),
	}.Build()
}

// marshalDesktopProtoJSON crosses the Wails string bridge with canonical
// ProtoJSON because Wails' encoding/json codec cannot encode opaque Go Proto.
func marshalDesktopProtoJSON(message proto.Message) (string, error) {
	if message == nil {
		return "", errors.New("desktop protobuf message is required")
	}
	if err := protovalidate.Validate(message); err != nil {
		return "", fmt.Errorf("validate desktop protobuf message: %w", err)
	}
	payload, err := (protojson.MarshalOptions{UseProtoNames: false}).Marshal(message)
	if err != nil {
		return "", fmt.Errorf("encode desktop protobuf message: %w", err)
	}
	return string(payload), nil
}

// unmarshalDesktopProtoJSON rejects unknown fields and validates the generated
// message before any desktop mutation reaches application code.
func unmarshalDesktopProtoJSON(payload string, message proto.Message) error {
	if message == nil {
		return errors.New("desktop protobuf message is required")
	}
	payload = strings.TrimSpace(payload)
	if payload == "" || len(payload) > maximumDesktopProtoJSON {
		return errors.New("desktop protobuf payload is missing or too large")
	}
	if err := (protojson.UnmarshalOptions{DiscardUnknown: false}).Unmarshal([]byte(payload), message); err != nil {
		return fmt.Errorf("decode desktop protobuf message: %w", err)
	}
	if err := protovalidate.Validate(message); err != nil {
		return fmt.Errorf("validate desktop protobuf message: %w", err)
	}
	return nil
}
