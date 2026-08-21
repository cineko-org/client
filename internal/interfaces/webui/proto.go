package webui

import (
	"io"
	"net/http"
	"strings"
	"time"

	"buf.build/go/protovalidate"
	clientpb "github.com/cineko-org/contracts/gen/go/cineko/client"
	commonpb "github.com/cineko-org/contracts/gen/go/cineko/common"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"
)

const maxRequestBody = 1 << 20

func decodeProtoJSON(server *Server, writer http.ResponseWriter, request *http.Request, output proto.Message) bool {
	payload, err := io.ReadAll(http.MaxBytesReader(writer, request.Body, maxRequestBody))
	if err != nil || (protojson.UnmarshalOptions{DiscardUnknown: false}).Unmarshal(payload, output) != nil {
		server.writeAPIError(writer, request, http.StatusBadRequest, "invalid_json", "request body is invalid", false)
		return false
	}
	if err := protovalidate.Validate(output); err != nil {
		server.writeAPIError(writer, request, http.StatusBadRequest, "invalid_request", "request body violates the contract", false)
		return false
	}
	return true
}

func writeProtoJSON(writer http.ResponseWriter, status int, message proto.Message) {
	payload, err := (protojson.MarshalOptions{UseProtoNames: false}).Marshal(message)
	if err != nil {
		http.Error(writer, http.StatusText(http.StatusInternalServerError), http.StatusInternalServerError)
		return
	}
	writer.Header().Set("Content-Type", "application/json; charset=utf-8")
	writer.WriteHeader(status)
	_, _ = writer.Write(payload) // #nosec G705 -- payload is serialized ProtoJSON, not HTML.
}

func (server *Server) writeAPIError(writer http.ResponseWriter, request *http.Request, status int, code, message string, retryable bool) {
	requestID := ""
	if request != nil {
		requestID = strings.TrimSpace(request.Header.Get("X-Request-Id"))
	}
	if requestID == "" {
		requestID = "webui"
		if server.ids != nil {
			requestID = server.ids.NewID()
		}
	}
	errorMessage := commonpb.APIError_builder{
		Code: &code, Message: &message, Retryable: &retryable, RequestId: &requestID,
	}.Build()
	writeProtoJSON(writer, status, commonpb.APIErrorResponse_builder{Error: errorMessage}.Build())
}

func actionStatus(started bool) *clientpb.WebUIActionStatus {
	if started {
		return clientpb.WebUIActionStatus_builder{Started: clientpb.WebUIActionStarted_builder{}.Build()}.Build()
	}
	return clientpb.WebUIActionStatus_builder{Completed: clientpb.WebUIActionCompleted_builder{}.Build()}.Build()
}

func taskStateMessage(id, status, message string, updatedAt time.Time) *clientpb.WebUITaskState {
	value := clientpb.WebUITaskState_builder{}
	value.Id = &id
	value.Message = &message
	value.UpdatedAt = timestamp(updatedAt)
	switch status {
	case "running":
		value.Running = clientpb.WebUITaskRunning_builder{}.Build()
	case "failed":
		value.Failed = clientpb.WebUITaskFailed_builder{}.Build()
	case "stopped":
		value.Stopped = clientpb.WebUITaskStopped_builder{}.Build()
	default:
		value.Completed = clientpb.WebUITaskCompleted_builder{}.Build()
	}
	return value.Build()
}

func accountStateMessage(status string, credentialsSaved bool, accountID, message string, checkedAt time.Time) *clientpb.WebUIAccountState {
	value := clientpb.WebUIAccountState_builder{
		CredentialsSaved: &credentialsSaved,
		AccountId:        &accountID,
		Message:          &message,
		CheckedAt:        timestamp(checkedAt),
	}
	switch status {
	case "authenticated":
		value.Authenticated = clientpb.WebUIAccountAuthenticated_builder{}.Build()
	case "unauthenticated":
		value.Unauthenticated = clientpb.WebUIAccountUnauthenticated_builder{}.Build()
	case "error":
		value.Error = clientpb.WebUIAccountError_builder{}.Build()
	default:
		value.Checking = clientpb.WebUIAccountChecking_builder{}.Build()
	}
	return value.Build()
}

func timestamp(value time.Time) *timestamppb.Timestamp {
	if value.IsZero() {
		return nil
	}
	return timestamppb.New(value)
}

func resourceIdentity(id string, createdAt time.Time) *commonpb.ResourceIdentity {
	revision := int64(0)
	return commonpb.ResourceIdentity_builder{
		Id: &id, Revision: &revision, CreatedAt: timestamp(createdAt),
	}.Build()
}
