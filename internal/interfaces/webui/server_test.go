package webui

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/cineko-org/client/internal/application"
	"github.com/cineko-org/client/internal/domain"
	"github.com/cineko-org/client/internal/testsupport/memoryrepo"
)

func TestListenLoopbackRejectsPublicBinding(t *testing.T) {
	t.Parallel()

	if listener, _, err := ListenLoopback("0.0.0.0:0"); err == nil {
		_ = listener.Close()
		t.Fatal("ListenLoopback() accepted a public bind address")
	}
}

func TestClientAPIExcludesCentralOperations(t *testing.T) {
	t.Parallel()
	server := &Server{
		repository: memoryrepo.New(),
		ids:        &webAtomicIDs{},
		clock:      webTestClock{time.Date(2026, 8, 10, 10, 0, 0, 0, time.UTC)},
	}
	stateRequest := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/api/state?user=user", nil)
	stateResponse := httptest.NewRecorder()
	server.apiRoutes().ServeHTTP(stateResponse, stateRequest)
	if stateResponse.Code != http.StatusOK {
		t.Fatalf("state status = %d, body = %s", stateResponse.Code, stateResponse.Body.String())
	}
	for _, adminField := range []string{"openingInsights", "collections", "scheduleIntelligence"} {
		if strings.Contains(stateResponse.Body.String(), `"`+adminField+`"`) {
			t.Fatalf("Client state exposes Central field %q: %s", adminField, stateResponse.Body.String())
		}
	}
	collectionRequest := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/api/collections", nil)
	collectionResponse := httptest.NewRecorder()
	server.apiRoutes().ServeHTTP(collectionResponse, collectionRequest)
	if collectionResponse.Code != http.StatusNotFound {
		t.Fatalf("Client collection API status = %d", collectionResponse.Code)
	}
}

func TestAccountCheckUsesStableAccountSession(t *testing.T) {
	t.Parallel()
	var gotPurpose AutomationPurpose
	var gotSessionKey string
	server := &Server{
		rootContext: context.Background(),
		clock:       webTestClock{time.Date(2026, 8, 10, 10, 0, 0, 0, time.UTC)},
		factory: func(_ context.Context, _ bool, purpose AutomationPurpose, sessionKey string) (Automation, error) {
			gotPurpose = purpose
			gotSessionKey = sessionKey
			return &webProbeAutomation{probes: &atomic.Int32{}}, nil
		},
	}
	server.checkAuthentication()
	if gotPurpose != AutomationSession || gotSessionKey != "account" {
		t.Fatalf("account check browser = %q/%q", gotPurpose, gotSessionKey)
	}
	if !server.account.Authenticated || server.account.Status != "authenticated" {
		t.Fatalf("account state = %+v", server.account)
	}
}

type webCredentialVault struct {
	mu          sync.Mutex
	credentials domain.AccountCredentials
}

func TestRefreshBookingDemandRequiresAuthenticatedSessionAndActiveMonitor(t *testing.T) {
	ctx := t.Context()
	store := memoryrepo.New()
	demands := make(chan bool, 4)
	server := &Server{
		repository: store, userID: "user",
		bookingDemandChanged: func(active bool) { demands <- active },
	}
	server.refreshBookingDemand(ctx)
	if active := <-demands; active {
		t.Fatal("unauthenticated account created warm demand")
	}
	monitor := domain.MonitorJob{
		ID: "monitor", UserID: "user", PresetID: "preset", Mode: domain.MonitorModeOpening,
		MovieID: "movie_1", Movie: "영화", TargetDates: []string{"2026-08-20"}, Status: domain.MonitorPending,
	}
	if err := store.PutMonitor(ctx, monitor); err != nil {
		t.Fatal(err)
	}
	server.account = accountState{Status: "authenticated", Authenticated: true}
	server.refreshBookingDemand(ctx)
	if active := <-demands; !active {
		t.Fatal("active opening monitor did not create warm demand")
	}
	monitor.Mode = domain.MonitorModeCancellation
	if err := store.PutMonitor(ctx, monitor); err != nil {
		t.Fatal(err)
	}
	server.refreshBookingDemand(ctx)
	if active := <-demands; !active {
		t.Fatal("active cancellation monitor did not create warm demand")
	}
}

