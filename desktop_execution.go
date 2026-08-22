package main

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/cineko-org/client/internal/adapters/cgv"
	"github.com/cineko-org/client/internal/application"
	catalogpb "github.com/cineko-org/contracts/gen/go/cineko/catalog"
	clientpb "github.com/cineko-org/contracts/gen/go/cineko/client"
	executionpb "github.com/cineko-org/contracts/gen/go/cineko/execution"
)

type desktopExecutionWorker struct {
	store          executionStore
	server         executionServer
	installationID string
	userID         string
	retryDelay     func(int) time.Duration
}

const (
	executionReasonPreferredSeatsUnavailable = "preferred_seats_unavailable"
	executionReasonShowtimeUnavailable       = "showtime_unavailable"
	executionReasonAuthenticationRequired    = "authentication_required"
	executionReasonCaptchaRequired           = "captcha_required"
	executionReasonProviderContractChanged   = "provider_contract_changed"
	executionReasonProviderAccessBlocked     = "provider_access_blocked"
	executionReasonProviderThrottled         = "provider_throttled"
	executionReasonClientInterrupted         = "client_interrupted"
	executionReasonBookingPreparationFailed  = "booking_preparation_failed"
)

type executionStore interface {
	ClaimExecution(context.Context, string) (*executionpb.Command, error)
	HeartbeatExecution(context.Context, string, string) (*executionpb.HeartbeatResponse, error)
	CompleteExecution(context.Context, string, *executionpb.ResultRequest) error
	ExecutionReady() <-chan struct{}
	ExecutionClaimRetryable(error) bool
}

type executionServer interface {
	CanAcceptExecution() bool
	ExecutionAvailable() <-chan struct{}
	ExecuteAvailability(context.Context, string, *catalogpb.Showtime) error
	RecordLocalSystemEvent(*clientpb.AppEvent)
}

func (worker *desktopExecutionWorker) Run(ctx context.Context) error {
	claimFailureReported := false
	claimFailures := 0
	for ctx.Err() == nil {
		if !worker.server.CanAcceptExecution() {
			if !waitExecutionSignal(ctx, worker.server.ExecutionAvailable()) {
				return nil
			}
			continue
		}
		command, err := worker.store.ClaimExecution(ctx, worker.installationID)
		if err != nil {
			if contextErr := ctx.Err(); contextErr != nil {
				return contextErr
			}
			if !claimFailureReported {
				worker.server.RecordLocalSystemEvent(desktopErrorEvent(
					worker.userID, "execution.claim_failed", "예매 실행 신호를 확인하지 못했습니다. 잠시 후 다시 시도합니다.",
				))
				claimFailureReported = true
			}
			if !worker.store.ExecutionClaimRetryable(err) {
				return fmt.Errorf("claim execution: %w", err)
			}
			claimFailures++
			if !waitExecutionRetry(ctx, worker.store.ExecutionReady(), worker.claimRetryDelay(claimFailures)) {
				return nil
			}
			continue
		}
		claimFailures = 0
		claimFailureReported = false
		if command == nil {
			if !waitExecutionSignal(ctx, worker.store.ExecutionReady()) {
				return nil
			}
			continue
		}
		worker.execute(ctx, command)
	}
	return nil
}

func (worker *desktopExecutionWorker) claimRetryDelay(failures int) time.Duration {
	if worker.retryDelay != nil {
		return worker.retryDelay(failures)
	}
	delay := 250 * time.Millisecond
	for attempt := 1; attempt < failures && delay < 8*time.Second; attempt++ {
		delay *= 2
	}
	if delay > 8*time.Second {
		return 8 * time.Second
	}
	return delay
}

