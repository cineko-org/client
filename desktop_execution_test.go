package main

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/cineko-org/client/internal/adapters/cgv"
	"github.com/cineko-org/client/internal/application"
	catalogpb "github.com/cineko-org/contracts/v3/gen/go/cineko/catalog"
	clientpb "github.com/cineko-org/contracts/v3/gen/go/cineko/client"
	commonpb "github.com/cineko-org/contracts/v3/gen/go/cineko/common"
	executionpb "github.com/cineko-org/contracts/v3/gen/go/cineko/execution"
	"google.golang.org/protobuf/types/known/timestamppb"
)

type executionStoreFake struct {
	mu           sync.Mutex
	heartbeatErr error
	heartbeat    func(context.Context) (*executionpb.HeartbeatResponse, error)
	claim        func() (*executionpb.Command, error)
	claims       int
	ready        chan struct{}
	retryable    bool
	completed    chan *executionpb.ResultRequest
}

func (store *executionStoreFake) ClaimExecution(context.Context, string) (*executionpb.Command, error) {
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
) (*executionpb.HeartbeatResponse, error) {
	if store.heartbeat != nil {
		return store.heartbeat(ctx)
	}
	if store.heartbeatErr != nil {
		return nil, store.heartbeatErr
	}
	return executionpb.HeartbeatResponse_builder{LeaseExpiresAt: timestamppb.New(time.Now().Add(time.Minute))}.Build(), nil
}

func TestCompletedPreparationDoesNotMistakeOwnedHeartbeatCancellationForFenceLoss(t *testing.T) {
	store := &executionStoreFake{
		completed: make(chan *executionpb.ResultRequest, 1),
		heartbeat: func(ctx context.Context) (*executionpb.HeartbeatResponse, error) {
			<-ctx.Done()
			return nil, ctx.Err()
		},
	}
	server := &executionServerFake{}
	(&desktopExecutionWorker{store: store, server: server, userID: "user"}).execute(
		t.Context(), validExecutionCommand(time.Now().Add(time.Minute)),
	)
	if result := <-store.completed; result.GetCompleted() == nil || result.GetFailed() != nil {
		t.Fatalf("completion = %s", result)
	}
}

func (store *executionStoreFake) CompleteExecution(
	_ context.Context,
	_ string,
	result *executionpb.ResultRequest,
) error {
	store.completed <- result
	return nil
}

type executionServerFake struct {
	mu       sync.Mutex
	showtime *catalogpb.Showtime
	run      func(context.Context) error
}

func (server *executionServerFake) ExecuteAvailability(
	ctx context.Context,
	_ string,
	showtime *catalogpb.Showtime,
) error {
	server.mu.Lock()
	server.showtime = showtime
	server.mu.Unlock()
	if server.run != nil {
		return server.run(ctx)
	}
	return nil
}

func (*executionServerFake) RecordLocalSystemEvent(*clientpb.AppEvent) {}
func (*executionServerFake) CanAcceptExecution() bool                  { return true }
func (*executionServerFake) ExecutionAvailable() <-chan struct{}       { return nil }

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
	store.claim = func() (*executionpb.Command, error) {
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
	store := &executionStoreFake{retryable: false, claim: func() (*executionpb.Command, error) {
		return nil, errors.New("unauthorized")
	}}
	worker := &desktopExecutionWorker{store: store, server: &executionServerFake{}}
	if err := worker.Run(t.Context()); err == nil {
		t.Fatal("Run() succeeded for terminal claim failure")
	}
}

