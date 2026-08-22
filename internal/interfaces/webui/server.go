package webui

import (
	"bytes"
	"context"
	"crypto/rand"
	"embed"
	"encoding/base64"
	"errors"
	"fmt"
	"io/fs"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/cineko-org/client/internal/application"
	"github.com/cineko-org/client/internal/domain"
	catalogpb "github.com/cineko-org/contracts/v3/gen/go/cineko/catalog"
	clientpb "github.com/cineko-org/contracts/v3/gen/go/cineko/client"
	seatmappb "github.com/cineko-org/contracts/v3/gen/go/cineko/seatmap"
	"google.golang.org/protobuf/proto"
)

//go:embed assets/*
var assets embed.FS

type Repository interface {
	application.TheaterRepository
	application.AuditoriumRepository
	application.SeatMapRepository
	application.CatalogRepository
	application.PresetRepository
	application.MonitorRepository
	application.ReservationRepository
	application.ExternalOperationRepository
}

type seatMapResolver interface {
	ResolveSeatMap(context.Context, string) (*seatmappb.Resolution, error)
}

type Automation interface {
	application.BookingGateway
	AuthenticateManuallyUntil(context.Context, time.Duration) error
	AuthenticateSavedUntil(context.Context, domain.AccountCredentials, time.Duration) error
	IsAuthenticated(context.Context) (bool, error)
	Close()
}

type CredentialVault interface {
	Load(context.Context, string) (domain.AccountCredentials, error)
	Save(context.Context, string, domain.AccountCredentials) error
	Delete(context.Context, string) error
}

type AutomationPurpose string

const (
	AutomationSession      AutomationPurpose = "session"
	AutomationScan         AutomationPurpose = "scan"
	AutomationCancellation AutomationPurpose = "cancellation"
)

type AutomationFactory func(context.Context, bool, AutomationPurpose, string) (Automation, error)

type lifetimeAutomation struct {
	Automation
	cancel context.CancelFunc
	once   sync.Once
}

func (automation *lifetimeAutomation) Close() {
	automation.once.Do(func() {
		automation.Automation.Close()
		automation.cancel()
	})
}

type Dependencies struct {
	Repository          Repository
	Factory             AutomationFactory
	IDs                 application.IDGenerator
	Clock               application.Clock
	Waiter              application.Waiter
	Events              application.EventPublisher
	AccountStateChanged func(bool)
	Credentials         CredentialVault
	UserID              string
	// BookingDemandChanged tells the Client runtime whether active monitors
	// need warm booking capacity. CGV member login is optional for the
	// supported non-member booking path.
	BookingDemandChanged func(bool)
	// BookingCapacityAvailable reports whether a ready booking slot exists.
	BookingCapacityAvailable func() bool
}

type Server struct {
	repository               Repository
	factory                  AutomationFactory
	ids                      application.IDGenerator
	clock                    application.Clock
	waiter                   application.Waiter
	eventPublisher           application.EventPublisher
	accountStateChanged      func(bool)
	credentials              CredentialVault
	userID                   string
	bookingDemandChanged     func(bool)
	bookingCapacityAvailable func() bool
	executionReady           chan struct{}

	rootContext     context.Context
	allowedHost     string
	tasksMu         sync.RWMutex
	tasks           map[string]*clientpb.WebUITaskState
	taskCancels     map[string]context.CancelFunc
	accountMu       sync.RWMutex
	account         *clientpb.WebUIAccountState
	paymentMu       sync.Mutex
	paymentSessions map[string]*paymentSession
}

func New(dependencies Dependencies) (*Server, error) {
	if dependencies.Repository == nil || dependencies.Factory == nil ||
		dependencies.IDs == nil || dependencies.Clock == nil || dependencies.Waiter == nil {
		return nil, errors.New("web UI dependencies are incomplete")
	}
	if dependencies.Credentials != nil && strings.TrimSpace(dependencies.UserID) == "" {
		return nil, errors.New("credential vault requires a Cineko user")
	}
	return &Server{
		repository: dependencies.Repository, factory: dependencies.Factory,
		ids: dependencies.IDs, clock: dependencies.Clock, waiter: dependencies.Waiter,
		eventPublisher: dependencies.Events, accountStateChanged: dependencies.AccountStateChanged,
		credentials: dependencies.Credentials, userID: strings.TrimSpace(dependencies.UserID),
		bookingDemandChanged:     dependencies.BookingDemandChanged,
		bookingCapacityAvailable: dependencies.BookingCapacityAvailable,
		executionReady:           make(chan struct{}, 1),
		tasks:                    make(map[string]*clientpb.WebUITaskState), taskCancels: make(map[string]context.CancelFunc),
		paymentSessions: make(map[string]*paymentSession),
	}, nil
}

