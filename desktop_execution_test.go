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
	claim        func() (*central.ExecutionCommand, error)
	claims       int
	ready        chan struct{}
	retryable    bool
	completed    chan central.ExecutionResultRequest
}

func (store *executionStoreFake) ClaimExecution(context.Context, string) (*central.ExecutionCommand, error) {
	store.mu.Lock()
	store.claims++
	claim := store.claim
	store.mu.Unlock()
	if claim != nil {
		return claim()
	}
	return nil, nil
}

func (store *executionStoreFake) ExecutionReady() <-chan struct{} {
	store.mu.Lock()
	defer store.mu.Unlock()
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
	mu       sync.Mutex
	showtime domain.Showtime
	run      func(context.Context) error
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
func (*executionServerFake) CanAcceptExecution() bool                                        { return true }
func (*executionServerFake) ExecutionAvailable() <-chan struct{}                             { return nil }

func (store *executionStoreFake) claimCount() int {
	store.mu.Lock()
	defer store.mu.Unlock()
	return store.claims
}

func TestExecutionWorkerWaitsForDurableReadySignal(t *testing.T) {
	store := &executionStoreFake{ready: make(chan struct{}, 1)}
	worker := &desktopExecutionWorker{store: store, server: &executionServerFake{}}
	ctx, cancel := context.WithTimeout(t.Context(), 20*time.Millisecond)
	defer cancel()
	if err := worker.Run(ctx); err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if got := store.claimCount(); got != 1 {
		t.Fatalf("claim count = %d, want initial claim only", got)
	}
}

func TestExecutionWorkerRetriesTransientClaimFailureWithBoundedDelay(t *testing.T) {
	store := &executionStoreFake{ready: make(chan struct{}, 1), retryable: true}
	var calls int
	store.claim = func() (*central.ExecutionCommand, error) {
		calls++
		if calls < 3 {
			return nil, errors.New("temporary central failure")
		}
		return nil, nil
	}
	worker := &desktopExecutionWorker{
		store: store, server: &executionServerFake{},
		retryDelay: func(int) time.Duration { return time.Millisecond },
	}
	ctx, cancel := context.WithTimeout(t.Context(), 20*time.Millisecond)
	defer cancel()
	if err := worker.Run(ctx); err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if got := store.claimCount(); got < 3 {
		t.Fatalf("claim count = %d, want at least 3", got)
	}
}

func TestExecutionWorkerStopsOnTerminalClaimFailure(t *testing.T) {
	store := &executionStoreFake{retryable: false, claim: func() (*central.ExecutionCommand, error) {
		return nil, errors.New("unauthorized")
	}}
	worker := &desktopExecutionWorker{store: store, server: &executionServerFake{}}
	if err := worker.Run(t.Context()); err == nil {
		t.Fatal("Run() succeeded for terminal claim failure")
	}
}

func TestExecutionWorkerCancellationDoesNotRetry(t *testing.T) {
	store := &executionStoreFake{retryable: true, claim: func() (*central.ExecutionCommand, error) {
		return nil, context.Canceled
	}}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	worker := &desktopExecutionWorker{store: store, server: &executionServerFake{}}
	if err := worker.Run(ctx); err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if got := store.claimCount(); got != 0 {
		t.Fatalf("claim count = %d, want 0 after cancellation", got)
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
		showtime.CivilDate != "2026-08-20" || showtime.StartsAt != "20:00" || showtime.EndsAt != "22:00" {
		t.Fatalf("executed showtime = %+v", showtime)
	}
	if result := <-store.completed; result.Status != "completed" {
		t.Fatalf("completion = %+v", result)
	}
}

func TestExecutionPreservesProviderDateForAfterMidnightShowtime(t *testing.T) {
	location := time.FixedZone("KST", 9*60*60)
	payload := central.ExecutionPayload{
		ObservedAt: time.Date(2026, 8, 20, 23, 0, 0, 0, location),
		Showtime: central.Showtime{
			ID: "showtime", ProviderID: central.ProviderCGV, SourceKey: "0056/2026-08-20/0007/0003", TheaterID: "theater",
			Movie:      central.Movie{ID: "movie_1", Title: "영화"},
			Auditorium: central.Auditorium{ID: "auditorium", Name: "IMAX"},
			StartsAt:   time.Date(2026, 8, 21, 1, 30, 0, 0, location),
			EndsAt:     time.Date(2026, 8, 21, 4, 32, 0, 0, location), AvailableSeats: 2, Capacity: 100,
		},
	}
	showtime, err := executionShowtime(payload)
	if err != nil {
		t.Fatal(err)
	}
	if showtime.Date != "2026-08-20" || showtime.CivilDate != "2026-08-21" || showtime.StartsAt != "01:30" || showtime.EndsAt != "04:32" {
		t.Fatalf("execution showtime = %+v", showtime)
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
				ID: "showtime", ProviderID: central.ProviderCGV, SourceKey: "0056/2026-08-20/0007/0003", TheaterID: "theater",
				Movie:          central.Movie{ID: "movie_1", Title: "영화"},
				Auditorium:     central.Auditorium{ID: "auditorium", Name: "IMAX"},
				StartsAt:       time.Date(2026, 8, 20, 20, 0, 0, 0, location),
				EndsAt:         time.Date(2026, 8, 20, 22, 0, 0, 0, location),
				AvailableSeats: 10, Capacity: 100,
			},
		},
	}
}
