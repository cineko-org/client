package main

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/cineko-org/client/internal/domain"
	central "github.com/cineko-org/contracts/v3"
)

type executionStoreFake struct {
	mu           sync.Mutex
	heartbeatErr error
	heartbeat    func(context.Context) (central.ExecutionHeartbeatResponse, error)
	completed    chan central.ExecutionResultRequest
	claims       int
	claim        func() (*central.ExecutionCommand, error)
	ready        chan struct{}
	retryable    bool
}

func (store *executionStoreFake) ClaimExecution(context.Context, string) (*central.ExecutionCommand, error) {
	store.mu.Lock()
	defer store.mu.Unlock()
	store.claims++
	if store.claim != nil {
		return store.claim()
	}
	return nil, nil
}

func (store *executionStoreFake) ExecutionReady() <-chan struct{} {
	if store.ready == nil {
		store.ready = make(chan struct{}, 1)
	}
	return store.ready
}

func (store *executionStoreFake) ExecutionClaimRetryable(error) bool { return store.retryable }

func (store *executionStoreFake) HeartbeatExecution(
	ctx context.Context,
	_ string,
	_ string,
) (central.ExecutionHeartbeatResponse, error) {
	if store.heartbeat != nil {
		return store.heartbeat(ctx)
	}
	if store.heartbeatErr != nil {
		return central.ExecutionHeartbeatResponse{}, store.heartbeatErr
	}
	return central.ExecutionHeartbeatResponse{LeaseExpiresAt: time.Now().Add(time.Minute)}, nil
}

func TestCompletedPreparationDoesNotMistakeOwnedHeartbeatCancellationForFenceLoss(t *testing.T) {
	store := &executionStoreFake{
		completed: make(chan central.ExecutionResultRequest, 1),
		heartbeat: func(ctx context.Context) (central.ExecutionHeartbeatResponse, error) {
			<-ctx.Done()
			return central.ExecutionHeartbeatResponse{}, ctx.Err()
		},
	}
	server := &executionServerFake{}
	(&desktopExecutionWorker{store: store, server: server, userID: "user"}).execute(
		t.Context(), validExecutionCommand(time.Now().Add(time.Minute)),
	)
	if result := <-store.completed; result.Status != "completed" || result.ReasonCode != "" {
		t.Fatalf("completion = %+v", result)
	}
}

func (store *executionStoreFake) CompleteExecution(
	_ context.Context,
	_ string,
	result central.ExecutionResultRequest,
) error {
	store.completed <- result
	return nil
}

type executionServerFake struct {
	mu        sync.Mutex
	showtime  domain.Showtime
	run       func(context.Context) error
	accepting *bool
	available chan struct{}
}

func (server *executionServerFake) CanAcceptExecution() bool {
	return server.accepting == nil || *server.accepting
}

func (server *executionServerFake) ExecutionAvailable() <-chan struct{} {
	if server.available == nil {
		server.available = make(chan struct{}, 1)
	}
	return server.available
}

func (server *executionServerFake) ExecuteAvailability(
	ctx context.Context,
	_ string,
	showtime domain.Showtime,
) error {
	server.mu.Lock()
	server.showtime = showtime
	server.mu.Unlock()
	if server.run != nil {
		return server.run(ctx)
	}
	return nil
}

func (*executionServerFake) RecordLocalSystemEvent(string, string, domain.EventTone, string) {}

func TestExecutionWorkerDoesNotClaimWhilePaymentIsPending(t *testing.T) {
	accepting := false
	store := &executionStoreFake{}
	worker := desktopExecutionWorker{store: store, server: &executionServerFake{accepting: &accepting}}
	ctx, cancel := context.WithTimeout(t.Context(), 20*time.Millisecond)
	defer cancel()
	worker.Run(ctx)
	if store.claims != 0 {
		t.Fatalf("claimed %d executions while payment was pending", store.claims)
	}
}

func TestExecutionWorkerClaimsOnceThenWaitsForDurableWake(t *testing.T) {
	store := &executionStoreFake{ready: make(chan struct{}, 1)}
	server := &executionServerFake{available: make(chan struct{}, 1)}
	ctx, cancel := context.WithTimeout(t.Context(), 20*time.Millisecond)
	defer cancel()
	(&desktopExecutionWorker{store: store, server: server}).Run(ctx)
	store.mu.Lock()
	claims := store.claims
	store.mu.Unlock()
	if claims != 1 {
		t.Fatalf("claims = %d, want one initial claim without polling", claims)
	}
}