func ListenLoopback(address string) (net.Listener, string, error) {
	listener, err := (&net.ListenConfig{}).Listen(context.Background(), "tcp", address)
	if err != nil {
		return nil, "", err
	}
	tcpAddress, ok := listener.Addr().(*net.TCPAddr)
	if !ok || tcpAddress.IP == nil || !tcpAddress.IP.IsLoopback() {
		_ = listener.Close()
		return nil, "", errors.New("cineko UI must listen on a loopback address")
	}
	host := net.JoinHostPort(tcpAddress.IP.String(), fmt.Sprintf("%d", tcpAddress.Port))
	return listener, "http://" + host, nil
}

func (server *Server) Serve(ctx context.Context, listener net.Listener) error {
	server.Start(ctx)
	server.allowedHost = listener.Addr().String()
	httpServer := &http.Server{
		Handler:           server.routes(),
		ReadHeaderTimeout: 5 * time.Second,
		IdleTimeout:       60 * time.Second,
		MaxHeaderBytes:    16 << 10,
	}
	done := make(chan struct{})
	// #nosec G118 -- this shutdown watcher exits on either the server or caller context.
	go func() {
		select {
		case <-ctx.Done():
			shutdownContext, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			_ = httpServer.Shutdown(shutdownContext)
		case <-done:
		}
	}()
	err := httpServer.Serve(listener)
	close(done)
	if errors.Is(err, http.ErrServerClosed) {
		return nil
	}
	return err
}

func (server *Server) routes() http.Handler {
	mux := server.apiRoutes()
	assetFS, _ := fs.Sub(assets, "assets")
	mux.Handle("GET /", http.FileServer(http.FS(assetFS)))
	return server.secure(mux)
}

func (server *Server) apiRoutes() *http.ServeMux {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/state", server.state)
	mux.HandleFunc("GET /api/status", server.status)
	mux.HandleFunc("GET /api/account", server.accountStatus)
	mux.HandleFunc("PUT /api/account/credentials", server.saveAccountCredentials)
	mux.HandleFunc("DELETE /api/account/credentials", server.deleteAccountCredentials)
	mux.HandleFunc("POST /api/auth/open", server.openAuthentication)
	mux.HandleFunc("POST /api/auth/restore", server.restoreAuthentication)
	mux.HandleFunc("POST /api/catalog/seat-map", server.resolveAuditoriumSeatMap)
	mux.HandleFunc("GET /api/catalog/seat-map:watch", server.watchAuditoriumSeatMap)
	mux.HandleFunc("GET /api/auditoriums", server.auditoriums)
	mux.HandleFunc("GET /api/seat-map", server.seatMap)
	mux.HandleFunc("POST /api/presets", server.createPreset)
	mux.HandleFunc("PUT /api/presets", server.updatePreset)
	mux.HandleFunc("DELETE /api/presets", server.deletePreset)
	mux.HandleFunc("POST /api/monitors", server.createMonitor)
	mux.HandleFunc("PUT /api/monitors", server.updateMonitor)
	mux.HandleFunc("DELETE /api/monitors", server.deleteMonitor)
	mux.HandleFunc("POST /api/monitors/retry", server.retryMonitor)
	mux.HandleFunc("POST /api/reservations/cancel", server.cancelReservation)
	mux.HandleFunc("GET /api/events", server.events)
	mux.HandleFunc("POST /api/events", server.createEvent)
	mux.HandleFunc("POST /api/events/read", server.readEvents)
	mux.HandleFunc("DELETE /api/events", server.clearEvents)

	return mux
}

// Start supplies the application lifetime used by asynchronous browser tasks.
func (server *Server) Start(ctx context.Context) {
	server.rootContext = ctx
	if ctx.Done() != nil {
		go func() {
			<-ctx.Done()
			server.closePaymentSessions()
		}()
	}
	now := server.clock.Now()
	if recovery, ok := server.repository.(startupRecoveryRepository); ok {
		events, err := recovery.RecoverInterruptedWork(ctx, now)
		if err != nil {
			server.recordMaintenanceFailure("startup-recovery", err)
		} else {
			for _, resource := range events {
				if event := resource.GetAppEvent(); event != nil {
					_ = server.persistEvent(ctx, event)
				}
			}
		}
	}
	if events, ok := server.repository.(appEventRepository); ok {
		if err := events.DeleteAppEventsBefore(ctx, now.AddDate(0, -6, 0)); err != nil {
			server.recordMaintenanceFailure("event-retention", err)
		}
	}
	server.refreshBookingDemand(ctx)
}

