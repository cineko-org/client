package main

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	centralstore "github.com/cineko-org/client/internal/adapters/storage/centralhttp"
	"github.com/cineko-org/client/internal/interfaces/webui"
	clientpb "github.com/cineko-org/contracts/gen/go/cineko/client"
	commonpb "github.com/cineko-org/contracts/gen/go/cineko/common"
	observationpb "github.com/cineko-org/contracts/gen/go/cineko/observation"
	probepb "github.com/cineko-org/contracts/gen/go/cineko/probe"
	servicepb "github.com/cineko-org/contracts/gen/go/cineko/service"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"
)

func TestEmbeddedProbeRejectsIncompleteRuntimeIdentity(t *testing.T) {
	if _, err := startEmbeddedProbe(
		context.Background(), nil, t.TempDir(), nil,
	); err == nil {
		t.Fatal("incomplete embedded Probe runtime identity accepted")
	}
}

func TestEmbeddedProbeWaitsForRuntimeReadiness(t *testing.T) {
	runtime := newFakeEmbeddedProbeRuntime()
	started := make(chan struct{})
	result := make(chan error, 1)
	var embedded *embeddedProbe
	go func() {
		close(started)
		var err error
		embedded, err = startEmbeddedProbeRuntime(t.Context(), runtime, time.Second)
		result <- err
	}()
	<-started
	select {
	case err := <-result:
		t.Fatalf("startup completed before Central readiness: %v", err)
	case <-time.After(20 * time.Millisecond):
	}
	runtime.ready <- nil
	if err := <-result; err != nil {
		t.Fatal(err)
	}
	if err := embedded.Close(); err != nil {
		t.Fatal(err)
	}
	select {
	case <-runtime.stopped:
	case <-time.After(time.Second):
		t.Fatal("embedded Probe runtime did not stop")
	}
}

func TestEmbeddedProbeStartupTimeoutStopsRuntime(t *testing.T) {
	runtime := newFakeEmbeddedProbeRuntime()
	if _, err := startEmbeddedProbeRuntime(t.Context(), runtime, time.Millisecond); err == nil ||
		err.Error() != "embedded Probe startup timed out" {
		t.Fatalf("startup timeout error = %v", err)
	}
	select {
	case <-runtime.stopped:
	case <-time.After(time.Second):
		t.Fatal("timed-out embedded Probe runtime did not stop")
	}
}