func TestAccountCheckDoesNotStartStoredLogin(t *testing.T) {
	t.Parallel()
	savedLogin := make(chan domain.AccountCredentials, 1)
	authenticated := false
	server := &Server{
		rootContext: t.Context(), clock: webTestClock{time.Date(2026, 8, 21, 7, 0, 0, 0, time.UTC)},
		credentials: &webCredentialVault{credentials: domain.AccountCredentials{ID: "member", Password: "secret"}},
		userID:      "user", tasks: make(map[string]taskState), taskCancels: make(map[string]context.CancelFunc),
		factory: func(context.Context, bool, AutomationPurpose, string) (Automation, error) {
			return &webProbeAutomation{probes: &atomic.Int32{}, authenticated: &authenticated, savedLogin: savedLogin}, nil
		},
	}

	server.checkAuthentication()
	select {
	case <-savedLogin:
		t.Fatal("account check started stored login without user action")
	default:
	}
	if server.account.Status != "unauthenticated" {
		t.Fatalf("account state = %+v", server.account)
	}
}

func (vault *webCredentialVault) Load(context.Context, string) (domain.AccountCredentials, error) {
	vault.mu.Lock()
	defer vault.mu.Unlock()
	if vault.credentials.ID == "" {
		return domain.AccountCredentials{}, domain.ErrAccountCredentialsNotFound
	}
	return vault.credentials, nil
}

func (vault *webCredentialVault) Save(_ context.Context, _ string, credentials domain.AccountCredentials) error {
	vault.mu.Lock()
	defer vault.mu.Unlock()
	vault.credentials = credentials
	return nil
}

func (vault *webCredentialVault) Delete(context.Context, string) error {
	vault.mu.Lock()
	defer vault.mu.Unlock()
	vault.credentials = domain.AccountCredentials{}
	return nil
}

func TestSavedAccountCredentialsRestoreSessionWithoutReturningPassword(t *testing.T) {
	t.Parallel()
	vault := &webCredentialVault{}
	savedLogin := make(chan domain.AccountCredentials, 1)
	authenticated := false
	server := &Server{
		rootContext: t.Context(), clock: webTestClock{time.Date(2026, 8, 19, 7, 0, 0, 0, time.UTC)},
		credentials: vault, userID: "user", tasks: make(map[string]taskState),
		taskCancels: make(map[string]context.CancelFunc),
		factory: func(context.Context, bool, AutomationPurpose, string) (Automation, error) {
			return &webProbeAutomation{probes: &atomic.Int32{}, authenticated: &authenticated, savedLogin: savedLogin}, nil
		},
	}

	request := httptest.NewRequestWithContext(t.Context(), http.MethodPut, "/api/account/credentials",
		strings.NewReader(`{"id":"member","password":"top-secret"}`))
	response := httptest.NewRecorder()
	server.apiRoutes().ServeHTTP(response, request)
	if response.Code != http.StatusAccepted || strings.Contains(response.Body.String(), "top-secret") {
		t.Fatalf("save credentials response = %d, %s", response.Code, response.Body.String())
	}
	select {
	case credentials := <-savedLogin:
		if credentials.ID != "member" || credentials.Password != "top-secret" {
			t.Fatalf("saved login credentials = %+v", credentials)
		}
	case <-time.After(time.Second):
		t.Fatal("saved credentials did not start session restoration")
	}

	deadline := time.Now().Add(time.Second)
	for {
		server.accountMu.RLock()
		state := server.account
		server.accountMu.RUnlock()
		if state.Authenticated && state.CredentialsSaved && state.AccountID == "member" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("account state = %+v", state)
		}
		time.Sleep(time.Millisecond)
	}

	deleteRequest := httptest.NewRequestWithContext(t.Context(), http.MethodDelete, "/api/account/credentials", nil)
	deleteResponse := httptest.NewRecorder()
	server.apiRoutes().ServeHTTP(deleteResponse, deleteRequest)
	if deleteResponse.Code != http.StatusOK {
		t.Fatalf("delete credentials response = %d, %s", deleteResponse.Code, deleteResponse.Body.String())
	}
	if _, err := vault.Load(t.Context(), "user"); !errors.Is(err, domain.ErrAccountCredentialsNotFound) {
		t.Fatalf("credentials remain after delete: %v", err)
	}
}