func TestExecutionWorkerConsumesWakeBufferedBeforeWait(t *testing.T) {
	ready := make(chan struct{}, 1)
	ready <- struct{}{}
	store := &executionStoreFake{ready: ready}
	server := &executionServerFake{available: make(chan struct{}, 1)}
	ctx, cancel := context.WithTimeout(t.Context(), 20*time.Millisecond)
	defer cancel()
	(&desktopExecutionWorker{store: store, server: server}).Run(ctx)
	store.mu.Lock()
	claims := store.claims
	store.mu.Unlock()
	if claims != 2 {
		t.Fatalf("claims = %d, want initial claim plus buffered wake claim", claims)
	}
}

func TestExecutionWorkerRetriesTransientClaimFailureWithoutPollingNoWork(t *testing.T) {
	attempt := 0
	store := &executionStoreFake{
		ready: make(chan struct{}, 1), retryable: true,
		claim: func() (*central.ExecutionCommand, error) {
			attempt++
			if attempt == 1 {
				return nil, errors.New("temporary transport failure")
			}
			return nil, nil
		},
	}
	server := &executionServerFake{available: make(chan struct{}, 1)}
	ctx, cancel := context.WithTimeout(t.Context(), 20*time.Millisecond)
	defer cancel()
	worker := &desktopExecutionWorker{
		store: store, server: server, retryDelay: func(int) time.Duration { return time.Millisecond },
	}
	worker.Run(ctx)
	store.mu.Lock()
	claims := store.claims
	store.mu.Unlock()
	if claims != 2 {
		t.Fatalf("claims = %d, want one transient retry followed by an event wait", claims)
	}
}

func TestExecutionWorkerReturnsTerminalClaimFailure(t *testing.T) {
	store := &executionStoreFake{
		ready: make(chan struct{}, 1),
		claim: func() (*central.ExecutionCommand, error) {
			return nil, errors.New("authentication rejected")
		},
	}
	worker := &desktopExecutionWorker{store: store, server: &executionServerFake{}}
	if err := worker.Run(t.Context()); err == nil {
		t.Fatal("terminal claim failure did not stop the execution supervisor")
	}
	store.mu.Lock()
	claims := store.claims
	store.mu.Unlock()
	if claims != 1 {
		t.Fatalf("terminal failure claims = %d, want no retry", claims)
	}
}

func TestExecutionWorkerRecoversFromTransientDeadline(t *testing.T) {
	attempt := 0
	store := &executionStoreFake{
		ready: make(chan struct{}, 1), retryable: true,
		claim: func() (*central.ExecutionCommand, error) {
			attempt++
			if attempt == 1 {
				return nil, context.DeadlineExceeded
			}
			return nil, nil
		},
	}
	ctx, cancel := context.WithTimeout(t.Context(), 20*time.Millisecond)
	defer cancel()
	worker := &desktopExecutionWorker{
		store: store, server: &executionServerFake{},
		retryDelay: func(int) time.Duration { return time.Millisecond },
	}
	if err := worker.Run(ctx); err != nil {
		t.Fatal(err)
	}
	store.mu.Lock()
	claims := store.claims
	store.mu.Unlock()
	if claims != 2 {
		t.Fatalf("deadline recovery claims = %d, want one retry then event wait", claims)
	}
}

func TestExecutionWorkerDoesNotRetryAfterParentCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	store := &executionStoreFake{
		ready: make(chan struct{}, 1), retryable: true,
		claim: func() (*central.ExecutionCommand, error) {
			cancel()
			return nil, context.Canceled
		},
	}
	worker := &desktopExecutionWorker{store: store, server: &executionServerFake{}}
	if err := worker.Run(ctx); err != nil {
		t.Fatal(err)
	}
	store.mu.Lock()
	claims := store.claims
	store.mu.Unlock()
	if claims != 1 {
		t.Fatalf("parent cancellation claims = %d, want no retry", claims)
	}
}