func TestEmbeddedProbeBookingDrainIsReferenceCounted(t *testing.T) {
	runtime := newFakeEmbeddedProbeRuntime()
	runtime.ready <- nil
	embedded, err := startEmbeddedProbeRuntime(t.Context(), runtime, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	firstDone, err := embedded.beginBooking()
	if err != nil {
		t.Fatal(err)
	}
	secondDone, err := embedded.beginBooking()
	if err != nil {
		t.Fatal(err)
	}
	firstDone()
	firstDone()
	secondDone()

	if got := runtime.drainStates(); len(got) != 2 || !got[0] || got[1] {
		t.Fatalf("booking drain states = %v, want [true false]", got)
	}
	if err := embedded.Close(); err != nil {
		t.Fatal(err)
	}
	if err := embedded.Close(); err != nil {
		t.Fatalf("idempotent Close() = %v", err)
	}
	if got := runtime.drainStates(); len(got) != 3 || !got[2] {
		t.Fatalf("shutdown drain states = %v, want final true", got)
	}
}

func TestEmbeddedProbeShutdownNeverClearsDrain(t *testing.T) {
	runtime := newFakeEmbeddedProbeRuntime()
	runtime.ready <- nil
	embedded, err := startEmbeddedProbeRuntime(t.Context(), runtime, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	bookingDone, err := embedded.beginBooking()
	if err != nil {
		t.Fatal(err)
	}
	if err := embedded.Close(); err != nil {
		t.Fatal(err)
	}
	bookingDone()
	if got := runtime.drainStates(); len(got) != 2 || !got[0] || !got[1] {
		t.Fatalf("shutdown drain states = %v, want [true true]", got)
	}
	if _, err := embedded.beginBooking(); err == nil {
		t.Fatal("booking accepted after embedded Probe shutdown")
	}
}

func TestEmbeddedProbeBookingAutomationOwnsDrainLifecycle(t *testing.T) {
	runtime := newFakeEmbeddedProbeRuntime()
	runtime.ready <- nil
	embedded, err := startEmbeddedProbeRuntime(t.Context(), runtime, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	automation := &closeTrackingAutomation{}
	wrapped, err := embedded.OpenBooking(func() (webui.Automation, error) {
		if got := runtime.drainStates(); len(got) != 1 || !got[0] {
			t.Fatalf("drain before browser open = %v, want [true]", got)
		}
		return automation, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	wrapped.Close()
	wrapped.Close()
	if automation.closes != 1 {
		t.Fatalf("booking browser closes = %d, want 1", automation.closes)
	}
	if got := runtime.drainStates(); len(got) != 2 || got[1] {
		t.Fatalf("drain after browser close = %v, want final false", got)
	}
	if err := embedded.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestEmbeddedProbeBookingOpenFailureReleasesDrain(t *testing.T) {
	runtime := newFakeEmbeddedProbeRuntime()
	runtime.ready <- nil
	embedded, err := startEmbeddedProbeRuntime(t.Context(), runtime, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	errOpen := errors.New("open booking browser")
	if _, err := embedded.OpenBooking(func() (webui.Automation, error) {
		return nil, errOpen
	}); !errors.Is(err, errOpen) {
		t.Fatalf("OpenBooking() error = %v, want %v", err, errOpen)
	}
	if got := runtime.drainStates(); len(got) != 2 || !got[0] || got[1] {
		t.Fatalf("failed booking drain states = %v, want [true false]", got)
	}
	if err := embedded.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestEmbeddedProbeBookingOpenFinishingDuringShutdownIsClosed(t *testing.T) {
	runtime := newFakeEmbeddedProbeRuntime()
	runtime.ready <- nil
	embedded, err := startEmbeddedProbeRuntime(t.Context(), runtime, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	openStarted := make(chan struct{})
	finishOpen := make(chan struct{})
	result := make(chan error, 1)
	automation := &closeTrackingAutomation{}
	go func() {
		_, openErr := embedded.OpenBooking(func() (webui.Automation, error) {
			close(openStarted)
			<-finishOpen
			return automation, nil
		})
		result <- openErr
	}()
	<-openStarted
	if err := embedded.Close(); err != nil {
		t.Fatal(err)
	}
	close(finishOpen)
	if err := <-result; err == nil {
		t.Fatal("booking browser opened after embedded Probe shutdown")
	}
	if automation.closes != 1 {
		t.Fatalf("shutdown-raced booking browser closes = %d, want 1", automation.closes)
	}
	if got := runtime.drainStates(); len(got) != 2 || !got[0] || !got[1] {
		t.Fatalf("shutdown-raced drain states = %v, want [true true]", got)
	}
}

func TestEmbeddedProbeRejectsMissingBookingOpener(t *testing.T) {
	runtime := newFakeEmbeddedProbeRuntime()
	runtime.ready <- nil
	embedded, err := startEmbeddedProbeRuntime(t.Context(), runtime, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := embedded.OpenBooking(nil); err == nil {
		t.Fatal("missing booking browser opener accepted")
	}
	if got := runtime.drainStates(); len(got) != 0 {
		t.Fatalf("missing opener changed drain state: %v", got)
	}
	if err := embedded.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestEmbeddedProbeReadinessFailureStopsRuntime(t *testing.T) {
	runtime := newFakeEmbeddedProbeRuntime()
	errReady := errors.New("initial heartbeat failed")
	runtime.ready <- errReady
	if _, err := startEmbeddedProbeRuntime(t.Context(), runtime, time.Second); !errors.Is(err, errReady) {
		t.Fatalf("startup error = %v, want %v", err, errReady)
	}
	select {
	case <-runtime.stopped:
	case <-time.After(time.Second):
		t.Fatal("failed embedded Probe runtime did not stop")
	}
}

func TestEmbeddedProbeSurfacesFailureAfterReadiness(t *testing.T) {
	runtime := newFakeEmbeddedProbeRuntime()
	runtime.ready <- nil
	embedded, err := startEmbeddedProbeRuntime(t.Context(), runtime, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	errRuntime := errors.New("Probe session failed")
	runtime.exit <- errRuntime
	select {
	case err := <-embedded.Failure():
		if !errors.Is(err, errRuntime) {
			t.Fatalf("runtime failure = %v, want %v", err, errRuntime)
		}
	case <-time.After(time.Second):
		t.Fatal("post-readiness Probe failure was not surfaced")
	}
	if err := embedded.Close(); !errors.Is(err, errRuntime) {
		t.Fatalf("Close() = %v, want %v", err, errRuntime)
	}
}

func TestSuperviseEmbeddedProbeStopsWithContext(t *testing.T) {
	runtime := newFakeEmbeddedProbeRuntime()
	runtime.ready <- nil
	embedded, err := startEmbeddedProbeRuntime(t.Context(), runtime, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	done := make(chan struct{})
	go func() {
		superviseEmbeddedProbe(ctx, embedded, func(error) { t.Error("unexpected Probe failure") })
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("Probe supervision did not stop with context")
	}
	if err := embedded.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestSuperviseEmbeddedProbeForwardsRuntimeFailure(t *testing.T) {
	runtime := newFakeEmbeddedProbeRuntime()
	runtime.ready <- nil
	embedded, err := startEmbeddedProbeRuntime(t.Context(), runtime, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	failures := make(chan error, 1)
	go superviseEmbeddedProbe(t.Context(), embedded, func(err error) { failures <- err })
	errRuntime := errors.New("Probe stopped")
	runtime.exit <- errRuntime
	select {
	case err := <-failures:
		if !errors.Is(err, errRuntime) {
			t.Fatalf("supervised failure = %v, want %v", err, errRuntime)
		}
	case <-time.After(time.Second):
		t.Fatal("supervisor did not forward Probe failure")
	}
	if err := embedded.Close(); !errors.Is(err, errRuntime) {
		t.Fatalf("Close() = %v, want %v", err, errRuntime)
	}
}

func TestClientProbeCredentialSource(t *testing.T) {
	now := time.Now().UTC()
	expiresAt := now.Add(time.Minute)
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("X-Cineko-Release-Generation", "17")
		switch request.URL.Path {
		case "/v1/auth/exchange":
			authentication := clientpb.AuthenticationResponse_builder{
				AccessToken: stringPointer("access"), ExpiresAt: timestamppb.New(now.Add(time.Hour)),
				RefreshToken: stringPointer("refresh"), RefreshExpiresAt: timestamppb.New(now.Add(2 * time.Hour)),
				User: clientpb.User_builder{Id: stringPointer("user")}.Build(),
			}.Build()
			writeMainProto(t, writer, servicepb.ExchangeTokenResponse_builder{Authentication: authentication}.Build())
		case "/v1/probe-bootstrap-tickets":
			if request.Header.Get("Authorization") != "Bearer access" {
				t.Errorf("authorization = %q", request.Header.Get("Authorization"))
			}
			ticket := clientpb.ProbeBootstrapTicketResponse_builder{
				Ticket: stringPointer("ticket"), ExpiresAt: timestamppb.New(expiresAt),
			}.Build()
			writeMainProto(t, writer, servicepb.CreateProbeBootstrapTicketResponse_builder{Response: ticket}.Build())
		default:
			http.NotFound(writer, request)
		}
	}))
	t.Cleanup(server.Close)
	store, err := centralstore.Open(t.Context(), centralstore.Config{
		BaseURL: server.URL, UserID: "user", AccessToken: "credential", HTTPClient: server.Client(),
	})
	if err != nil {
		t.Fatal(err)
	}
	source := clientProbeCredentialSource{
		store: store, deviceID: "device",
		registration: probepb.RegisterRequest_builder{
			InstallationId: stringPointer("installation"),
			Kind:           probepb.ProbeKind_builder{Client: probepb.ClientProbe_builder{}.Build()}.Build(),
			Capabilities: []*observationpb.Capability{
				observationpb.Capability_builder{CatalogCapture: observationpb.CatalogCapture_builder{}.Build()}.Build(),
			},
			MaxConcurrency: int32Pointer(1),
			Runtime: commonpb.Runtime_builder{
				ComponentVersion: stringPointer("1.0.0"), BrowserRevision: stringPointer("1234"),
				Platform: stringPointer("test"), Architecture: stringPointer("test"),
			}.Build(),
		}.Build(),
	}
	if credential, err := source.Credential(t.Context()); err != nil || credential != "ticket" {
		t.Fatalf("Credential() = %q, %v", credential, err)
	}
	expiresAt = now.Add(-time.Minute)
	if _, err := source.Credential(t.Context()); err == nil {
		t.Fatal("expired bootstrap ticket accepted")
	}
	server.Close()
	if _, err := source.Credential(t.Context()); err == nil || errors.Is(err, context.Canceled) {
		t.Fatalf("Credential() transport error = %v", err)
	}
}

func writeMainProto(t *testing.T, writer http.ResponseWriter, message proto.Message) {
	t.Helper()
	writer.Header().Set("Content-Type", "application/json")
	encoded, err := (protojson.MarshalOptions{UseProtoNames: false}).Marshal(message)
	if err != nil {
		t.Errorf("encode protobuf fixture: %v", err)
		return
	}
	if _, err := writer.Write(encoded); err != nil {
		t.Errorf("write protobuf fixture: %v", err)
	}
}

func TestEnvironmentValue(t *testing.T) {
	t.Setenv("CINEKO_TEST_VALUE", "  configured  ")
	if got := environmentValue("CINEKO_TEST_VALUE", "fallback"); got != "configured" {
		t.Fatalf("environmentValue() = %q, want configured", got)
	}
	t.Setenv("CINEKO_TEST_VALUE", "  ")
	if got := environmentValue("CINEKO_TEST_VALUE", "fallback"); got != "fallback" {
		t.Fatalf("empty environmentValue() = %q, want fallback", got)
	}
}

type fakeEmbeddedProbeRuntime struct {
	ready   chan error
	exit    chan error
	stopped chan struct{}
	mu      sync.Mutex
	drains  []bool
}

type closeTrackingAutomation struct {
	webui.Automation
	closes int
}

func (automation *closeTrackingAutomation) Close() {
	automation.closes++
}

func newFakeEmbeddedProbeRuntime() *fakeEmbeddedProbeRuntime {
	return &fakeEmbeddedProbeRuntime{
		ready: make(chan error, 1), exit: make(chan error, 1), stopped: make(chan struct{}),
	}
}

func (runtime *fakeEmbeddedProbeRuntime) RunReady(ctx context.Context, ready chan<- error) error {
	var err error
	select {
	case err = <-runtime.ready:
	case <-ctx.Done():
		close(runtime.stopped)
		return ctx.Err()
	}
	ready <- err
	if err != nil {
		close(runtime.stopped)
		return err
	}
	select {
	case err = <-runtime.exit:
	case <-ctx.Done():
		err = nil
	}
	close(runtime.stopped)
	return err
}

func (runtime *fakeEmbeddedProbeRuntime) SetDraining(draining bool) {
	runtime.mu.Lock()
	runtime.drains = append(runtime.drains, draining)
	runtime.mu.Unlock()
}

func (runtime *fakeEmbeddedProbeRuntime) drainStates() []bool {
	runtime.mu.Lock()
	defer runtime.mu.Unlock()
	return append([]bool(nil), runtime.drains...)
}