func TestRestoreAuthenticationExplainsMissingSavedCredentials(t *testing.T) {
	t.Parallel()
	server := &Server{
		rootContext: t.Context(), credentials: &webCredentialVault{}, userID: "user",
		tasks: make(map[string]taskState), taskCancels: make(map[string]context.CancelFunc),
	}
	request := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/api/auth/restore", nil)
	response := httptest.NewRecorder()
	server.apiRoutes().ServeHTTP(response, request)
	if response.Code != http.StatusNotFound || !strings.Contains(response.Body.String(), "saved CGV credentials were not found") {
		t.Fatalf("restore without saved credentials = %d, %s", response.Code, response.Body.String())
	}
}

func TestSeatMapRequestReturnsWaitingWithoutOpeningBrowser(t *testing.T) {
	t.Parallel()
	var browserOpens atomic.Int32
	server := &Server{
		repository: memoryrepo.New(),
		clock:      webTestClock{time.Date(2026, 8, 10, 10, 0, 0, 0, time.UTC)},
		factory: func(context.Context, bool, AutomationPurpose, string) (Automation, error) {
			browserOpens.Add(1)
			return &webProbeAutomation{probes: &atomic.Int32{}}, nil
		},
	}
	request := httptest.NewRequestWithContext(
		t.Context(), http.MethodPost, "/api/catalog/seat-map",
		strings.NewReader(`{"auditoriumId":"auditorium"}`),
	)
	response := httptest.NewRecorder()
	server.apiRoutes().ServeHTTP(response, request)
	if response.Code != http.StatusAccepted || !strings.Contains(response.Body.String(), `"status":"waiting"`) {
		t.Fatalf("seat-map request = %d, %s", response.Code, response.Body.String())
	}
	if browserOpens.Load() != 0 {
		t.Fatalf("seat-map request opened %d local browsers", browserOpens.Load())
	}
}