// NotifyBookingCapacityChanged wakes the desktop execution worker after a
// warm browser becomes ready or a retained payment session is released.
func (server *Server) NotifyBookingCapacityChanged() {
	server.signalExecutionAvailable()
}

// NotifyExecutionEvent wakes the execution worker after Central data changes.
func (server *Server) NotifyExecutionEvent() {
	server.signalExecutionAvailable()
}

// ExecutionAvailable is the local readiness event consumed by desktop execution.
func (server *Server) ExecutionAvailable() <-chan struct{} {
	return server.executionReady
}

// CanAcceptExecution prevents Central claims while no ready browser slot is
// available locally.
func (server *Server) CanAcceptExecution() bool {
	if server.bookingCapacityAvailable != nil {
		return server.bookingCapacityAvailable()
	}
	server.paymentMu.Lock()
	defer server.paymentMu.Unlock()
	return len(server.paymentSessions) == 0
}

func (server *Server) signalExecutionAvailable() {
	if server.executionReady == nil {
		return
	}
	select {
	case server.executionReady <- struct{}{}:
	default:
	}
}

func (server *Server) refreshBookingDemand(ctx context.Context) {
	if server.bookingDemandChanged == nil {
		return
	}
	active := false
	if strings.TrimSpace(server.userID) != "" {
		monitors, err := server.repository.ListMonitorsByUser(ctx, server.userID)
		if err != nil {
			server.recordMaintenanceFailure("booking-demand", err)
		} else {
			for _, resource := range monitors {
				state := resource.GetMonitor().GetState()
				if state.GetPending() != nil || state.GetRunning() != nil {
					active = true
					break
				}
			}
		}
	}
	server.bookingDemandChanged(active)
	server.signalExecutionAvailable()
}

func (server *Server) recordMaintenanceFailure(id string, err error) {
	server.tasksMu.Lock()
	server.tasks[id] = taskStateMessage(id, "failed", publicErrorMessage(err), server.clock.Now())
	server.tasksMu.Unlock()
}

// HasActiveTasks protects configuration transfers from replacing records while
// a browser workflow can still write results based on the previous snapshot.
func (server *Server) HasActiveTasks() bool {
	server.tasksMu.RLock()
	defer server.tasksMu.RUnlock()
	for _, state := range server.tasks {
		if state.GetRunning() != nil {
			return true
		}
	}
	return false
}

// DesktopHandler serves only dynamic API requests inside the Wails asset
// server. It does not open a TCP listener.
func (server *Server) DesktopHandler() http.Handler {
	return SecurityHeaders(server.apiRoutes())
}

func Assets() fs.FS {
	assetFS, _ := fs.Sub(assets, "assets")
	return assetFS
}

func (server *Server) secure(next http.Handler) http.Handler {
	return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		remoteHost, _, err := net.SplitHostPort(request.RemoteAddr)
		if err != nil || net.ParseIP(remoteHost) == nil || !net.ParseIP(remoteHost).IsLoopback() {
			http.Error(writer, "loopback access only", http.StatusForbidden)
			return
		}
		if request.Host != server.allowedHost {
			http.Error(writer, "invalid host", http.StatusForbidden)
			return
		}
		if request.Method != http.MethodGet && request.Method != http.MethodHead {
			if !sameOrigin(request) || !strings.HasPrefix(request.Header.Get("Content-Type"), "application/json") {
				http.Error(writer, "same-origin JSON request required", http.StatusForbidden)
				return
			}
		}
		SecurityHeaders(next).ServeHTTP(writer, request)
	})
}

func SecurityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("X-Content-Type-Options", "nosniff")
		writer.Header().Set("X-Frame-Options", "DENY")
		writer.Header().Set("Referrer-Policy", "no-referrer")
		if strings.HasPrefix(request.URL.Path, "/api/") {
			writer.Header().Set("Cache-Control", "no-store")
		}
		if request.Method == http.MethodGet && (request.URL.Path == "/" || request.URL.Path == "/index.html") {
			styleNonce, err := newStyleNonce()
			if err != nil {
				http.Error(writer, "cannot prepare application", http.StatusInternalServerError)
				return
			}
			writer.Header().Set("Content-Security-Policy", contentSecurityPolicy(styleNonce))
			serveApplicationPage(writer, styleNonce)
			return
		}
		writer.Header().Set("Content-Security-Policy", contentSecurityPolicy(""))
		next.ServeHTTP(writer, request)
	})
}