func TestExecutionWorkerCancellationDoesNotRetry(t *testing.T) {
	store := &executionStoreFake{retryable: true, claim: func() (*executionpb.Command, error) {
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
		heartbeatErr: errors.New("lease rejected"), completed: make(chan *executionpb.ResultRequest, 1),
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
	if result.GetFailed() == nil || result.GetFailed().GetReasonCode() != "execution_lease_lost" {
		t.Fatalf("completion = %s", result)
	}
}

func TestExecutionUsesExactCommandShowtime(t *testing.T) {
	store := &executionStoreFake{completed: make(chan *executionpb.ResultRequest, 1)}
	server := &executionServerFake{}
	worker := desktopExecutionWorker{store: store, server: server, userID: "user"}
	command := validExecutionCommand(time.Now().Add(time.Minute))
	worker.execute(t.Context(), command)
	server.mu.Lock()
	showtime := server.showtime
	server.mu.Unlock()
	if showtime.GetId() != command.GetPayload().GetShowtime().GetId() || showtime.GetMovie().GetTitle() != "영화" ||
		showtime.GetAuditorium().GetId() != "auditorium" || !executionIdentityEquals(showtime.GetIdentity().GetCgv(), "0056", "2026-08-20", "0007", "0003") ||
		showtime.GetStartsAt().AsTime() != command.GetPayload().GetShowtime().GetStartsAt().AsTime() ||
		showtime.GetEndsAt().AsTime() != command.GetPayload().GetShowtime().GetEndsAt().AsTime() {
		t.Fatalf("executed showtime = %+v", showtime)
	}
	if result := <-store.completed; result.GetCompleted() == nil {
		t.Fatalf("completion = %+v", result)
	}
}

func TestExecutionPreservesProviderDateForAfterMidnightShowtime(t *testing.T) {
	location := time.FixedZone("KST", 9*60*60)
	payload := executionpb.Payload_builder{
		ObservedAt: timestamppb.New(time.Date(2026, 8, 20, 23, 0, 0, 0, location)),
		Showtime: catalogpb.Showtime_builder{
			Id: stringPointer("showtime"), ProviderId: stringPointer("cgv"), Identity: executionShowtimeIdentity(), TheaterId: stringPointer("theater"),
			Movie: catalogpb.Movie_builder{
				Id: stringPointer("movie_1"), ProviderId: stringPointer("cgv"), Identity: executionMovieIdentity(), Title: stringPointer("영화"),
			}.Build(),
			Auditorium: catalogpb.Auditorium_builder{
				Id: stringPointer("auditorium"), TheaterId: stringPointer("theater"), Identity: executionAuditoriumIdentity(), Name: stringPointer("IMAX"),
			}.Build(),
			StartsAt: timestamppb.New(time.Date(2026, 8, 21, 1, 30, 0, 0, location)), EndsAt: timestamppb.New(time.Date(2026, 8, 21, 4, 32, 0, 0, location)),
			AvailableSeats: int32Pointer(2), Capacity: int32Pointer(100),
		}.Build(),
	}.Build()
	showtime, err := executionShowtime(payload)
	if err != nil {
		t.Fatal(err)
	}
	if !executionIdentityEquals(showtime.GetIdentity().GetCgv(), "0056", "2026-08-20", "0007", "0003") ||
		showtime.GetStartsAt().AsTime().In(location).Format(time.DateOnly) != "2026-08-21" ||
		showtime.GetStartsAt().AsTime().In(location).Format("15:04") != "01:30" ||
		showtime.GetEndsAt().AsTime().In(location).Format("15:04") != "04:32" {
		t.Fatalf("execution showtime = %+v", showtime)
	}
}

func TestExecutionRejectsInvalidCommandPayloadBeforeBrowser(t *testing.T) {
	store := &executionStoreFake{completed: make(chan *executionpb.ResultRequest, 1)}
	called := false
	server := &executionServerFake{run: func(context.Context) error { called = true; return nil }}
	command := validExecutionCommand(time.Now().Add(time.Minute))
	command.GetPayload().GetShowtime().SetId("")
	(&desktopExecutionWorker{store: store, server: server, userID: "user"}).execute(t.Context(), command)
	if called {
		t.Fatal("invalid command opened browser preparation")
	}
	if result := <-store.completed; result.GetFailed() == nil || result.GetFailed().GetReasonCode() != "invalid_execution_payload" {
		t.Fatalf("completion = %s", result)
	}
}

func TestExecutionFailureCodeSeparatesAvailabilityFromTransientFailures(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want string
	}{
		{name: "preferred seats", err: application.ErrSeatUnavailable, want: executionReasonPreferredSeatsUnavailable},
		{name: "showtime", err: application.ErrBookingNotOpen, want: executionReasonShowtimeUnavailable},
		{name: "authentication", err: cgv.ErrAuthenticationRequired, want: executionReasonAuthenticationRequired},
		{name: "captcha", err: cgv.ErrCaptchaRequired, want: executionReasonCaptchaRequired},
		{name: "provider contract", err: cgv.ErrUIContractChanged, want: executionReasonProviderContractChanged},
		{name: "provider access blocked", err: cgv.ErrProviderAccessBlocked, want: executionReasonProviderAccessBlocked},
		{name: "provider throttled", err: cgv.ErrProviderThrottled, want: executionReasonProviderThrottled},
		{name: "interrupted", err: context.Canceled, want: executionReasonClientInterrupted},
		{name: "transient", err: errors.New("browser failed"), want: executionReasonBookingPreparationFailed},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := executionFailureCode(test.err); got != test.want {
				t.Fatalf("executionFailureCode() = %q, want %q", got, test.want)
			}
		})
	}
}