func TestExecutionHeartbeatIntervalUsesRemainingLease(t *testing.T) {
	now := time.Date(2026, time.August, 12, 0, 0, 0, 0, time.UTC)
	if got := executionHeartbeatInterval(now.Add(30*time.Second), now); got != 10*time.Second {
		t.Fatalf("executionHeartbeatInterval() = %s, want 10s", got)
	}
	if got := executionHeartbeatInterval(now.Add(2*time.Second), now); got != time.Second {
		t.Fatalf("short executionHeartbeatInterval() = %s, want 1s", got)
	}
	if got := executionHeartbeatInterval(now.Add(-time.Second), now); got != time.Second {
		t.Fatalf("expired executionHeartbeatInterval() = %s, want 1s", got)
	}
}

func TestExecutionLeaseHeartbeatFailureCancelsPreparationAndReportsFailure(t *testing.T) {
	store := &executionStoreFake{
		heartbeatErr: errors.New("lease rejected"), completed: make(chan central.ExecutionResultRequest, 1),
	}
	cancelled := make(chan struct{})
	server := &executionServerFake{run: func(ctx context.Context) error {
		<-ctx.Done()
		close(cancelled)
		return ctx.Err()
	}}
	worker := desktopExecutionWorker{store: store, server: server, userID: "user"}
	started := time.Now()
	worker.execute(t.Context(), validExecutionCommand(time.Now().Add(time.Minute)))
	select {
	case <-cancelled:
	case <-time.After(100 * time.Millisecond):
		t.Fatal("heartbeat failure did not cancel browser preparation")
	}
	if elapsed := time.Since(started); elapsed > 1500*time.Millisecond {
		t.Fatalf("heartbeat failure cancellation took %s", elapsed)
	}
	result := <-store.completed
	if result.Status != "failed" || result.ReasonCode != "execution_lease_lost" {
		t.Fatalf("completion = %+v", result)
	}
}

func TestExecutionUsesExactCommandShowtime(t *testing.T) {
	store := &executionStoreFake{completed: make(chan central.ExecutionResultRequest, 1)}
	server := &executionServerFake{}
	worker := desktopExecutionWorker{store: store, server: server, userID: "user"}
	command := validExecutionCommand(time.Now().Add(time.Minute))
	worker.execute(t.Context(), command)
	server.mu.Lock()
	showtime := server.showtime
	server.mu.Unlock()
	if showtime.ID != command.Payload.Showtime.ID || showtime.Movie != "영화" ||
		showtime.AuditoriumID != "auditorium" || showtime.Date != "2026-08-20" ||
		showtime.StartsAt != "20:00" || showtime.EndsAt != "22:00" {
		t.Fatalf("executed showtime = %+v", showtime)
	}
	if result := <-store.completed; result.Status != "completed" {
		t.Fatalf("completion = %+v", result)
	}
}

func TestExecutionRejectsInvalidCommandPayloadBeforeBrowser(t *testing.T) {
	store := &executionStoreFake{completed: make(chan central.ExecutionResultRequest, 1)}
	called := false
	server := &executionServerFake{run: func(context.Context) error { called = true; return nil }}
	command := validExecutionCommand(time.Now().Add(time.Minute))
	command.Payload.Showtime.ID = ""
	(&desktopExecutionWorker{store: store, server: server, userID: "user"}).execute(t.Context(), command)
	if called {
		t.Fatal("invalid command opened browser preparation")
	}
	if result := <-store.completed; result.Status != "failed" || result.ReasonCode != "invalid_execution_payload" {
		t.Fatalf("completion = %+v", result)
	}
}

func validExecutionCommand(expiresAt time.Time) central.ExecutionCommand {
	location := time.FixedZone("KST", 9*60*60)
	return central.ExecutionCommand{
		ID: "execution", MonitorID: "monitor", LeaseToken: "lease", LeaseExpiresAt: expiresAt,
		Payload: central.ExecutionPayload{
			ObservedAt: time.Date(2026, 8, 12, 19, 59, 0, 0, location),
			Showtime: central.Showtime{
				ID: "showtime", ProviderID: central.ProviderCGV, SourceKey: "source", TheaterID: "theater",
				Movie:          central.Movie{Title: "영화"},
				Auditorium:     central.Auditorium{ID: "auditorium", Name: "IMAX"},
				StartsAt:       time.Date(2026, 8, 20, 20, 0, 0, 0, location),
				EndsAt:         time.Date(2026, 8, 20, 22, 0, 0, 0, location),
				AvailableSeats: 10, Capacity: 100,
			},
		},
	}
}