const styleNoncePlaceholder = "__CINEKO_STYLE_NONCE__"

// newStyleNonce creates the per-document nonce used by Mantine style tags.
func newStyleNonce() (string, error) {
	random := make([]byte, 16)
	if _, err := rand.Read(random); err != nil {
		return "", fmt.Errorf("create style nonce: %w", err)
	}
	return base64.RawStdEncoding.EncodeToString(random), nil
}

// contentSecurityPolicy keeps scripts strict while allowing Mantine style properties.
func contentSecurityPolicy(styleNonce string) string {
	styleSources := "style-src 'self'"
	if styleNonce != "" {
		styleSources += " 'nonce-" + styleNonce + "'"
	}
	return "default-src 'self'; img-src 'self' data:; " + styleSources +
		"; style-src-attr 'unsafe-inline'; script-src 'self'; connect-src 'self'; frame-ancestors 'none'"
}

// serveApplicationPage binds the document CSP nonce to Mantine's generated styles.
func serveApplicationPage(writer http.ResponseWriter, styleNonce string) {
	page, err := assets.ReadFile("assets/index.html")
	if err != nil {
		http.Error(writer, "cannot load application", http.StatusInternalServerError)
		return
	}
	placeholder := []byte(styleNoncePlaceholder)
	if !bytes.Contains(page, placeholder) {
		http.Error(writer, "application security metadata is missing", http.StatusInternalServerError)
		return
	}
	page = bytes.ReplaceAll(page, placeholder, []byte(styleNonce))
	writer.Header().Set("Content-Type", "text/html; charset=utf-8")
	writer.Header().Set("Cache-Control", "no-store")
	writer.WriteHeader(http.StatusOK)
	_, _ = writer.Write(page)
}

func sameOrigin(request *http.Request) bool {
	origin := request.Header.Get("Origin")
	if origin == "" {
		return true
	}
	parsed, err := url.Parse(origin)
	return err == nil && parsed.Scheme == "http" && parsed.Host == request.Host
}

func (server *Server) state(writer http.ResponseWriter, request *http.Request) {
	userID := strings.TrimSpace(request.URL.Query().Get("user"))
	if userID == "" {
		userID = "local-user"
	}
	ctx := request.Context()
	catalog, err := server.repository.GetCatalog(ctx)
	if err != nil {
		server.writeError(writer, err)
		return
	}
	presets, err := server.repository.ListPresetsByUser(ctx, userID)
	if err != nil {
		server.writeError(writer, err)
		return
	}
	monitors, err := server.repository.ListMonitorsByUser(ctx, userID)
	if err != nil {
		server.writeError(writer, err)
		return
	}
	reservations, err := server.repository.ListReservationsByUser(ctx, userID)
	if err != nil {
		server.writeError(writer, err)
		return
	}
	resources := make([]*clientpb.Resource, 0, len(presets)+len(monitors)+len(reservations))
	resources = append(resources, presets...)
	for _, resource := range monitors {
		monitor := resource.GetMonitor()
		if failed := monitor.GetState().GetFailed(); failed != nil && failed.GetReason() != "" {
			resource = proto.CloneOf(resource)
			failure := resource.GetMonitor().GetState().GetFailed()
			failure.SetReason(publicErrorMessage(errors.New(failure.GetReason())))
		}
		resources = append(resources, resource)
	}
	resources = append(resources, reservations...)
	writeProtoJSON(writer, http.StatusOK, clientpb.WebUIState_builder{
		UserId: &userID, Catalog: catalog, Resources: resources,
	}.Build())
}

func (server *Server) status(writer http.ResponseWriter, _ *http.Request) {
	server.tasksMu.RLock()
	defer server.tasksMu.RUnlock()
	tasks := make([]*clientpb.WebUITaskState, 0, len(server.tasks))
	for _, value := range server.tasks {
		tasks = append(tasks, value)
	}
	writeProtoJSON(writer, http.StatusOK, clientpb.WebUITaskStatusResponse_builder{Tasks: tasks}.Build())
}

func (server *Server) accountStatus(writer http.ResponseWriter, _ *http.Request) {
	server.accountMu.Lock()
	if server.account == nil {
		server.account = accountStateMessage("checking", false, "", "", server.clock.Now())
		go server.checkAuthentication()
	}
	value := server.account
	server.accountMu.Unlock()
	writeProtoJSON(writer, http.StatusOK, value)
}