func TestExecutionFailureOutcomeUsesRetryOnlyForGenericPreparationFailure(t *testing.T) {
	tests := []struct {
		name  string
		err   error
		retry bool
		want  string
	}{
		{name: "generic preparation", err: errors.New("browser failed"), retry: true, want: executionReasonBookingPreparationFailed},
		{name: "preferred seats", err: application.ErrSeatUnavailable, want: executionReasonPreferredSeatsUnavailable},
		{name: "showtime", err: application.ErrBookingNotOpen, want: executionReasonShowtimeUnavailable},
		{name: "authentication", err: cgv.ErrAuthenticationRequired, want: executionReasonAuthenticationRequired},
		{name: "captcha", err: cgv.ErrCaptchaRequired, want: executionReasonCaptchaRequired},
		{name: "provider contract", err: cgv.ErrUIContractChanged, want: executionReasonProviderContractChanged},
		{name: "provider access blocked", err: cgv.ErrProviderAccessBlocked, want: executionReasonProviderAccessBlocked},
		{name: "provider throttled", err: cgv.ErrProviderThrottled, want: executionReasonProviderThrottled},
		{name: "interrupted", err: context.Canceled, want: executionReasonClientInterrupted},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			store := &executionStoreFake{completed: make(chan *executionpb.ResultRequest, 1)}
			server := &executionServerFake{run: func(context.Context) error {
				return fmt.Errorf("execution failed: %w", test.err)
			}}
			(&desktopExecutionWorker{store: store, server: server, userID: "user"}).execute(
				t.Context(), validExecutionCommand(time.Now().Add(time.Minute)),
			)
			result := <-store.completed
			if test.retry {
				if result.GetRetryRequested() == nil || result.GetFailed() != nil ||
					result.GetRetryRequested().GetReasonCode() != test.want {
					t.Fatalf("retry outcome = %s, want retry_requested(%q)", result, test.want)
				}
				return
			}
			if result.GetFailed() == nil || result.GetRetryRequested() != nil || result.GetFailed().GetReasonCode() != test.want {
				t.Fatalf("failed outcome = %s, want failed(%q)", result, test.want)
			}
		})
	}
}

func validExecutionCommand(expiresAt time.Time) *executionpb.Command {
	location := time.FixedZone("KST", 9*60*60)
	return executionpb.Command_builder{
		Id: stringPointer("execution"), MonitorId: stringPointer("monitor"), LeaseToken: stringPointer("lease"), LeaseExpiresAt: timestamppb.New(expiresAt),
		Payload: executionpb.Payload_builder{
			ObservedAt: timestamppb.New(time.Date(2026, 8, 12, 19, 59, 0, 0, location)),
			Showtime: catalogpb.Showtime_builder{
				Id: stringPointer("showtime"), ProviderId: stringPointer("cgv"), Identity: executionShowtimeIdentity(), TheaterId: stringPointer("theater"),
				Movie: catalogpb.Movie_builder{
					Id: stringPointer("movie_1"), ProviderId: stringPointer("cgv"), Identity: executionMovieIdentity(), Title: stringPointer("영화"),
				}.Build(),
				Auditorium: catalogpb.Auditorium_builder{
					Id: stringPointer("auditorium"), TheaterId: stringPointer("theater"), Identity: executionAuditoriumIdentity(), Name: stringPointer("IMAX"),
				}.Build(),
				StartsAt: timestamppb.New(time.Date(2026, 8, 20, 20, 0, 0, 0, location)), EndsAt: timestamppb.New(time.Date(2026, 8, 20, 22, 0, 0, 0, location)),
				AvailableSeats: int32Pointer(10), Capacity: int32Pointer(100),
			}.Build(),
		}.Build(),
	}.Build()
}

func executionShowtimeIdentity() *catalogpb.ShowtimeIdentity {
	siteNo, screenNo, sequence := "0056", "0007", "0003"
	return catalogpb.ShowtimeIdentity_builder{Cgv: catalogpb.CgvShowtimeIdentity_builder{
		SiteNo: &siteNo,
		ScheduleDate: commonpb.LocalDate_builder{
			Year: int32Pointer(2026), Month: int32Pointer(8), Day: int32Pointer(20),
		}.Build(),
		ScreenNo: &screenNo,
		Sequence: &sequence,
	}.Build()}.Build()
}

func executionMovieIdentity() *catalogpb.MovieIdentity {
	movieNo := "1"
	return catalogpb.MovieIdentity_builder{Cgv: catalogpb.CgvMovieIdentity_builder{MovieNo: &movieNo}.Build()}.Build()
}

func executionAuditoriumIdentity() *catalogpb.AuditoriumIdentity {
	siteNo, screenNo := "0056", "0007"
	return catalogpb.AuditoriumIdentity_builder{Cgv: catalogpb.CgvAuditoriumIdentity_builder{
		SiteNo: &siteNo, ScreenNo: &screenNo,
	}.Build()}.Build()
}

func executionIdentityEquals(identity *catalogpb.CgvShowtimeIdentity, siteNo, date, screenNo, sequence string) bool {
	if identity == nil || identity.GetScheduleDate() == nil {
		return false
	}
	scheduleDate := identity.GetScheduleDate()
	actualDate := fmt.Sprintf("%04d-%02d-%02d", scheduleDate.GetYear(), scheduleDate.GetMonth(), scheduleDate.GetDay())
	return identity.GetSiteNo() == siteNo && actualDate == date && identity.GetScreenNo() == screenNo && identity.GetSequence() == sequence
}

func stringPointer(value string) *string { return &value }
func int32Pointer(value int32) *int32    { return &value }