func TestSeatMapRequestReturnsCentralStoredLayoutWithoutOpeningBrowser(t *testing.T) {
	t.Parallel()
	var browserOpens atomic.Int32
	repository := memoryrepo.New()
	want := domain.SeatMap{
		AuditoriumID: "auditorium", Version: "layout-hash",
		Seats: []domain.Seat{{ID: "seat-1", AuditoriumID: "auditorium", Label: "A1"}},
	}
	if err := repository.PutSeatMap(t.Context(), want); err != nil {
		t.Fatal(err)
	}
	server := &Server{
		repository: repository,
		clock:      webTestClock{time.Date(2026, 8, 10, 10, 0, 0, 0, time.UTC)},
		factory: func(context.Context, bool, AutomationPurpose, string) (Automation, error) {
			browserOpens.Add(1)
			return &webProbeAutomation{probes: &atomic.Int32{}}, nil
		},
	}
	request := httptest.NewRequestWithContext(
		t.Context(), http.MethodPost, "/api/catalog/seat-map",
		strings.NewReader(`{"auditoriumId":"auditorium"}`),
	)
	response := httptest.NewRecorder()
	server.apiRoutes().ServeHTTP(response, request)
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"version":"layout-hash"`) {
		t.Fatalf("seat-map request = %d, %s", response.Code, response.Body.String())
	}
	if browserOpens.Load() != 0 {
		t.Fatalf("seat-map request opened %d local browsers", browserOpens.Load())
	}
}

func TestRemovedClientCatalogScanRoutesAreUnavailable(t *testing.T) {
	t.Parallel()
	server := &Server{repository: memoryrepo.New()}
	for _, path := range []string{"/api/catalog/sync", "/api/catalog/auditoriums"} {
		response := httptest.NewRecorder()
		request := httptest.NewRequestWithContext(t.Context(), http.MethodPost, path, nil)
		server.apiRoutes().ServeHTTP(response, request)
		if response.Code != http.StatusNotFound {
			t.Fatalf("POST %s status = %d", path, response.Code)
		}
	}
}

type webAtomicIDs struct{ value atomic.Int32 }

func (ids *webAtomicIDs) NewID() string { return fmt.Sprintf("id-%d", ids.value.Add(1)) }

type webEventPublisher struct {
	events []domain.AppEvent
	err    error
}

func (publisher *webEventPublisher) Publish(_ context.Context, event domain.AppEvent) error {
	publisher.events = append(publisher.events, event)
	return publisher.err
}

type webProbeAutomation struct {
	probes        *atomic.Int32
	authenticated *bool
	savedLogin    chan domain.AccountCredentials
}

func (automation *webProbeAutomation) FindShowtimes(context.Context, application.ShowtimeQuery) ([]domain.Showtime, error) {
	automation.probes.Add(1)
	return nil, nil
}
func (*webProbeAutomation) CaptureSchedules(context.Context, domain.Theater, []string) ([]domain.ScheduleCapture, error) {
	return nil, nil
}
func (*webProbeAutomation) OpenSeatSelection(
	context.Context,
	domain.Showtime,
	int,
) (domain.SeatSelection, error) {
	return domain.SeatSelection{}, nil
}
func (*webProbeAutomation) PreparePayment(context.Context, domain.Showtime, []string) (domain.BookingDraft, error) {
	return domain.BookingDraft{}, nil
}
func (*webProbeAutomation) PrepareCancellation(context.Context, domain.Reservation) (domain.CancellationDraft, error) {
	return domain.CancellationDraft{}, nil
}
func (*webProbeAutomation) CommitCancellation(context.Context) error { return nil }
func (*webProbeAutomation) AuthenticateManuallyUntil(context.Context, time.Duration) error {
	return nil
}
func (automation *webProbeAutomation) AuthenticateSavedUntil(
	_ context.Context,
	credentials domain.AccountCredentials,
	_ time.Duration,
) error {
	if automation.savedLogin != nil {
		automation.savedLogin <- credentials
	}
	return nil
}
func (automation *webProbeAutomation) IsAuthenticated(context.Context) (bool, error) {
	if automation.authenticated != nil {
		return *automation.authenticated, nil
	}
	return true, nil
}
func (*webProbeAutomation) Close() {}

func TestSecureRejectsCrossOriginMutation(t *testing.T) {
	t.Parallel()

	server := &Server{allowedHost: "127.0.0.1:8877"}
	handler := server.secure(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writer.WriteHeader(http.StatusNoContent)
	}))
	request := httptest.NewRequestWithContext(
		context.Background(), http.MethodPost, "http://127.0.0.1:8877/api/test", strings.NewReader(`{}`),
	)
	request.Host = "127.0.0.1:8877"
	request.RemoteAddr = "127.0.0.1:54321"
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Origin", "https://attacker.example")
	recorder := httptest.NewRecorder()

	handler.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want %d", recorder.Code, http.StatusForbidden)
	}
}

func TestEventAPIUsesDurableStore(t *testing.T) {
	t.Parallel()
	store := memoryrepo.New()
	now := time.Date(2026, 8, 10, 10, 0, 0, 0, time.UTC)
	publisher := &webEventPublisher{}
	server := &Server{repository: store, ids: &webAtomicIDs{}, clock: webTestClock{now}, eventPublisher: publisher}
	handler := server.apiRoutes()

	create := httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/api/events",
		strings.NewReader(`{"userId":"user","kind":"test","tone":"success","message":"done"}`))
	created := httptest.NewRecorder()
	handler.ServeHTTP(created, create)
	if created.Code != http.StatusCreated {
		t.Fatalf("create event status/body = %d/%s", created.Code, created.Body.String())
	}
	if len(publisher.events) != 1 || publisher.events[0].ID == "" || publisher.events[0].Message != "done" {
		t.Fatalf("published events = %+v", publisher.events)
	}
	server.RecordLocalSystemEvent("user", "hook.delivery_failed", domain.EventError, "local only")
	if len(publisher.events) != 1 {
		t.Fatalf("local event was republished: %+v", publisher.events)
	}
	listed := httptest.NewRecorder()
	handler.ServeHTTP(listed, httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/api/events?user=user", nil))
	if listed.Code != http.StatusOK || !strings.Contains(listed.Body.String(), `"message":"done"`) {
		t.Fatalf("list events status/body = %d/%s", listed.Code, listed.Body.String())
	}
	for _, method := range []string{http.MethodPost, http.MethodDelete} {
		path := "/api/events/read"
		if method == http.MethodDelete {
			path = "/api/events"
		}
		request := httptest.NewRequestWithContext(context.Background(), method, path, strings.NewReader(`{"userId":"user"}`))
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		if response.Code != http.StatusOK {
			t.Fatalf("%s %s = %d/%s", method, path, response.Code, response.Body.String())
		}
	}
}

func TestCreateMonitorIsIdempotent(t *testing.T) {
	t.Parallel()
	store := memoryrepo.New()
	now := time.Date(2026, 8, 10, 10, 0, 0, 0, time.UTC)
	if err := store.PutPreset(context.Background(), domain.Preset{
		ID: "preset", UserID: "user", Name: "seat", TheaterID: "theater", AuditoriumID: "auditorium", SeatCount: 1,
	}); err != nil {
		t.Fatal(err)
	}
	server := &Server{
		repository: store, ids: &webAtomicIDs{}, clock: webTestClock{now},
	}
	body := `{"idempotencyKey":"command","userId":"user","presetId":"preset","movieId":"movie_1","movie":"Movie","targetDates":["2026-08-20"],"pollInterval":180000000000,"pollIntervalMax":480000000000}`
	for range 2 {
		request := httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/api/monitors", strings.NewReader(body))
		response := httptest.NewRecorder()
		server.apiRoutes().ServeHTTP(response, request)
		if response.Code != http.StatusCreated {
			t.Fatalf("create status/body = %d/%s", response.Code, response.Body.String())
		}
	}
	monitors, err := store.ListMonitorsByUser(context.Background(), "user")
	if err != nil || len(monitors) != 1 || monitors[0].ID != "command" {
		t.Fatalf("idempotent monitors = %+v, %v", monitors, err)
	}
}

func TestSecureAcceptsLoopbackSameOriginJSON(t *testing.T) {
	t.Parallel()

	server := &Server{allowedHost: "127.0.0.1:8877"}
	handler := server.secure(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writer.WriteHeader(http.StatusNoContent)
	}))
	request := httptest.NewRequestWithContext(
		context.Background(), http.MethodPost, "http://127.0.0.1:8877/api/test", strings.NewReader(`{}`),
	)
	request.Host = "127.0.0.1:8877"
	request.RemoteAddr = "127.0.0.1:54321"
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Origin", "http://127.0.0.1:8877")
	recorder := httptest.NewRecorder()

	handler.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want %d", recorder.Code, http.StatusNoContent)
	}
	if recorder.Header().Get("Content-Security-Policy") == "" {
		t.Fatal("CSP header is missing")
	}
}

func TestSecurityHeadersBindMantineStylesToDocumentNonce(t *testing.T) {
	t.Parallel()

	handler := SecurityHeaders(http.NotFoundHandler())
	first := httptest.NewRecorder()
	handler.ServeHTTP(first, httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/", nil))
	if first.Code != http.StatusOK {
		t.Fatalf("first application page status = %d, body = %s", first.Code, first.Body.String())
	}
	firstNonce := applicationStyleNonce(t, first.Body.String())
	policy := first.Header().Get("Content-Security-Policy")
	if !strings.Contains(policy, "style-src 'self' 'nonce-"+firstNonce+"'") {
		t.Fatalf("CSP does not authorize the application nonce: %q", policy)
	}
	if !strings.Contains(policy, "style-src-attr 'unsafe-inline'") {
		t.Fatalf("CSP does not authorize Mantine style properties: %q", policy)
	}
	if strings.Contains(first.Body.String(), styleNoncePlaceholder) {
		t.Fatal("application page still contains the nonce placeholder")
	}

	second := httptest.NewRecorder()
	handler.ServeHTTP(second, httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/index.html", nil))
	secondNonce := applicationStyleNonce(t, second.Body.String())
	if firstNonce == secondNonce {
		t.Fatal("application pages reused a CSP nonce")
	}
}

func applicationStyleNonce(t *testing.T, page string) string {
	t.Helper()
	const marker = `name="cineko-style-nonce" content="`
	start := strings.Index(page, marker)
	if start < 0 {
		t.Fatal("application page is missing the style nonce metadata")
	}
	start += len(marker)
	end := strings.Index(page[start:], `"`)
	if end < 0 {
		t.Fatal("application style nonce metadata is malformed")
	}
	nonce := page[start : start+end]
	if nonce == "" {
		t.Fatal("application style nonce is empty")
	}
	return nonce
}

