package main

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/cineko-org/client/internal/domain"
	central "github.com/cineko-org/contracts/v3"
)

type desktopExecutionWorker struct {
	store          executionStore
	server         executionServer
	installationID string
	userID         string
	retryDelay     func(int) time.Duration
}

type executionStore interface {
	ClaimExecution(context.Context, string) (*central.ExecutionCommand, error)
	HeartbeatExecution(context.Context, string, string) (central.ExecutionHeartbeatResponse, error)
	CompleteExecution(context.Context, string, central.ExecutionResultRequest) error
	ExecutionReady() <-chan struct{}
	ExecutionClaimRetryable(error) bool
}

type executionServer interface {
	CanAcceptExecution() bool
	ExecutionAvailable() <-chan struct{}
	ExecuteAvailability(context.Context, string, domain.Showtime) error
	RecordLocalSystemEvent(string, string, domain.EventTone, string)
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
			if ctx.Err() != nil {
				return nil
			}
			if !claimFailureReported {
				worker.server.RecordLocalSystemEvent(
					worker.userID, "execution.claim_failed", domain.EventError,
					"예매 실행 신호를 확인하지 못했습니다. 잠시 후 다시 시도합니다.",
				)
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
		worker.execute(ctx, *command)
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

func (worker *desktopExecutionWorker) execute(ctx context.Context, command central.ExecutionCommand) {
	showtime, err := executionShowtime(command.Payload)
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
		executionDone <- worker.server.ExecuteAvailability(executionContext, command.MonitorID, showtime)
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
	result := central.ExecutionResultRequest{LeaseToken: command.LeaseToken, Status: "completed"}
	if err != nil {
		result.Status = "failed"
		if heartbeatErr != nil {
			result.ReasonCode = "execution_lease_lost"
		} else {
			result.ReasonCode = executionFailureCode(err)
		}
		worker.server.RecordLocalSystemEvent(
			worker.userID, "execution.failed", domain.EventError,
			"예매 준비에 실패했습니다. 모니터 상태를 확인하고 다시 시도하세요.",
		)
	} else {
		worker.server.RecordLocalSystemEvent(
			worker.userID, "execution.prepared", domain.EventSuccess,
			"조건에 맞는 회차의 결제 확인 화면을 준비했습니다.",
		)
	}
	if completeErr := worker.store.CompleteExecution(context.WithoutCancel(ctx), command.ID, result); completeErr != nil {
		worker.server.RecordLocalSystemEvent(
			worker.userID, "execution.result_failed", domain.EventError,
			"예매 실행 결과를 저장하지 못했습니다. 연결을 확인하세요.",
		)
	}
}

func (worker *desktopExecutionWorker) heartbeat(ctx context.Context, command central.ExecutionCommand) error {
	expiresAt, err := worker.renewExecutionLease(
		ctx, command.ID, command.LeaseToken, command.LeaseExpiresAt,
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
			expiresAt, err = worker.renewExecutionLease(ctx, command.ID, command.LeaseToken, expiresAt)
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
	if !response.LeaseExpiresAt.After(time.Now()) {
		return time.Time{}, errors.New("central returned an expired execution lease")
	}
	return response.LeaseExpiresAt, nil
}

func (worker *desktopExecutionWorker) completeFailedExecution(
	ctx context.Context,
	command central.ExecutionCommand,
	reasonCode string,
	_ error,
) {
	worker.server.RecordLocalSystemEvent(
		worker.userID, "execution.failed", domain.EventError,
		"예매 실행 신호가 올바르지 않습니다. 모니터를 새로고침하고 다시 시도하세요.",
	)
	if err := worker.store.CompleteExecution(context.WithoutCancel(ctx), command.ID, central.ExecutionResultRequest{
		LeaseToken: command.LeaseToken, Status: "failed", ReasonCode: reasonCode,
	}); err != nil {
		worker.server.RecordLocalSystemEvent(
			worker.userID, "execution.result_failed", domain.EventError,
			"예매 실행 결과를 저장하지 못했습니다. 연결을 확인하세요.",
		)
	}
}

func executionShowtime(payload central.ExecutionPayload) (domain.Showtime, error) {
	const koreaOffset = 9 * 60 * 60
	location := time.FixedZone("Asia/Seoul", koreaOffset)
	value := payload.Showtime
	if value.ID == "" || value.TheaterID == "" || value.Movie.Title == "" || value.Auditorium.ID == "" ||
		value.Auditorium.Name == "" || value.StartsAt.IsZero() || value.EndsAt.IsZero() ||
		!value.EndsAt.After(value.StartsAt) || payload.ObservedAt.IsZero() ||
		value.AvailableSeats < 1 || value.Capacity < value.AvailableSeats || value.SoldOut {
		return domain.Showtime{}, errors.New("central execution showtime is incomplete or unavailable")
	}
	startsAt, endsAt := value.StartsAt.In(location), value.EndsAt.In(location)
	return domain.Showtime{
		ID: value.ID, Movie: value.Movie.Title, PosterURL: value.Movie.PosterURL,
		TheaterID:    value.TheaterID,
		AuditoriumID: value.Auditorium.ID, AuditoriumName: value.Auditorium.Name,
		ScreenTypes: append([]string(nil), value.Auditorium.ScreenTypes...),
		Date:        startsAt.Format(time.DateOnly), StartsAt: startsAt.Format("15:04"),
		EndsAt: endsAt.Format("15:04"), AvailableSeats: value.AvailableSeats,
		Capacity: value.Capacity, SoldOut: value.SoldOut, ObservedAt: payload.ObservedAt,
	}, nil
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
		return "client_interrupted"
	}
	return "booking_preparation_failed"
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