func (server *Server) checkAuthentication() {
	ctx, cancel := context.WithTimeout(server.rootContext, 45*time.Second)
	defer cancel()
	automation, err := server.factory(ctx, true, AutomationSession, "account")
	authenticated := false
	if err == nil {
		defer automation.Close()
		authenticated, err = automation.IsAuthenticated(ctx)
	}
	server.setAccountState(authenticated, err)
}

func (server *Server) setAccountState(authenticated bool, err error) {
	credentialsSaved := false
	accountID := ""
	if server.credentials != nil {
		credentials, credentialErr := server.credentials.Load(server.lifetimeContext(), server.userID)
		switch {
		case credentialErr == nil:
			credentialsSaved = true
			accountID = credentials.ID
		case !errors.Is(credentialErr, domain.ErrAccountCredentialsNotFound) && err == nil:
			err = credentialErr
		}
	}
	message := ""
	status := "unauthenticated"
	switch {
	case err != nil:
		status = "error"
		message = publicErrorMessage(err)
	case authenticated:
		status = "authenticated"
	}
	server.accountMu.Lock()
	server.account = accountStateMessage(status, credentialsSaved, accountID, message, server.clock.Now())
	server.accountMu.Unlock()
	if server.accountStateChanged != nil {
		server.accountStateChanged(authenticated && err == nil)
	}
	server.refreshBookingDemand(server.lifetimeContext())
}

func (server *Server) saveAccountCredentials(writer http.ResponseWriter, request *http.Request) {
	if server.credentials == nil {
		server.writeAPIError(writer, request, http.StatusNotImplemented, "credentials_unavailable", "credential storage is unavailable", false)
		return
	}
	input := &clientpb.AccountCredentials{}
	if !decodeProtoJSON(server, writer, request, input) {
		return
	}
	credentials := domain.AccountCredentials{ID: input.GetId(), Password: input.GetPassword()}
	if err := server.credentials.Save(request.Context(), server.userID, credentials); err != nil {
		server.writeError(writer, err)
		return
	}
	server.accountMu.Lock()
	server.account = accountStateMessage("checking", true, strings.TrimSpace(input.GetId()), "", server.clock.Now())
	server.accountMu.Unlock()
	server.startSavedAuthentication()
	server.refreshBookingDemand(request.Context())
	writeProtoJSON(writer, http.StatusAccepted, actionStatus(true))
}

func (server *Server) deleteAccountCredentials(writer http.ResponseWriter, request *http.Request) {
	if server.credentials == nil {
		server.writeAPIError(writer, request, http.StatusNotImplemented, "credentials_unavailable", "credential storage is unavailable", false)
		return
	}
	if err := server.credentials.Delete(request.Context(), server.userID); err != nil {
		server.writeError(writer, err)
		return
	}
	server.accountMu.Lock()
	server.account = accountStateMessage("unauthenticated", false, "", "", server.clock.Now())
	server.accountMu.Unlock()
	server.refreshBookingDemand(request.Context())
	writeProtoJSON(writer, http.StatusOK, actionStatus(false))
}

func (server *Server) restoreAuthentication(writer http.ResponseWriter, _ *http.Request) {
	if server.credentials == nil {
		server.writeAPIError(writer, nil, http.StatusNotImplemented, "credentials_unavailable", "credential storage is unavailable", false)
		return
	}
	credentials, err := server.credentials.Load(server.lifetimeContext(), server.userID)
	if errors.Is(err, domain.ErrAccountCredentialsNotFound) {
		server.writeAPIError(writer, nil, http.StatusNotFound, "credentials_not_found", "saved CGV credentials were not found", false)
		return
	}
	if err != nil {
		server.writeError(writer, err)
		return
	}
	if !server.startCredentialAuthentication(credentials) {
		server.writeAPIError(writer, nil, http.StatusConflict, "authentication_in_progress", "CGV 로그인 브라우저가 이미 열려 있습니다.", true)
		return
	}
	writeProtoJSON(writer, http.StatusAccepted, actionStatus(true))
}

func (server *Server) startSavedAuthentication() {
	if server.credentials == nil {
		return
	}
	credentials, err := server.credentials.Load(server.lifetimeContext(), server.userID)
	if err != nil {
		if !errors.Is(err, domain.ErrAccountCredentialsNotFound) {
			server.setAccountState(false, err)
		}
		return
	}
	server.startCredentialAuthentication(credentials)
}