func TestEmbeddedUIContainsMantineApplication(t *testing.T) {
	t.Parallel()

	page, err := assets.ReadFile("assets/index.html")
	if err != nil {
		t.Fatalf("read embedded index: %v", err)
	}
	for _, required := range []string{`id="root"`, `/app.css`, `/app.js`, `name="cineko-style-nonce"`} {
		if !strings.Contains(string(page), required) {
			t.Fatalf("embedded UI is missing %s", required)
		}
	}
	for _, removed := range []string{"brand-mark", "schedule-widgets", "app-helpers", `id="project-gate"`} {
		if strings.Contains(string(page), removed) {
			t.Fatalf("embedded UI still contains removed shell element %s", removed)
		}
	}
	for _, asset := range []string{"assets/app.js", "assets/app.css", "assets/PretendardVariable.woff2"} {
		content, readErr := assets.ReadFile(asset)
		if readErr != nil || len(content) == 0 {
			t.Fatalf("embedded UI asset %s is missing: %v", asset, readErr)
		}
	}
	script, _ := assets.ReadFile("assets/app.js")
	for _, contract := range []string{"SaveNetworkSettings", "좌석 프리셋", "Pretendard Variable"} {
		if !strings.Contains(string(script), contract) {
			t.Fatalf("embedded app bundle is missing %s contract", contract)
		}
	}
}

func TestConfigurationTransferDetectsActiveTasks(t *testing.T) {
	t.Parallel()
	server := &Server{tasks: map[string]taskState{}}
	if server.HasActiveTasks() {
		t.Fatal("empty task set reported active work")
	}
	server.tasks["scan"] = taskState{Status: "running"}
	if !server.HasActiveTasks() {
		t.Fatal("running task was not detected")
	}
	server.tasks["scan"] = taskState{Status: "completed"}
	if server.HasActiveTasks() {
		t.Fatal("completed task still blocks transfer")
	}
}

type webTestClock struct{ now time.Time }

func (clock webTestClock) Now() time.Time { return clock.now }