func (worker *desktopExecutionWorker) execute(ctx context.Context, command *executionpb.Command) {
	showtime, err := executionShowtime(command.GetPayload())
	if err != nil {
		worker.completeFailedExecution(ctx, command, "invalid_execution_payload", err)
		return
	}
	executionContext, cancel := context.WithCancel(ctx)
	heartbeatDone := make(chan error, 1)
	executionDone := make(chan error, 1)
	go func() {
		heartbeatErr := worker.heartbeat(executionContext, command)
		if heartbeatErr != nil {
			cancel()
		}
		heartbeatDone <- heartbeatErr
	}()
	go func() {
		executionDone <- worker.server.ExecuteAvailability(executionContext, command.GetMonitorId(), showtime)
	}()
	var heartbeatErr error
	select {
	case err = <-executionDone:
		// Successful preparation owns cancellation of the heartbeat request. A
		// context cancellation produced by this path is not a lost fence.
		cancel()
		heartbeatErr = <-heartbeatDone
		if errors.Is(heartbeatErr, context.Canceled) {
			heartbeatErr = nil
		}
	case heartbeatErr = <-heartbeatDone:
		cancel()
		err = <-executionDone
	}
	if ctx.Err() != nil {
		return
	}
	if heartbeatErr != nil {
		err = errors.Join(err, fmt.Errorf("execution lease heartbeat failed: %w", heartbeatErr))
	}
	commandID, leaseToken := command.GetId(), command.GetLeaseToken()
	result := executionpb.ResultRequest_builder{CommandId: &commandID, LeaseToken: &leaseToken}.Build()
	if err != nil {
		reasonCode := ""
		if heartbeatErr != nil {
			reasonCode = "execution_lease_lost"
		} else {
			reasonCode = executionFailureCode(err)
		}
		if reasonCode == executionReasonBookingPreparationFailed {
			result.SetRetryRequested(executionpb.RetryRequested_builder{ReasonCode: &reasonCode}.Build())
		} else {
			result.SetFailed(executionpb.Failed_builder{ReasonCode: &reasonCode}.Build())
		}
		message := "예매 준비에 실패했습니다. 모니터 상태를 확인하고 다시 시도하세요."
		if reasonCode == executionReasonAuthenticationRequired {
			message = "CGV 로그인이 필요합니다. 로그인 후 모니터를 다시 실행하세요."
		}
		worker.server.RecordLocalSystemEvent(desktopErrorEvent(worker.userID, "execution.failed", message))
	} else {
		result.SetCompleted(executionpb.Completed_builder{}.Build())
		worker.server.RecordLocalSystemEvent(desktopSuccessEvent(
			worker.userID, "execution.prepared", "조건에 맞는 회차의 결제 확인 화면을 준비했습니다.",
		))
	}
	if completeErr := worker.store.CompleteExecution(context.WithoutCancel(ctx), commandID, result); completeErr != nil {
		worker.server.RecordLocalSystemEvent(desktopErrorEvent(
			worker.userID, "execution.result_failed", "예매 실행 결과를 저장하지 못했습니다. 연결을 확인하세요.",
		))
	}
}

func (worker *desktopExecutionWorker) heartbeat(ctx context.Context, command *executionpb.Command) error {
	if command.GetLeaseExpiresAt() == nil {
		return errors.New("central returned an execution command without a lease expiry")
	}
	expiresAt, err := worker.renewExecutionLease(
		ctx, command.GetId(), command.GetLeaseToken(), command.GetLeaseExpiresAt().AsTime(),
	)
	if err != nil {
		return err
	}
	for {
		timer := time.NewTimer(executionHeartbeatInterval(expiresAt, time.Now()))
		select {
		case <-ctx.Done():
			if !timer.Stop() {
				<-timer.C
			}
			return nil
		case <-timer.C:
			expiresAt, err = worker.renewExecutionLease(ctx, command.GetId(), command.GetLeaseToken(), expiresAt)
			if err != nil {
				return err
			}
		}
	}
}

func (worker *desktopExecutionWorker) renewExecutionLease(
	ctx context.Context,
	commandID string,
	leaseToken string,
	expiresAt time.Time,
) (time.Time, error) {
	if !expiresAt.After(time.Now()) {
		return time.Time{}, errors.New("execution lease expired before heartbeat")
	}
	response, err := worker.store.HeartbeatExecution(ctx, commandID, leaseToken)
	if err != nil {
		return time.Time{}, err
	}
	if response == nil || response.GetLeaseExpiresAt() == nil || !response.GetLeaseExpiresAt().AsTime().After(time.Now()) {
		return time.Time{}, errors.New("central returned an expired execution lease")
	}
	return response.GetLeaseExpiresAt().AsTime(), nil
}

func (worker *desktopExecutionWorker) completeFailedExecution(
	ctx context.Context,
	command *executionpb.Command,
	reasonCode string,
	_ error,
) {
	worker.server.RecordLocalSystemEvent(desktopErrorEvent(
		worker.userID, "execution.failed", "예매 실행 신호가 올바르지 않습니다. 모니터를 새로고침하고 다시 시도하세요.",
	))
	commandID, leaseToken := command.GetId(), command.GetLeaseToken()
	result := executionpb.ResultRequest_builder{CommandId: &commandID, LeaseToken: &leaseToken}.Build()
	result.SetFailed(executionpb.Failed_builder{ReasonCode: &reasonCode}.Build())
	if err := worker.store.CompleteExecution(context.WithoutCancel(ctx), commandID, result); err != nil {
		worker.server.RecordLocalSystemEvent(desktopErrorEvent(
			worker.userID, "execution.result_failed", "예매 실행 결과를 저장하지 못했습니다. 연결을 확인하세요.",
		))
	}
}