func (server *Server) startCredentialAuthentication(credentials domain.AccountCredentials) bool {
	if !server.beginTask("authentication") {
		return false
	}
	go func() {
		ctx, cancel := context.WithTimeout(server.lifetimeContext(), 6*time.Minute)
		defer cancel()
		var automation Automation
		var err error
		automation, err = server.factory(ctx, false, AutomationSession, "account")
		if err == nil {
			defer automation.Close()
			authenticated, checkErr := automation.IsAuthenticated(ctx)
			switch {
			case checkErr != nil:
				err = checkErr
			case authenticated:
				err = nil
			default:
				err = automation.AuthenticateSavedUntil(ctx, credentials, 5*time.Minute)
			}
		}
		server.setAccountState(err == nil, err)
		server.finishTask("authentication", err)
	}()
	return true
}

func (server *Server) lifetimeContext() context.Context {
	if server.rootContext != nil {
		return server.rootContext
	}
	return context.Background()
}

func (server *Server) openAuthentication(writer http.ResponseWriter, _ *http.Request) {
	if !server.beginTask("authentication") {
		server.writeAPIError(writer, nil, http.StatusConflict, "authentication_in_progress", "CGV 로그인 브라우저가 이미 열려 있습니다.", true)
		return
	}
	go func() {
		ctx, cancel := context.WithTimeout(server.rootContext, 6*time.Minute)
		defer cancel()
		automation, err := server.factory(ctx, false, AutomationSession, "account")
		if err == nil {
			defer automation.Close()
			err = automation.AuthenticateManuallyUntil(ctx, 5*time.Minute)
		}
		server.setAccountState(err == nil, err)
		server.finishTask("authentication", err)
	}()
	writeProtoJSON(writer, http.StatusAccepted, actionStatus(true))
}

func (server *Server) resolveAuditoriumSeatMap(writer http.ResponseWriter, request *http.Request) {
	input := &clientpb.SeatMapRequest{}
	if !decodeProtoJSON(server, writer, request, input) {
		return
	}
	resolver, supported := server.repository.(seatMapResolver)
	if !supported {
		server.writeAPIError(writer, request, http.StatusServiceUnavailable, "seat_map_unavailable", "seat-map resolution is unavailable", true)
		return
	}
	auditoriumID := input.GetAuditoriumId()
	resolution, err := resolver.ResolveSeatMap(request.Context(), auditoriumID)
	if err != nil {
		server.writeError(writer, err)
		return
	}
	status := http.StatusOK
	if resolution.GetState() == nil || resolution.GetState().GetIdle() == nil {
		status = http.StatusAccepted
	}
	writeProtoJSON(writer, status, resolution)
}

func (server *Server) auditoriums(writer http.ResponseWriter, request *http.Request) {
	id := strings.TrimSpace(request.URL.Query().Get("theaterId"))
	if id == "" {
		server.writeAPIError(writer, request, http.StatusBadRequest, "invalid_request", "theaterId is required", false)
		return
	}
	auditoriums, err := server.repository.ListAuditoriumsByTheater(request.Context(), id)
	if err != nil {
		server.writeError(writer, err)
		return
	}
	writeProtoJSON(writer, http.StatusOK, clientpb.AuditoriumResponse_builder{Auditoriums: auditoriums}.Build())
}

func (server *Server) seatMap(writer http.ResponseWriter, request *http.Request) {
	id := strings.TrimSpace(request.URL.Query().Get("auditoriumId"))
	if id == "" {
		server.writeAPIError(writer, request, http.StatusBadRequest, "invalid_request", "auditoriumId is required", false)
		return
	}
	value, err := server.repository.GetSeatMap(request.Context(), id)
	if err != nil {
		server.writeError(writer, err)
		return
	}
	writeProtoJSON(writer, http.StatusOK, value)
}

func (server *Server) createPreset(writer http.ResponseWriter, request *http.Request) {
	service := application.NewPresetService(server.repository, server.ids, server.clock)
	server.mutatePreset(writer, request, http.StatusCreated, service.Create)
}

func (server *Server) updatePreset(writer http.ResponseWriter, request *http.Request) {
	service := application.NewPresetService(server.repository, server.ids, server.clock)
	server.mutatePreset(writer, request, http.StatusOK, service.Update)
}

func (server *Server) mutatePreset(
	writer http.ResponseWriter,
	request *http.Request,
	status int,
	mutate func(context.Context, *clientpb.WebUIResourceMutation) (*clientpb.Resource, error),
) {
	input := &clientpb.WebUIResourceMutation{}
	if !decodeProtoJSON(server, writer, request, input) {
		return
	}
	value, err := mutate(request.Context(), input)
	if err != nil {
		server.writeError(writer, err)
		return
	}
	writeProtoJSON(writer, status, value)
}

