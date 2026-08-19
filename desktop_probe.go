package main

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/cineko-org/client/internal/adapters/browserfactory"
	centralstore "github.com/cineko-org/client/internal/adapters/storage/centralhttp"
	"github.com/cineko-org/client/internal/interfaces/webui"
	central "github.com/cineko-org/contracts/v3"
	"github.com/cineko-org/probe/v2/probe"
)

const (
	embeddedProbeStartupTimeout  = 30 * time.Second
	embeddedProbeShutdownTimeout = 10 * time.Second
)

type embeddedProbeRuntime interface {
	RunReady(context.Context, chan<- error) error
	SetDraining(bool)
}

// embeddedProbe owns the background runtime and coordinates local booking
// activity with Probe assignment availability.
type embeddedProbe struct {
	runtime embeddedProbeRuntime
	cancel  context.CancelFunc
	stopped chan struct{}
	failure chan error

	activityMu    sync.Mutex
	activeBooking int
	closing       bool
	runtimeErr    error
	shutdownOnce  sync.Once
	shutdownDone  chan struct{}
	shutdownErr   error
}

type probeDrainingAutomation struct {
	webui.Automation
	releaseOnce sync.Once
	release     func()
}

func (automation *probeDrainingAutomation) Close() {
	automation.releaseOnce.Do(func() {
		defer automation.release()
		automation.Automation.Close()
	})
}

type clientProbeCredentialSource struct {
	store        *centralstore.Store
	registration central.RegisterProbeRequest
	deviceID     string
}

func (source *clientProbeCredentialSource) Credential(ctx context.Context) (string, error) {
	response, err := source.store.IssueProbeBootstrapTicket(ctx, central.ProbeBootstrapTicketRequest{
		InstallationID: source.registration.InstallationID,
		DeviceID:       source.deviceID,
		Capabilities:   source.registration.Capabilities,
		MaxConcurrency: source.registration.MaxConcurrency,
		Runtime:        source.registration.Runtime,
	})
	if err != nil {
		return "", fmt.Errorf("issue embedded Probe bootstrap ticket: %w", err)
	}
	if response.Ticket == "" || !response.ExpiresAt.After(time.Now()) {
		return "", errors.New("central returned an invalid embedded Probe bootstrap ticket")
	}
	return response.Ticket, nil
}

func startEmbeddedProbe(
	parent context.Context,
	store *centralstore.Store,
	dataDir string,
	identity desktopRuntimeIdentity,
	browsers *browserfactory.Factory,
	capabilityState *seatMapCapabilityState,
) (*embeddedProbe, error) {
	if identity.InstallationID == "" || identity.DeviceID == "" || identity.ClientVersion == "" ||
		identity.BrowserRevision == "" || browsers == nil || capabilityState == nil {
		return nil, errors.New("embedded Probe runtime identity is incomplete")
	}
	registration := central.RegisterProbeRequest{
		InstallationID: identity.InstallationID,
		Kind:           "client",
		Capabilities: []string{
			central.CapabilityCGVCatalogCapture,
			central.CapabilityCGVScheduleCapture,
			central.CapabilityCGVSeatMapCapture,
		},
		MaxConcurrency: 1,
		Runtime: central.Runtime{
			Version: identity.ClientVersion, Protocol: central.ProtocolVersion,
			BrowserRevision: identity.BrowserRevision, Platform: runtime.GOOS, Arch: runtime.GOARCH,
		},
	}
	credentials, err := probe.NewClientCredentialSource(&clientProbeCredentialSource{
		store: store, registration: registration, deviceID: identity.DeviceID,
	}, probe.ClientCredentialConfig{
		PublicKeyFiles: strings.TrimSpace(os.Getenv("CINEKO_PROBE_BOOTSTRAP_PUBLIC_KEYS")),
		Issuer:         environmentValue("CINEKO_PROBE_BOOTSTRAP_ISSUER", central.ProbeBootstrapIssuer),
		Audience:       environmentValue("CINEKO_PROBE_BOOTSTRAP_AUDIENCE", central.ProbeBootstrapAudience),
		ClockSkew:      15 * time.Second,
		Registration:   registration,
	})
	if err != nil {
		return nil, err
	}
	probeRuntime, err := probe.NewBrowserRuntime(probe.BrowserRuntimeConfig{
		CentralURL:      os.Getenv("CINEKO_CENTRAL_URL"),
		DataDir:         filepath.Join(dataDir, "probe"),
		HTTPClient:      &http.Client{Timeout: 20 * time.Second},
		Credentials:     credentials,
		Registration:    registration,
		SeatMapExecutor: &clientSeatMapExecutor{browsers: browsers, userID: store.UserID(), state: capabilityState},
		Runtime:         probe.Config{AvailableCapabilities: capabilityState.AvailableCapabilities},
	})
	if err != nil {
		return nil, err
	}
	return startEmbeddedProbeRuntime(parent, probeRuntime, embeddedProbeStartupTimeout)
}