func executionShowtime(payload *executionpb.Payload) (*catalogpb.Showtime, error) {
	if payload == nil || payload.GetShowtime() == nil || payload.GetObservedAt() == nil {
		return nil, errors.New("central execution showtime is incomplete or unavailable")
	}
	value := payload.GetShowtime()
	observedAt := payload.GetObservedAt().AsTime()
	if err := validateExecutionShowtime(value, observedAt); err != nil {
		return nil, err
	}
	return value, nil
}

func validateExecutionShowtime(value *catalogpb.Showtime, observedAt time.Time) error {
	if executionIdentityMissing(value) || executionScheduleDateInvalid(value) || executionTimeInvalid(value, observedAt) || executionAvailabilityInvalid(value) {
		return errors.New("central execution showtime is incomplete or unavailable")
	}
	return nil
}

func executionScheduleDateInvalid(value *catalogpb.Showtime) bool {
	date := value.GetScheduleDate()
	if date == nil {
		return true
	}
	parsed := time.Date(int(date.GetYear()), time.Month(date.GetMonth()), int(date.GetDay()), 0, 0, 0, 0, time.UTC)
	return parsed.Year() != int(date.GetYear()) || parsed.Month() != time.Month(date.GetMonth()) || parsed.Day() != int(date.GetDay())
}

func executionIdentityMissing(value *catalogpb.Showtime) bool {
	return value == nil || value.GetId() == "" || value.GetProviderId() == "" || value.GetSourceKey() == "" ||
		value.GetTheaterId() == "" || value.GetMovie() == nil || value.GetMovie().GetId() == "" || value.GetMovie().GetTitle() == "" ||
		value.GetAuditorium() == nil || value.GetAuditorium().GetId() == "" || value.GetAuditorium().GetName() == ""
}

func executionTimeInvalid(value *catalogpb.Showtime, observedAt time.Time) bool {
	if value == nil || value.GetStartsAt() == nil || value.GetEndsAt() == nil || observedAt.IsZero() {
		return true
	}
	startsAt, endsAt := value.GetStartsAt().AsTime(), value.GetEndsAt().AsTime()
	return startsAt.IsZero() || endsAt.IsZero() || !endsAt.After(startsAt)
}

func executionAvailabilityInvalid(value *catalogpb.Showtime) bool {
	return value == nil || value.GetAvailableSeats() < 1 || value.GetCapacity() < value.GetAvailableSeats() || value.GetSoldOut()
}

func executionHeartbeatInterval(expiresAt, now time.Time) time.Duration {
	interval := expiresAt.Sub(now) / 3
	if interval < time.Second {
		return time.Second
	}
	return interval
}

func executionFailureCode(err error) string {
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return executionReasonClientInterrupted
	}
	if errors.Is(err, application.ErrSeatUnavailable) {
		return executionReasonPreferredSeatsUnavailable
	}
	if errors.Is(err, application.ErrBookingNotOpen) {
		return executionReasonShowtimeUnavailable
	}
	switch {
	case errors.Is(err, cgv.ErrAuthenticationRequired):
		return executionReasonAuthenticationRequired
	case errors.Is(err, cgv.ErrCaptchaRequired):
		return executionReasonCaptchaRequired
	case errors.Is(err, cgv.ErrUIContractChanged):
		return executionReasonProviderContractChanged
	case errors.Is(err, cgv.ErrProviderAccessBlocked):
		return executionReasonProviderAccessBlocked
	case errors.Is(err, cgv.ErrProviderThrottled):
		return executionReasonProviderThrottled
	}
	return executionReasonBookingPreparationFailed
}

func waitExecutionSignal(ctx context.Context, signal <-chan struct{}) bool {
	select {
	case <-ctx.Done():
		return false
	case <-signal:
		return true
	}
}

func waitExecutionRetry(ctx context.Context, signal <-chan struct{}, delay time.Duration) bool {
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-signal:
		return true
	case <-timer.C:
		return true
	}
}