func (server *Server) deletePreset(writer http.ResponseWriter, request *http.Request) {
	input := &clientpb.WebUIResourceDeletion{}
	if !decodeProtoJSON(server, writer, request, input) {
		return
	}
	err := application.NewPresetService(
		server.repository, server.ids, server.clock,
	).Delete(request.Context(), input.GetUserId(), input.GetId(), input.GetMutation().GetExpectedRevision())
	if err != nil {
		server.writeError(writer, err)
		return
	}
	writeProtoJSON(writer, http.StatusOK, actionStatus(false))
}

// ExecuteAvailability runs the exact showtime selected by Central after this
// Client receives its execution lease. Schedule discovery is deliberately not
// repeated here: a different result would violate the command being fenced.
func (server *Server) ExecuteAvailability(
	ctx context.Context,
	monitorID string,
	showtime *catalogpb.Showtime,
) error {
	if server.hasPaymentSession(monitorID) {
		return nil
	}
	job, err := server.repository.GetMonitor(ctx, monitorID)
	if err != nil {
		return err
	}
	if monitor := job.GetMonitor(); monitor == nil || monitor.GetState().GetTriggered() != nil || monitor.GetState().GetPaymentUnknown() != nil {
		return nil
	}
	monitor, preset, theater, auditorium, err := server.claimedBooking(ctx, monitorID)
	if err != nil {
		return err
	}
	browserContext := server.rootContext
	if browserContext == nil {
		browserContext = context.Background()
	}
	browserLifetimeContext, cancelBrowserLifetime := context.WithCancel(browserContext)
	stopBrowserFenceWatch := context.AfterFunc(ctx, cancelBrowserLifetime)
	openedAutomation, err := server.factory(browserLifetimeContext, false, AutomationSession, monitorID)
	if err != nil {
		_ = stopBrowserFenceWatch()
		cancelBrowserLifetime()
		return err
	}
	automation := Automation(&lifetimeAutomation{
		Automation: openedAutomation, cancel: cancelBrowserLifetime,
	})
	stopAutomationFenceWatch := context.AfterFunc(ctx, automation.Close)
	retained := false
	defer func() {
		if !retained {
			_ = stopBrowserFenceWatch()
			_ = stopAutomationFenceWatch()
			cancelBrowserLifetime()
			automation.Close()
		}
	}()
	worker := application.NewBookingWorker(application.BookingWorkerDependencies{
		Monitors: server.repository, Reservations: server.repository,
		Booking: automation, IDs: server.ids, Clock: server.clock,
		Waiter: server.waiter,
	})
	reservation, err := worker.RunClaimedShowtime(ctx, monitor, preset, theater, auditorium, showtime)
	if !stopBrowserFenceWatch() || !stopAutomationFenceWatch() || ctx.Err() != nil {
		return ctx.Err()
	}
	if err == nil && reservation.GetReservation().GetPrepared() != nil {
		retained = server.retainPaymentSession(monitorID, reservation, automation)
	}
	return err
}

func (server *Server) claimedBooking(
	ctx context.Context,
	monitorID string,
) (*clientpb.Resource, *clientpb.Resource, *catalogpb.Theater, *catalogpb.Auditorium, error) {
	job, err := server.repository.GetMonitor(ctx, monitorID)
	if err != nil {
		return nil, nil, nil, nil, err
	}
	monitor := job.GetMonitor()
	if monitor == nil {
		return nil, nil, nil, nil, application.ErrNotFound
	}
	preset, err := server.repository.GetPreset(ctx, monitor.GetPresetId())
	if err != nil {
		return nil, nil, nil, nil, err
	}
	presetMessage := preset.GetPreset()
	if presetMessage == nil {
		return nil, nil, nil, nil, application.ErrNotFound
	}
	theater, err := server.repository.GetTheater(ctx, presetMessage.GetTheaterId())
	if err != nil {
		return nil, nil, nil, nil, err
	}
	auditorium, err := server.repository.GetAuditorium(ctx, presetMessage.GetAuditoriumId())
	if err != nil {
		return nil, nil, nil, nil, err
	}
	return job, preset, theater, auditorium, nil
}