func startEmbeddedProbeRuntime(
	parent context.Context,
	runtime embeddedProbeRuntime,
	startupTimeout time.Duration,
) (*embeddedProbe, error) {
	if parent == nil || runtime == nil || startupTimeout <= 0 {
		return nil, errors.New("embedded Probe startup dependencies are incomplete")
	}
	ctx, cancel := context.WithCancel(parent)
	ready := make(chan error, 1)
	done := make(chan error, 1)
	go func() { done <- runtime.RunReady(ctx, ready) }()
	timer := time.NewTimer(startupTimeout)
	defer timer.Stop()
	select {
	case err := <-ready:
		if err != nil {
			cancel()
			_ = waitEmbeddedProbe(done, embeddedProbeShutdownTimeout)
			return nil, fmt.Errorf("start embedded Probe: %w", err)
		}
		embedded := &embeddedProbe{
			runtime: runtime, cancel: cancel, stopped: make(chan struct{}), failure: make(chan error, 1),
			shutdownDone: make(chan struct{}),
		}
		go embedded.observeRuntime(done)
		return embedded, nil
	case err := <-done:
		cancel()
		if err == nil {
			err = errors.New("embedded Probe stopped before readiness")
		}
		return nil, fmt.Errorf("start embedded Probe: %w", err)
	case <-timer.C:
		cancel()
		_ = waitEmbeddedProbe(done, embeddedProbeShutdownTimeout)
		return nil, errors.New("embedded Probe startup timed out")
	case <-parent.Done():
		cancel()
		_ = waitEmbeddedProbe(done, embeddedProbeShutdownTimeout)
		return nil, parent.Err()
	}
}

func (embedded *embeddedProbe) observeRuntime(done <-chan error) {
	err := <-done
	embedded.activityMu.Lock()
	closing := embedded.closing
	if err == nil && !closing {
		err = errors.New("embedded Probe stopped unexpectedly")
	}
	embedded.runtimeErr = err
	embedded.activityMu.Unlock()
	if !closing && !errors.Is(err, context.Canceled) {
		embedded.failure <- err
	}
	close(embedded.stopped)
}

func (embedded *embeddedProbe) Failure() <-chan error {
	return embedded.failure
}

// beginBooking prevents the embedded Probe from accepting new assignments
// until every overlapping local booking browser has closed.
func (embedded *embeddedProbe) beginBooking() (func(), error) {
	embedded.activityMu.Lock()
	if embedded.closing {
		embedded.activityMu.Unlock()
		return nil, errors.New("embedded Probe is shutting down")
	}
	embedded.activeBooking++
	if embedded.activeBooking == 1 {
		embedded.runtime.SetDraining(true)
	}
	embedded.activityMu.Unlock()

	var once sync.Once
	return func() {
		once.Do(func() {
			embedded.activityMu.Lock()
			embedded.activeBooking--
			if embedded.activeBooking == 0 && !embedded.closing {
				embedded.runtime.SetDraining(false)
			}
			embedded.activityMu.Unlock()
		})
	}, nil
}

func (embedded *embeddedProbe) OpenBooking(
	open func() (webui.Automation, error),
) (webui.Automation, error) {
	if open == nil {
		return nil, errors.New("booking browser opener is required")
	}
	release, err := embedded.beginBooking()
	if err != nil {
		return nil, err
	}
	automation, err := open()
	if err != nil {
		release()
		return nil, err
	}
	embedded.activityMu.Lock()
	closing := embedded.closing
	embedded.activityMu.Unlock()
	if closing {
		automation.Close()
		release()
		return nil, errors.New("embedded Probe is shutting down")
	}
	return &probeDrainingAutomation{Automation: automation, release: release}, nil
}

func (embedded *embeddedProbe) Close() error {
	embedded.shutdownOnce.Do(func() {
		embedded.activityMu.Lock()
		embedded.closing = true
		embedded.runtime.SetDraining(true)
		embedded.activityMu.Unlock()
		embedded.cancel()
		if err := waitEmbeddedProbeStop(embedded.stopped, embeddedProbeShutdownTimeout); err != nil {
			embedded.shutdownErr = err
		} else {
			embedded.activityMu.Lock()
			embedded.shutdownErr = embedded.runtimeErr
			embedded.activityMu.Unlock()
		}
		if errors.Is(embedded.shutdownErr, context.Canceled) {
			embedded.shutdownErr = nil
		}
		close(embedded.shutdownDone)
	})
	<-embedded.shutdownDone
	return embedded.shutdownErr
}

func waitEmbeddedProbeStop(stopped <-chan struct{}, timeout time.Duration) error {
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	select {
	case <-stopped:
		return nil
	case <-timer.C:
		return errors.New("embedded Probe shutdown timed out")
	}
}

func superviseEmbeddedProbe(
	ctx context.Context,
	embedded *embeddedProbe,
	onFailure func(error),
) {
	select {
	case err := <-embedded.Failure():
		if err != nil {
			onFailure(err)
		}
	case <-ctx.Done():
	}
}

func waitEmbeddedProbe(done <-chan error, timeout time.Duration) error {
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	select {
	case err := <-done:
		return err
	case <-timer.C:
		return errors.New("embedded Probe shutdown timed out")
	}
}

func environmentValue(name string, fallback string) string {
	if value := strings.TrimSpace(os.Getenv(name)); value != "" {
		return value
	}
	return fallback
}
