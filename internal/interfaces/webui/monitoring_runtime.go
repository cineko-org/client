package webui

import (
	"context"
	"encoding/json"
	"net/http"

	clientpb "github.com/cineko-org/contracts/v3/gen/go/cineko/client"
	"google.golang.org/protobuf/encoding/protojson"
)

// Runtime state is deliberately separate from persisted monitoring intent.
// Reading this endpoint never opens a browser or queries CGV.
type monitoringRuntimeState struct {
	State  string `json:"state"`
	Reason string `json:"reason"`
}

func (server *Server) SetMonitoringRunning(running bool) {
	server.tasksMu.Lock()
	server.monitoringRunning = running
	server.tasksMu.Unlock()
}

func (server *Server) monitoringStateForSnapshot(account *clientpb.WebUIAccountState, scanner string) monitoringRuntimeState {
	server.tasksMu.RLock()
	running, preparationError := server.monitoringRunning, server.bookingPreparationError
	server.tasksMu.RUnlock()
	if !running {
		return monitoringRuntimeState{"stopped", "감시 워커가 실행되고 있지 않습니다."}
	}
	if server.monitoringBlockReason != nil {
		if reason := server.monitoringBlockReason(); reason != "" {
			return monitoringRuntimeState{"rate_limited", reason}
		}
	}
	if scanner == "failed" {
		return monitoringRuntimeState{"scan_failed", "신규 일정 조회에 실패했습니다. 관제 로그를 확인하세요. 직접 접속으로 대체하지 않습니다."}
	}
	if account == nil || account.GetChecking() != nil {
		return monitoringRuntimeState{"checking", "CGV 로그인 상태를 확인하고 있습니다."}
	}
	if account.GetAuthenticated() == nil {
		return monitoringRuntimeState{"login_required", "CGV 로그인 확인이 필요합니다. 예매를 진행할 수 없습니다."}
	}
	if preparationError != "" {
		return monitoringRuntimeState{"preparation_failed", "예매 브라우저 준비에 실패했습니다. 관제 로그를 확인하세요."}
	}
	return monitoringRuntimeState{"ready", ""}
}

func (server *Server) monitoringRuntime(writer http.ResponseWriter, _ *http.Request) {
	// Both projections must use the same account snapshot. Reading separately
	// can pair a stale "checking" reason with a newly authenticated green light.
	server.accountMu.RLock()
	account := server.account
	server.accountMu.RUnlock()
	if account == nil {
		account = clientpb.WebUIAccountState_builder{Checking: clientpb.WebUIAccountChecking_builder{}.Build()}.Build()
	}
	encodedAccount, err := protojson.Marshal(account)
	if err != nil {
		server.writeError(writer, err)
		return
	}
	server.tasksMu.RLock()
	tasks := make([]*clientpb.WebUITaskState, 0, len(server.tasks))
	for _, task := range server.tasks {
		tasks = append(tasks, task)
	}
	server.tasksMu.RUnlock()
	encodedTasks, err := protojson.Marshal(clientpb.WebUITaskStatusResponse_builder{Tasks: tasks}.Build())
	if err != nil {
		server.writeError(writer, err)
		return
	}
	writer.Header().Set("Content-Type", "application/json; charset=utf-8")
	scanner := "off"
	if server.scannerStatus != nil {
		scanner = server.scannerStatus()
	}
	writer.Header().Set("Cache-Control", "no-store")
	_ = json.NewEncoder(writer).Encode(struct {
		monitoringRuntimeState
		Account json.RawMessage `json:"account"`
		Tasks   json.RawMessage `json:"tasks"`
		Scanner string          `json:"scanner"`
	}{server.monitoringStateForSnapshot(account, scanner), encodedAccount, encodedTasks, scanner})
}

func (server *Server) monitorConfigurationChanged(ctx context.Context) {
	if server.monitoringChanged != nil {
		server.monitoringChanged()
	}
	server.refreshBookingDemand(ctx)
	server.signalExecutionAvailable()
}