func (server *Server) cancelReservation(writer http.ResponseWriter, request *http.Request) {
	input := &clientpb.WebUIReservationCancellationRequest{}
	if !decodeProtoJSON(server, writer, request, input) {
		return
	}
	reservation := input.GetReservation()
	server.withAutomationKey(writer, request.Context(), !input.GetHeadful(), AutomationCancellation, reservation.GetId(), func(automation Automation) (*clientpb.WebUICancellationResult, error) {
		result, err := application.NewCancellationService(
			server.repository, automation, server.clock, server.repository,
		).Cancel(request.Context(), input)
		if err == nil && input.GetCommit() {
			server.addEvent(appSuccessEvent(reservation.GetUserId(), "reservation.cancelled", "예매 취소가 완료되었습니다."))
		}
		if err != nil {
			return nil, err
		}
		return result, nil
	})
}

func (server *Server) withAutomationKey(
	writer http.ResponseWriter,
	ctx context.Context,
	background bool,
	purpose AutomationPurpose,
	sessionKey string,
	action func(Automation) (*clientpb.WebUICancellationResult, error),
) {
	automation, err := server.factory(ctx, background, purpose, sessionKey)
	if err != nil {
		server.writeError(writer, err)
		return
	}
	defer automation.Close()
	value, err := action(automation)
	if err != nil {
		server.writeError(writer, err)
		return
	}
	writeProtoJSON(writer, http.StatusOK, value)
}

func (server *Server) beginTask(id string) bool {
	server.tasksMu.Lock()
	defer server.tasksMu.Unlock()
	if state, exists := server.tasks[id]; exists && state.GetRunning() != nil {
		return false
	}
	server.tasks[id] = taskStateMessage(id, "running", "", server.clock.Now())
	return true
}

func (server *Server) finishTask(id string, err error) {
	status := "completed"
	message := ""
	if errors.Is(err, context.Canceled) {
		status = "stopped"
	} else if err != nil {
		status = "failed"
		message = publicErrorMessage(err)
	}
	server.tasksMu.Lock()
	server.tasks[id] = taskStateMessage(id, status, message, server.clock.Now())
	delete(server.taskCancels, id)
	server.tasksMu.Unlock()
}

func (server *Server) writeError(writer http.ResponseWriter, err error) {
	status := http.StatusInternalServerError
	switch {
	case errors.Is(err, application.ErrNotFound):
		status = http.StatusNotFound
	case errors.Is(err, application.ErrConflict):
		status = http.StatusConflict
	case errors.Is(err, application.ErrBookingNotOpen), errors.Is(err, application.ErrSeatUnavailable),
		errors.Is(err, application.ErrMonitorExpired):
		status = http.StatusUnprocessableEntity
	}
	server.writeAPIError(writer, nil, status, "request_failed", publicErrorMessage(err), status >= http.StatusInternalServerError)
}

func publicErrorMessage(err error) string {
	if err == nil {
		return "요청을 처리하지 못했습니다."
	}
	switch {
	case errors.Is(err, application.ErrNotFound):
		return "요청한 정보를 찾을 수 없습니다. 새로고침 후 다시 시도하세요."
	case errors.Is(err, application.ErrConflict):
		return "다른 변경사항이 먼저 저장되었습니다. 새로고침 후 다시 시도하세요."
	case errors.Is(err, application.ErrBookingNotOpen):
		return "아직 예매할 수 없는 회차입니다."
	case errors.Is(err, application.ErrSeatUnavailable):
		return "조건에 맞는 좌석을 찾지 못했습니다."
	case errors.Is(err, application.ErrMonitorExpired):
		return "관찰할 날짜가 지나 모니터가 종료되었습니다."
	case errors.Is(err, context.Canceled), errors.Is(err, context.DeadlineExceeded):
		return "요청 시간이 초과되었습니다. 잠시 후 다시 시도하세요."
	}
	message := strings.ToLower(err.Error())
	switch {
	case strings.Contains(message, "proxy"), strings.Contains(message, "soxy"), strings.Contains(message, "socks"):
		return "프록시 설정이나 연결 상태를 확인하세요."
	case strings.Contains(message, "credential"), strings.Contains(message, "authenticate"), strings.Contains(message, "authentication"), strings.Contains(message, "login"):
		return "CGV 로그인에 실패했습니다. 로그인 정보를 확인하고 다시 시도하세요."
	case strings.Contains(message, "central"), strings.Contains(message, "connect"), strings.Contains(message, "dial"), strings.Contains(message, "network"):
		return "Cineko 서비스에 연결할 수 없습니다. 잠시 후 다시 시도하세요."
	default:
		return "요청을 처리하지 못했습니다. 입력값을 확인하고 다시 시도하세요."
	}
}
