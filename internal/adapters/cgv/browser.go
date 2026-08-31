package cgv

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/cineko-org/client/internal/logging"
	"github.com/cineko-org/probe/v2/networkcapture"
	"github.com/cineko-org/probe/v2/networkcapture/playwrightcapture"
	"github.com/mxschmitt/playwright-go"
)

const (
	homeURL          = "https://cgv.co.kr/"
	loginURL         = "https://cgv.co.kr/mem/login?returnUrl=%2F"
	bookingCinemaURL = "https://cgv.co.kr/cnm/movieBook/cinema"
)

var (
	ErrAuthenticationRequired = errors.New("CGV authentication is required")
	ErrUIContractChanged      = errors.New("CGV UI contract changed")
	ErrCaptchaRequired        = errors.New("manual CAPTCHA entry is required")
	ErrProviderThrottled      = errors.New("CGV temporarily limited requests")
	ErrProviderAccessBlocked  = errors.New("CGV blocked the current request")
)

type BrowserConfig struct {
	ChromePath       string
	ProfileDir       string
	SessionStatePath string
	ArtifactsDir     string
	Headless         bool
	StartMinimized   bool
	RestoreSession   bool
	BlockResources   bool
	UserAgentMode    UserAgentMode
	Proxy            *BrowserProxy
	Capacity         int
	NetworkCapture   *networkcapture.Store
}

// BrowserProxy is the proxy identity assigned to one browser process. Secrets
// stay separate from Server so they are never embedded in URLs or logs.
type BrowserProxy struct {
	Server   string
	Username string
	Password string
}

const shadowDOMBootstrap = `(() => {
	const collect = (root, selector, result) => {
		if (!root || !root.querySelectorAll) return;
		for (const element of root.querySelectorAll(selector)) result.push(element);
		for (const element of root.querySelectorAll('*')) {
			if (element.shadowRoot) collect(element.shadowRoot, selector, result);
		}
	};
	window.__cinekoQueryAll = (selector, root = document) => {
		const result = [];
		collect(root, selector, result);
		return result;
	};
	window.__cinekoQuery = (selector, root = document) => window.__cinekoQueryAll(selector, root)[0] || null;
})();`

func DefaultBrowserConfig() BrowserConfig {
	return BrowserConfig{
		ProfileDir:     filepath.Join(".cineko", "chrome-profile"),
		ArtifactsDir:   filepath.Join(".cineko", "artifacts"),
		Headless:       true,
		BlockResources: true,
	}
}

type Adapter struct {
	ctx                context.Context
	cancelContext      context.CancelFunc
	owner              *Adapter
	browserContext     playwright.BrowserContext
	page               playwright.Page
	identitySession    playwright.CDPSession
	stopPlaywright     func() error
	processPID         int
	browserProcessPID  int
	hideUntilPayment   bool
	profileDir         string
	sessionStatePath   string
	processCrashed     chan error
	processDone        chan struct{}
	processDoneOnce    sync.Once
	processCrashOnce   sync.Once
	closeAttemptDone   chan struct{}
	closeAttemptOnce   sync.Once
	forceWait          chan struct{}
	forceWaitOnce      sync.Once
	processWaitOnce    sync.Once
	processWaitDone    chan struct{}
	processWaitErr     error
	fallbackWait       bool
	closing            atomic.Bool
	closeOnce          sync.Once
	lifecycleMu        sync.Mutex
	windowVisibilityMu sync.Mutex
	closeHooks         []func()
	closed             bool
	closeErr           error
	artifactsDir       string
	mu                 sync.Mutex
	selectedRegion     string
	selectedTheater    string
	preparedPayment    bool
	preparedCancel     bool
	blockedRequests    atomic.Uint64
	continuedRequests  atomic.Uint64
	blockResources     bool
	seatResponses      chan seatNetworkResponse
	scheduleResponseMu sync.Mutex
	providerResponses  []capturedProviderResponse
	networkStarts      sync.Map
	networkCompleted   sync.Map
	networkCapture     *networkcapture.Store
	rateLimit          *networkcapture.RateLimitGate
	rateLimitAutomated bool
	paymentHandoff     atomic.Bool
	userAgent          browserUserAgent
	userAgentMetadata  userAgentBootstrapIdentity
	webGLIdentity      webGLIdentity
}

type BrowserPool struct {
	config     BrowserConfig
	closeOnce  sync.Once
	mu         sync.Mutex
	closed     bool
	active     map[*Adapter]struct{}
	slot       chan struct{}
	playwright *playwright.Playwright
	openings   sync.WaitGroup
}

func NewBrowserPool(config BrowserConfig) (*BrowserPool, error) {
	config = normalizedBrowserConfig(config)
	if err := prepareBrowserDirectories(config); err != nil {
		return nil, err
	}
	pw, err := startPlaywright()
	if err != nil {
		return nil, fmt.Errorf("start Playwright: %w", err)
	}
	config.ChromePath, err = resolveBrowserExecutable(pw, config.ChromePath)
	if err != nil {
		_ = pw.Stop()
		return nil, err
	}
	capacity := config.Capacity
	if capacity < 1 {
		capacity = 1
	}
	pool := &BrowserPool{
		config:     config,
		active:     make(map[*Adapter]struct{}, capacity),
		slot:       make(chan struct{}, capacity),
		playwright: pw,
	}
	for range capacity {
		pool.slot <- struct{}{}
	}
	return pool, nil
}

func (pool *BrowserPool) NewAdapter(parent context.Context, config BrowserConfig) (*Adapter, error) {
	if parent == nil {
		return nil, errors.New("browser task context is required")
	}
	select {
	case <-parent.Done():
		return nil, parent.Err()
	case <-pool.slot:
	}
	releaseSlot := true
	defer func() {
		if releaseSlot {
			pool.slot <- struct{}{}
		}
	}()

	config = normalizedBrowserConfig(config)
	if config.ChromePath == "" {
		config.ChromePath = pool.config.ChromePath
	} else {
		var err error
		config.ChromePath, err = resolveBrowserExecutable(pool.playwright, config.ChromePath)
		if err != nil {
			return nil, err
		}
	}
	if config.ArtifactsDir == DefaultBrowserConfig().ArtifactsDir {
		config.ArtifactsDir = pool.config.ArtifactsDir
	}
	if err := prepareBrowserDirectories(config); err != nil {
		return nil, err
	}
	pool.mu.Lock()
	if pool.closed {
		pool.mu.Unlock()
		return nil, errors.New("browser pool is closed")
	}
	pool.openings.Add(1)
	pool.mu.Unlock()
	defer pool.openings.Done()

	// A logical automation task owns one Chrome process and its only page
	// target. Closing the adapter tears the process down, so no browser state,
	// handles, or renderer memory can accumulate across sequential tasks.
	adapter, err := newAdapter(parent, pool.playwright, config, nil)
	if err != nil {
		return nil, err
	}
	pool.mu.Lock()
	if pool.closed {
		pool.mu.Unlock()
		adapter.Close()
		return nil, errors.New("browser pool is closed")
	}
	pool.active[adapter] = struct{}{}
	pool.mu.Unlock()
	adapter.AddCloseHook(func() { pool.releaseAdapter(adapter) })
	releaseSlot = false
	return adapter, nil
}

func (pool *BrowserPool) releaseAdapter(adapter *Adapter) {
	pool.mu.Lock()
	if _, exists := pool.active[adapter]; exists {
		delete(pool.active, adapter)
		pool.slot <- struct{}{}
	}
	pool.mu.Unlock()
}

func (pool *BrowserPool) Close() {
	pool.closeOnce.Do(func() {
		pool.mu.Lock()
		pool.closed = true
		adapters := make([]*Adapter, 0, len(pool.active))
		for adapter := range pool.active {
			adapters = append(adapters, adapter)
		}
		pool.mu.Unlock()
		pool.openings.Wait()
		for _, adapter := range adapters {
			adapter.Close()
		}
		if pool.playwright != nil {
			_ = pool.playwright.Stop()
		}
	})
}

func NewAdapter(parent context.Context, config BrowserConfig) (*Adapter, error) {
	if parent == nil {
		return nil, errors.New("browser task context is required")
	}
	config = normalizedBrowserConfig(config)
	if err := prepareBrowserDirectories(config); err != nil {
		return nil, err
	}
	pw, err := startPlaywright()
	if err != nil {
		return nil, fmt.Errorf("start Playwright: %w", err)
	}
	config.ChromePath, err = resolveBrowserExecutable(pw, config.ChromePath)
	if err != nil {
		_ = pw.Stop()
		return nil, err
	}
	adapter, err := newAdapter(parent, pw, config, pw.Stop)
	if err != nil {
		_ = pw.Stop()
		return nil, err
	}
	return adapter, nil
}

//nolint:gocyclo,cyclop // Browser construction validates and owns one coupled Playwright identity session.
func newAdapter(
	parent context.Context,
	pw *playwright.Playwright,
	config BrowserConfig,
	stopPlaywright func() error,
) (*Adapter, error) {
	if pw == nil {
		return nil, errors.New("playwright runtime is required")
	}
	adapterContext, cancelContext := context.WithCancel(parent)
	persistedIdentity, err := loadSessionIdentity(config)
	if err != nil {
		cancelContext()
		return nil, err
	}
	var selectedUserAgent browserUserAgent
	if persistedIdentity != nil {
		selectedUserAgent = persistedIdentity.UserAgent
	} else {
		selectedUserAgent, err = selectBrowserUserAgent(config.ChromePath, config.UserAgentMode, nil)
		if err != nil {
			cancelContext()
			return nil, err
		}
	}
	locale := ""
	if persistedIdentity != nil {
		locale = persistedIdentity.Languages[0]
	} else if config.UserAgentMode == UserAgentSession {
		locale = profilePrimaryLanguage(config.ProfileDir)
	}
	options := persistentContextOptions(config, locale)
	browserContext, err := launchBrowserContext(pw, config, options)
	if err != nil {
		cancelContext()
		return nil, err
	}
	page, err := onlyBrowserPage(browserContext)
	if err != nil {
		_ = browserContext.Close()
		cancelContext()
		return nil, err
	}
	if persistedIdentity == nil {
		selectedUserAgent.Value, err = readNativeReducedUserAgent(page, selectedUserAgent.Major)
		if err != nil {
			_ = browserContext.Close()
			cancelContext()
			return nil, err
		}
	}
	identity, err := initializeBrowserIdentity(page, selectedUserAgent, persistedIdentity)
	if err != nil {
		_ = browserContext.Close()
		cancelContext()
		return nil, err
	}
	adapter := &Adapter{
		ctx: adapterContext, cancelContext: cancelContext, browserContext: browserContext, page: page,
		identitySession: identity.session,
		stopPlaywright:  stopPlaywright, artifactsDir: config.ArtifactsDir,
		processPID: pw.Pid(), profileDir: config.ProfileDir, sessionStatePath: config.SessionStatePath,
		hideUntilPayment: config.StartMinimized && !config.Headless,
		processCrashed:   make(chan error, 1), processDone: make(chan struct{}),
		closeAttemptDone: make(chan struct{}), forceWait: make(chan struct{}),
		processWaitDone:   make(chan struct{}),
		seatResponses:     make(chan seatNetworkResponse, 8),
		userAgent:         selectedUserAgent,
		userAgentMetadata: identity.metadata, webGLIdentity: identity.webGL,
		blockResources: config.BlockResources, networkCapture: config.NetworkCapture,
		rateLimit: networkcapture.NewRateLimitGate(), rateLimitAutomated: config.Headless || config.StartMinimized,
	}
	if adapter.networkCapture == nil {
		adapter.networkCapture, err = networkcapture.NewStore(filepath.Join(config.ArtifactsDir, "network"), logging.Logger(), networkcapture.WithDebug(logging.DebugEnabled()))
		if err != nil {
			adapter.Close()
			return nil, fmt.Errorf("initialize booking network capture: %w", err)
		}
	}
	if persistedIdentity == nil {
		if err := saveSessionIdentity(config, persistentBrowserIdentity{
			Version: sessionIdentityVersion, UserAgent: selectedUserAgent,
			Metadata: identity.metadata, Languages: identity.languages, WebGL: identity.webGL,
		}); err != nil {
			adapter.Close()
			return nil, err
		}
	}
	if err := adapter.installBrowserHooks(identity.scripts); err != nil {
		adapter.Close()
		return nil, err
	}
	if config.StartMinimized && !config.Headless {
		if err := adapter.minimizeBrowserWindow(); err != nil {
			adapter.Close()
			return nil, fmt.Errorf("minimize background booking browser: %w", err)
		}
	}
	go func() {
		<-adapterContext.Done()
		adapter.Close()
	}()
	return adapter, nil
}

func launchBrowserContext(
	pw *playwright.Playwright,
	config BrowserConfig,
	options playwright.BrowserTypeLaunchPersistentContextOptions,
) (playwright.BrowserContext, error) {
	browserContext, err := pw.Chromium.LaunchPersistentContext(config.ProfileDir, options)
	if err != nil {
		return nil, fmt.Errorf("launch Chrome with Playwright: %w", err)
	}
	if err := restoreSessionState(browserContext, config.SessionStatePath); err != nil {
		_ = browserContext.Close()
		return nil, err
	}
	return browserContext, nil
}

func onlyBrowserPage(browserContext playwright.BrowserContext) (playwright.Page, error) {
	pages := browserContext.Pages()
	if len(pages) == 0 {
		page, err := browserContext.NewPage()
		if err != nil {
			return nil, fmt.Errorf("create browser page: %w", err)
		}
		pages = []playwright.Page{page}
	}
	for _, extraPage := range pages[1:] {
		_ = extraPage.Close()
	}
	return pages[0], nil
}

type browserIdentitySetup struct {
	session   playwright.CDPSession
	webGL     webGLIdentity
	metadata  userAgentBootstrapIdentity
	languages []string
	scripts   []string
}

func initializeBrowserIdentity(
	page playwright.Page,
	userAgent browserUserAgent,
	persisted *persistentBrowserIdentity,
) (browserIdentitySetup, error) {
	var languages []string
	var webGL webGLIdentity
	var metadata userAgentBootstrapIdentity
	var err error
	if persisted == nil {
		languages, err = readNativeBrowserLanguages(page)
		if err != nil {
			return browserIdentitySetup{}, err
		}
		languages = languages[:1]
		webGL, err = readNativeWebGLIdentity(page)
		if err != nil {
			return browserIdentitySetup{}, err
		}
		metadata, err = readNativeUserAgentIdentity(page, userAgent)
		if err != nil {
			return browserIdentitySetup{}, err
		}
	} else {
		languages = append([]string(nil), persisted.Languages...)
		webGL = persisted.WebGL
		metadata = persisted.Metadata
	}
	userAgentScript, err := browserUserAgentBootstrap(metadata)
	if err != nil {
		return browserIdentitySetup{}, err
	}
	session, err := openBrowserIdentitySession(page, userAgent, metadata)
	if err != nil {
		return browserIdentitySetup{}, err
	}
	localizedStealth, err := stealthBootstrapForIdentity(languages, webGL)
	if err != nil {
		_ = session.Detach()
		return browserIdentitySetup{}, fmt.Errorf("configure browser stealth: %w", err)
	}
	return browserIdentitySetup{
		session:   session,
		webGL:     webGL,
		metadata:  metadata,
		languages: languages,
		scripts: []string{
			localizedStealth, chromeStealthBootstrap, userAgentScript, cinekoStealthOverrides, shadowDOMBootstrap,
		},
	}, nil
}

func (adapter *Adapter) installBrowserHooks(scripts []string) error {
	for _, script := range scripts {
		if err := adapter.browserContext.AddInitScript(
			playwright.Script{Content: playwright.String(script)},
		); err != nil {
			return fmt.Errorf("install browser init script: %w", err)
		}
	}
	if err := adapter.browserContext.Route("**/*", adapter.routeRequest); err != nil {
		return fmt.Errorf("install browser resource routing: %w", err)
	}
	adapter.browserContext.OnResponse(adapter.handleResponse)
	adapter.browserContext.OnResponse(adapter.observeRateLimitResponse)
	adapter.browserContext.OnRequestFinished(func(request playwright.Request) {
		adapter.captureNetworkExchange(request, false)
	})
	adapter.browserContext.OnRequestFailed(func(request playwright.Request) {
		adapter.observeRateLimitFailure(request)
		adapter.captureNetworkExchange(request, true)
	})
	adapter.browserContext.OnRequestFailed(adapter.handleRequestFailed)
	if adapter.hideUntilPayment {
		adapter.browserContext.OnPage(func(playwright.Page) {
			if err := adapter.ensureBackgroundBrowserHidden(); err != nil {
				logging.ErrorUnexpected(adapter.ctx, "cgv.booking.window.repark.failed",
					"booking_monitoring", "repark_booking_window",
					"new background tabs remain minimized and off-screen until payment",
					"a new browser tab may have become visible", err)
			}
		})
	}
	adapter.installPageHooks()
	adapter.browserContext.OnClose(func(playwright.BrowserContext) {
		if adapter.closing.Load() {
			return
		}
		adapter.processCrashOnce.Do(func() {
			adapter.processCrashed <- errors.New("CGV browser context closed unexpectedly")
		})
	})
	for _, script := range scripts {
		if _, err := adapter.page.Evaluate(script); err != nil {
			return fmt.Errorf("initialize browser page: %w", err)
		}
	}
	return nil
}

// OpenTab creates an independently synchronized booking tab in the same
// persistent, authenticated browser context. Network routing and init scripts
// remain context-wide while provider-response correlation stays page-local.
func (adapter *Adapter) OpenTab(parent context.Context) (*Adapter, error) {
	if adapter == nil || adapter.owner != nil || adapter.browserContext == nil || parent == nil {
		return nil, errors.New("booking tab owner and context are required")
	}
	if adapter.closing.Load() {
		return nil, errors.New("booking browser is closing")
	}
	ctx, cancel := context.WithCancel(parent)
	page, err := adapter.browserContext.NewPage()
	if err != nil {
		cancel()
		return nil, fmt.Errorf("create booking tab: %w", err)
	}
	identitySession, err := openBrowserIdentitySession(page, adapter.userAgent, adapter.userAgentMetadata)
	if err != nil {
		_ = page.Close()
		cancel()
		return nil, err
	}
	tab := &Adapter{
		ctx: ctx, cancelContext: cancel, owner: adapter,
		browserContext: adapter.browserContext, page: page, identitySession: identitySession,
		artifactsDir: adapter.artifactsDir, seatResponses: make(chan seatNetworkResponse, 8),
		networkCapture: adapter.networkCapture,
		rateLimit:      adapter.rateLimit, rateLimitAutomated: true,
		userAgent: adapter.userAgent, userAgentMetadata: adapter.userAgentMetadata,
		webGLIdentity: adapter.webGLIdentity, blockResources: adapter.blockResources,
	}
	tab.installPageHooks()
	if err := adapter.ensureBackgroundBrowserHidden(); err != nil {
		_ = identitySession.Detach()
		_ = page.Close()
		cancel()
		return nil, fmt.Errorf("keep booking browser hidden after opening tab: %w", err)
	}
	page.OnClose(func(playwright.Page) { cancel() })
	go func() {
		<-ctx.Done()
		tab.Close()
	}()
	logging.Info(ctx, "cgv.booking.tab.opened",
		"event", "cgv.booking.tab.opened", "scenario", "booking_monitoring",
		"operation", "open_watcher_tab", "outcome", "succeeded",
		"browser_page_count", len(adapter.browserContext.Pages()))
	return tab, nil
}

func (adapter *Adapter) installPageHooks() {
	if adapter == nil || adapter.page == nil {
		return
	}
	adapter.page.OnResponse(adapter.captureProviderResponse)
	adapter.page.OnResponse(adapter.handleSeatResponse)
	root := adapter.rootAdapter()
	if root != nil && root.hideUntilPayment {
		adapter.page.OnFrameNavigated(func(frame playwright.Frame) {
			if frame == adapter.page.MainFrame() {
				adapter.rehideAfterBrowserEvent("main_frame_navigated")
			}
		})
		adapter.page.OnLoad(func(playwright.Page) {
			adapter.rehideAfterBrowserEvent("page_loaded")
		})
	}
}

func (adapter *Adapter) rehideAfterBrowserEvent(operation string) {
	root := adapter.rootAdapter()
	if root == nil || root.closing.Load() || root.paymentHandoff.Load() {
		return
	}
	go func() {
		if err := root.ensureBackgroundBrowserHidden(); err != nil && !root.closing.Load() {
			logging.ErrorUnexpected(root.ctx, "cgv.booking.window.repark.failed",
				"booking_monitoring", operation,
				"background Chrome remains hidden until payment",
				"browser navigation may have made Chrome visible", err)
		}
	}()
}

func (adapter *Adapter) handleSeatResponse(response playwright.Response) {
	if response == nil || !strings.Contains(response.URL(), seatDataPath) {
		return
	}
	if response.Status() < 200 || response.Status() > 299 {
		adapter.publishSeatResponse(seatNetworkResponse{err: providerHTTPError(response.Status())})
		return
	}
	go func() {
		body, err := response.Body()
		adapter.publishSeatResponse(seatNetworkResponse{body: body, err: err})
	}()
}

func readNativeBrowserLanguages(page playwright.Page) ([]string, error) {
	value, err := page.Evaluate(`Array.from(navigator.languages || [navigator.language]).filter(Boolean)`)
	if err != nil {
		return nil, fmt.Errorf("read browser languages: %w", err)
	}
	encoded, err := json.Marshal(value)
	if err != nil {
		return nil, fmt.Errorf("encode browser languages: %w", err)
	}
	var languages []string
	if err := json.Unmarshal(encoded, &languages); err != nil {
		return nil, fmt.Errorf("decode browser languages: %w", err)
	}
	unique := make([]string, 0, len(languages))
	seen := make(map[string]struct{}, len(languages))
	for _, language := range languages {
		language = strings.TrimSpace(language)
		if language == "" {
			continue
		}
		key := strings.ToLower(language)
		if _, exists := seen[key]; exists {
			continue
		}
		seen[key] = struct{}{}
		unique = append(unique, language)
	}
	if len(unique) == 0 {
		return nil, errors.New("browser returned no languages")
	}
	return unique, nil
}

func profilePrimaryLanguage(profileDir string) string {
	contents, err := os.ReadFile(filepath.Join(profileDir, "Default", "Preferences")) // #nosec G304 -- app profile path.
	if err != nil {
		return ""
	}
	var preferences struct {
		Intl struct {
			AcceptLanguages string `json:"accept_languages"`
		} `json:"intl"`
	}
	if json.Unmarshal(contents, &preferences) != nil {
		return ""
	}
	primary, _, _ := strings.Cut(preferences.Intl.AcceptLanguages, ",")
	return strings.TrimSpace(primary)
}

func normalizedBrowserConfig(config BrowserConfig) BrowserConfig {
	defaults := DefaultBrowserConfig()
	if config.ProfileDir == "" {
		config.ProfileDir = defaults.ProfileDir
	}
	if config.ArtifactsDir == "" {
		config.ArtifactsDir = defaults.ArtifactsDir
	}
	if config.UserAgentMode == "" {
		config.UserAgentMode = UserAgentSession
	}
	return config
}

func prepareBrowserDirectories(config BrowserConfig) error {
	for _, directory := range []string{config.ProfileDir, config.ArtifactsDir} {
		if err := os.MkdirAll(directory, 0o700); err != nil {
			return fmt.Errorf("create browser directory: %w", err)
		}
	}
	return nil
}

func persistentContextOptions(
	config BrowserConfig,
	locale string,
) playwright.BrowserTypeLaunchPersistentContextOptions {
	position := "--window-position=80,80"
	if config.StartMinimized && !config.Headless {
		position = "--window-position=-32000,-32000"
	}
	options := playwright.BrowserTypeLaunchPersistentContextOptions{
		ExecutablePath:    playwright.String(config.ChromePath),
		Headless:          playwright.Bool(config.Headless),
		IgnoreDefaultArgs: []string{"--enable-automation"},
		Args: []string{
			"--disable-blink-features=AutomationControlled",
			"--disable-background-timer-throttling",
			"--disable-backgrounding-occluded-windows",
			"--disable-renderer-backgrounding",
			"--window-size=1440,1100",
			position,
		},
		TimezoneId:     playwright.String("Asia/Seoul"),
		ServiceWorkers: playwright.ServiceWorkerPolicyBlock,
		Screen:         &playwright.Size{Width: 1440, Height: 1100},
		Viewport:       &playwright.Size{Width: 1440, Height: 1100},
	}
	if config.StartMinimized && !config.Headless {
		options.Args = append(options.Args, "--start-minimized")
	}
	if config.RestoreSession {
		options.Args = append(options.Args, "--restore-last-session")
		options.IgnoreDefaultArgs = append(options.IgnoreDefaultArgs, "--no-startup-window")
	}
	if locale != "" {
		options.Locale = playwright.String(locale)
	}
	if config.Proxy != nil {
		options.Proxy = &playwright.Proxy{
			Server:   config.Proxy.Server,
			Username: optionalString(config.Proxy.Username),
			Password: optionalString(config.Proxy.Password),
		}
	}
	return options
}

func optionalString(value string) *string {
	if value == "" {
		return nil
	}
	return playwright.String(value)
}

var thirdPartyBlocklist = []string{
	"google-analytics.com",
	"googletagmanager.com",
	"doubleclick.net",
	"googlesyndication.com",
	"adservice.google.",
	"connect.facebook.net",
	"facebook.com/tr",
	"analytics",
	"tracking",
	"/pixel",
	"pixel.",
}

func shouldBlockResource(requestURL, resourceType string) bool {
	lowerURL := strings.ToLower(requestURL)
	for _, pattern := range thirdPartyBlocklist {
		if strings.Contains(lowerURL, pattern) {
			return true
		}
	}
	switch strings.ToLower(resourceType) {
	case "document", "xhr", "fetch":
		return false
	case "script":
		parsed, err := url.Parse(requestURL)
		if err != nil {
			return true
		}
		host := strings.ToLower(parsed.Hostname())
		return host != "cgv.co.kr" && !strings.HasSuffix(host, ".cgv.co.kr")
	default:
		return true
	}
}

func (adapter *Adapter) routeRequest(route playwright.Route) {
	request := route.Request()
	requestID := browserRequestID(request)
	started := time.Now()
	adapter.networkStarts.Store(requestID, started)
	fields := browserRequestFields(request, requestID)
	logging.Debug(adapter.ctx, "http.client.request.attempted", fields...)
	if adapter.blockResources && shouldBlockResource(request.URL(), request.ResourceType()) {
		adapter.blockedRequests.Add(1)
		err := route.Abort("blockedbyclient")
		if err != nil {
			fields = append(fields, "duration_ms", browserDurationMs(started), "status", 0, "error", fmt.Sprintf("%+v", err))
		} else {
			fields = append(fields, "duration_ms", browserDurationMs(started), "status", 0, "error", "blockedbyclient")
		}
		adapter.completeBrowserRequest(requestID, fields...)
		return
	}
	if adapter.rateLimitAutomated && !adapter.paymentHandoff.Load() {
		if allowed, decision := adapter.rateLimit.Allow(browserRequestHost(request.URL())); !allowed {
			adapter.blockedRequests.Add(1)
			fields = append(fields, "duration_ms", browserDurationMs(started), "status", 0,
				"retry_at", decision.BlockedUntil, "retry_after_ms", decision.Delay.Milliseconds(),
				"error", "provider rate limit circuit is open")
			adapter.completeBrowserRequest(requestID, fields...)
			_ = route.Abort("blockedbyclient")
			return
		}
	}
	adapter.continuedRequests.Add(1)
	headers, err := request.AllHeaders()
	if err != nil {
		// A request header read can fail for a short-lived browser request.  We
		// still propagate the correlation header through a fresh map so the
		// response/failure callback can join the attempt event.
		headers = make(map[string]string)
	}
	applyUserAgentHeaders(headers, adapter.userAgent, adapter.userAgentMetadata)
	headers[logging.RequestIDHeader] = requestID
	if err := route.Continue(playwright.RouteContinueOptions{Headers: headers}); err != nil {
		fields = append(fields, "duration_ms", browserDurationMs(started), "status", 0, "error", fmt.Sprintf("%+v", err))
		adapter.completeBrowserRequest(requestID, fields...)
		return
	}
}

func (adapter *Adapter) handleResponse(response playwright.Response) {
	request := response.Request()
	requestID := browserRequestID(request)
	fields := browserRequestFields(request, requestID)
	fields = append(fields,
		"status", response.Status(),
		"duration_ms", browserResponseDurationMs(request),
	)
	if response.Status() >= http.StatusBadRequest {
		fields = append(fields, "error", http.StatusText(response.Status()))
	}
	if request != nil {
		if sizes, err := request.Sizes(); err == nil && sizes != nil {
			fields = append(fields, "response_bytes", sizes.ResponseBodySize)
		}
	}
	adapter.completeBrowserRequest(requestID, fields...)
}

func (adapter *Adapter) captureNetworkExchange(request playwright.Request, failed bool) {
	if adapter == nil || adapter.networkCapture == nil || request == nil {
		return
	}
	if !playwrightcapture.ShouldCapturePlaywrightRequest(adapter.networkCapture, request, failed) {
		return
	}
	record := playwrightcapture.PlaywrightRecord(request, failed)
	record.Service = "client"
	record.Scenario = "booking_browser"
	record.CorrelationID = browserRequestID(request)
	if _, err := adapter.networkCapture.Save(context.WithoutCancel(adapter.ctx), record); err != nil {
		logging.ErrorUnexpected(adapter.ctx, "browser.network.capture.failed", "network", "capture_booking_exchange",
			"complete browser request and response artifact", "network artifact write failed", err,
			"request_id", record.CorrelationID, "method", record.Request.Method, "request_url", record.Request.URL)
	}
}

func (adapter *Adapter) observeRateLimitResponse(response playwright.Response) {
	if adapter == nil || adapter.rateLimit == nil || response == nil || !adapter.rateLimitAutomated || adapter.paymentHandoff.Load() {
		return
	}
	host := browserRequestHost(response.URL())
	if response.Status() != http.StatusTooManyRequests {
		if adapter.rateLimit.ObserveSuccess(host) {
			logging.Info(adapter.ctx, "browser.network.rate_limit.closed", "event", "browser.network.rate_limit.closed",
				"scenario", "booking_monitoring", "request_url", response.URL(), "status", response.Status(), "outcome", "recovered")
		}
		return
	}
	headers, _ := response.HeadersArray()
	decision := adapter.rateLimit.Observe429(host, playwrightcapture.PlaywrightHeaders(headers))
	logging.ErrorUnexpected(adapter.ctx, "browser.network.rate_limit.opened", "booking_monitoring", "observe_provider_response",
		"provider request below its rate limit", "HTTP 429 opened the local circuit", ErrProviderThrottled,
		"request_url", response.URL(), "status", response.Status(), "retry_at", decision.BlockedUntil,
		"retry_after_ms", decision.Delay.Milliseconds(), "rate_limit_source", decision.Source,
		"rate_limit_failures", decision.Failures)
}

func (adapter *Adapter) observeRateLimitFailure(request playwright.Request) {
	if adapter == nil || adapter.rateLimit == nil || request == nil || !adapter.rateLimitAutomated || adapter.paymentHandoff.Load() {
		return
	}
	decision, observed := adapter.rateLimit.ObserveFailure(browserRequestHost(request.URL()))
	if !observed {
		return
	}
	logging.WarnUnexpected(adapter.ctx, "browser.network.rate_limit.half_open_failed", "booking_monitoring", "probe_provider_rate_limit",
		"one successful half-open provider request", "half-open request failed before a response",
		"request_url", request.URL(), "retry_at", decision.BlockedUntil,
		"retry_after_ms", decision.Delay.Milliseconds(), "rate_limit_failures", decision.Failures)
}

func (adapter *Adapter) handleRequestFailed(request playwright.Request) {
	requestID := browserRequestID(request)
	err := request.Failure()
	if err == nil {
		err = errors.New("browser request failed")
	}
	// routeRequest already records client-side resource blocks synchronously;
	// Playwright may also emit requestfailed for the same abort without the
	// injected header, so avoid a second uncorrelated completion event.
	if strings.EqualFold(strings.TrimSpace(err.Error()), "blockedbyclient") {
		return
	}
	fields := browserRequestFields(request, requestID)
	fields = append(fields,
		"status", 0,
		"duration_ms", browserResponseDurationMs(request),
		"error", fmt.Sprintf("%+v", err),
	)
	adapter.completeBrowserRequest(requestID, fields...)
}

func browserRequestID(request playwright.Request) string {
	if request != nil {
		if requestID, err := request.HeaderValue(logging.RequestIDHeader); err == nil && strings.TrimSpace(requestID) != "" {
			return strings.TrimSpace(requestID)
		}
	}
	return logging.NewRequestID()
}

func browserRequestPath(rawURL string) string {
	parsed, err := url.Parse(rawURL)
	if err != nil || parsed.Path == "" {
		return "/"
	}
	return parsed.Path
}

func browserRequestHost(rawURL string) string {
	parsed, err := url.Parse(rawURL)
	if err != nil {
		return rawURL
	}
	return parsed.Hostname()
}

func browserRequestFields(request playwright.Request, requestID string) []any {
	method, rawURL, resourceType := http.MethodGet, "", ""
	if request != nil {
		method = strings.TrimSpace(request.Method())
		rawURL = request.URL()
		resourceType = request.ResourceType()
	}
	if method == "" {
		method = http.MethodGet
	}
	path := browserRequestPath(rawURL)
	fields := []any{
		"request_id", requestID,
		"method", method,
		"route", path,
		"path", path,
	}
	if resourceType != "" {
		fields = append(fields, "resource_type", resourceType)
	}
	if request != nil {
		if body, err := request.PostDataBuffer(); err == nil && body != nil {
			fields = append(fields, "request_bytes", len(body))
		}
	}
	return fields
}

func browserDurationMs(started time.Time) float64 {
	if started.IsZero() {
		return 0
	}
	return float64(time.Since(started).Microseconds()) / 1000
}

func browserResponseDurationMs(request playwright.Request) float64 {
	if request == nil {
		return 0
	}
	if started, ok := requestTimingStart(request); ok {
		return browserDurationMs(started)
	}
	timing := request.Timing()
	if timing == nil {
		return 0
	}
	for _, duration := range []float64{timing.ResponseEnd, timing.ResponseStart} {
		if duration >= 0 {
			return duration
		}
	}
	return 0
}

func requestTimingStart(request playwright.Request) (time.Time, bool) {
	// Playwright's Timing values are relative to a wall-clock Unix epoch.  A
	// local start is more reliable for callbacks that arrive after the route
	// command, so this helper is intentionally a no-op for now; routeRequest
	// duration is retained in networkStarts and consumed by completion below.
	return time.Time{}, false
}

func (adapter *Adapter) completeBrowserRequest(requestID string, fields ...any) {
	if requestID == "" {
		requestID = logging.NewRequestID()
	}
	if _, alreadyCompleted := adapter.networkCompleted.LoadOrStore(requestID, struct{}{}); alreadyCompleted {
		return
	}
	time.AfterFunc(time.Minute, func() { adapter.networkCompleted.Delete(requestID) })
	if started, ok := adapter.networkStarts.LoadAndDelete(requestID); ok {
		if value, ok := started.(time.Time); ok {
			fields = replaceBrowserDuration(fields, browserDurationMs(value))
		}
	}
	if outcome := expectedBrowserRequestOutcome(fields); outcome != "" {
		logging.Debug(adapter.ctx, "http.client.request.completed", append(fields, "outcome", outcome)...)
		return
	}
	if browserRequestFailed(fields) {
		logging.Error(adapter.ctx, "http.client.request.completed", fields...)
		return
	}
	logging.Debug(adapter.ctx, "http.client.request.completed", fields...)
}

func expectedBrowserRequestOutcome(fields []any) string {
	for index := 0; index+1 < len(fields); index += 2 {
		if fields[index] != "status" {
			continue
		}
		switch status := fields[index+1].(type) {
		case int:
			if status == http.StatusUnauthorized {
				return "unauthenticated"
			}
		case int32:
			if status == http.StatusUnauthorized {
				return "unauthenticated"
			}
		}
	}
	for index := 0; index+1 < len(fields); index += 2 {
		if fields[index] != "error" {
			continue
		}
		reason := strings.ToUpper(strings.TrimSpace(fmt.Sprint(fields[index+1])))
		switch {
		case strings.Contains(reason, "BLOCKEDBYCLIENT"), strings.Contains(reason, "ERR_BLOCKED_BY_CLIENT"):
			return "blocked"
		case strings.Contains(reason, "ERR_ABORTED"):
			return "canceled"
		}
	}
	return ""
}

func browserRequestFailed(fields []any) bool {
	for index := 0; index+1 < len(fields); index += 2 {
		switch fields[index] {
		case "error":
			return fmt.Sprint(fields[index+1]) != ""
		case "status":
			switch status := fields[index+1].(type) {
			case int:
				if status >= http.StatusBadRequest {
					return true
				}
			case int32:
				if status >= http.StatusBadRequest {
					return true
				}
			}
		}
	}
	return false
}

func replaceBrowserDuration(fields []any, duration float64) []any {
	for index := 0; index+1 < len(fields); index += 2 {
		if fields[index] == "duration_ms" {
			fields[index+1] = duration
			return fields
		}
	}
	return append(fields, "duration_ms", duration)
}

func providerHTTPError(status int) error {
	switch status {
	case 403:
		return fmt.Errorf("%w: HTTP %d", ErrProviderAccessBlocked, status)
	case 429:
		return fmt.Errorf("%w: HTTP %d", ErrProviderThrottled, status)
	default:
		return fmt.Errorf("CGV provider response returned HTTP %d", status)
	}
}

func (adapter *Adapter) publishSeatResponse(response seatNetworkResponse) {
	select {
	case adapter.seatResponses <- response:
	default:
	}
}

// Close stops the adapter and keeps the existing fire-and-forget automation
// contract. Process owners that must preserve lifecycle errors should call
// CloseWithError instead.
func (adapter *Adapter) Close() {
	_ = adapter.CloseWithError()
}

// CloseWithError stops the adapter and returns browser-context or Playwright
// transport errors to the owning process lifecycle.
func (adapter *Adapter) CloseWithError() error {
	if adapter == nil {
		return nil
	}
	if adapter.owner != nil {
		adapter.closeOnce.Do(func() {
			adapter.closing.Store(true)
			if adapter.cancelContext != nil {
				adapter.cancelContext()
			}
			var closeErr error
			if adapter.identitySession != nil {
				closeErr = errors.Join(closeErr, adapter.identitySession.Detach())
			}
			if adapter.page != nil {
				closeErr = errors.Join(closeErr, adapter.page.Close())
			}
			adapter.lifecycleMu.Lock()
			adapter.closeErr = closeErr
			adapter.closed = true
			adapter.lifecycleMu.Unlock()
			logging.Info(context.Background(), "cgv.booking.tab.closed",
				"event", "cgv.booking.tab.closed", "scenario", "booking_monitoring",
				"operation", "close_watcher_tab", "outcome", "completed")
		})
		adapter.lifecycleMu.Lock()
		defer adapter.lifecycleMu.Unlock()
		return adapter.closeErr
	}
	adapter.closeOnce.Do(func() {
		adapter.closing.Store(true)
		if adapter.cancelContext != nil {
			adapter.cancelContext()
		}
		var closeErr error
		if adapter.identitySession != nil {
			closeErr = errors.Join(closeErr, adapter.identitySession.Detach())
		}
		if adapter.browserContext != nil {
			closeErr = errors.Join(closeErr, adapter.browserContext.Close())
		}
		var stopErr error
		if adapter.stopPlaywright != nil {
			stopErr = adapter.stopPlaywright()
			closeErr = errors.Join(closeErr, stopErr)
		}
		fallbackWait := false
		if stopErr != nil && adapter.processPID > 0 {
			state, stateErr := rootProcessState(adapter.processPID)
			closeErr = errors.Join(closeErr, stateErr)
			// A transport error is not proof that cmd.Wait was skipped: Playwright
			// can report a driver's exit after it has already reaped the child.
			// Only a known-reaped child may skip forced cleanup; an unknown state
			// must fail closed through the bounded kill-and-wait path.
			fallbackWait = state != rootProcessReaped
		}
		adapter.lifecycleMu.Lock()
		adapter.closeErr = closeErr
		adapter.fallbackWait = fallbackWait
		adapter.closed = true
		adapter.lifecycleMu.Unlock()
		if adapter.closeAttemptDone != nil {
			adapter.closeAttemptOnce.Do(func() { close(adapter.closeAttemptDone) })
		}
		if !fallbackWait {
			adapter.markProcessReaped(nil)
		}
	})
	adapter.lifecycleMu.Lock()
	defer adapter.lifecycleMu.Unlock()
	return adapter.closeErr
}

// markProcessReaped records that the driver has been reaped and runs deferred
// profile/resource hooks exactly once. It is called by the Playwright stop
// owner or by the explicit fallback wait after a forced tree kill.
func (adapter *Adapter) markProcessReaped(waitErr error) {
	if adapter == nil {
		return
	}
	if adapter.processDone != nil {
		adapter.processDoneOnce.Do(func() { close(adapter.processDone) })
	}
	adapter.lifecycleMu.Lock()
	if waitErr != nil && adapter.processWaitErr == nil {
		adapter.processWaitErr = waitErr
	}
	hooks := append([]func(){}, adapter.closeHooks...)
	adapter.closeHooks = nil
	adapter.lifecycleMu.Unlock()
	for _, hook := range hooks {
		hook()
	}
}

// AddCloseHook registers resource cleanup after Chrome has stopped. If Close
// already won the race, the hook runs synchronously instead of being lost.
func (adapter *Adapter) AddCloseHook(hook func()) {
	if hook == nil {
		return
	}
	adapter.lifecycleMu.Lock()
	if !adapter.closed || adapter.processDoneOpenLocked() {
		adapter.closeHooks = append(adapter.closeHooks, hook)
		adapter.lifecycleMu.Unlock()
		return
	}
	adapter.lifecycleMu.Unlock()
	hook()
}

func (adapter *Adapter) processDoneOpenLocked() bool {
	if adapter.processDone == nil {
		return false
	}
	select {
	case <-adapter.processDone:
		return false
	default:
		return true
	}
}

func (adapter *Adapter) Capture(label string) (string, error) {
	adapter.mu.Lock()
	defer adapter.mu.Unlock()
	return adapter.captureUnlocked(label)
}

func (adapter *Adapter) captureUnlocked(label string) (string, error) {
	screenshot, err := adapter.page.Screenshot(playwright.PageScreenshotOptions{
		FullPage: playwright.Bool(true),
		Type:     playwright.ScreenshotTypePng,
	})
	if err != nil {
		return "", err
	}
	label = strings.Map(func(character rune) rune {
		if character >= 'a' && character <= 'z' || character >= 'A' && character <= 'Z' ||
			character >= '0' && character <= '9' || character == '-' || character == '_' {
			return character
		}
		return '-'
	}, label)
	path := filepath.Join(
		adapter.artifactsDir,
		fmt.Sprintf("%s-%s.png", time.Now().Format("20060102T150405.000"), label),
	)
	if err := os.WriteFile(path, screenshot, 0o600); err != nil {
		return "", err
	}
	return path, nil
}

func (adapter *Adapter) navigate(url string) error {
	if err := adapter.ctx.Err(); err != nil {
		return err
	}
	if _, err := adapter.page.Goto(url, playwright.PageGotoOptions{
		WaitUntil: playwright.WaitUntilStateDomcontentloaded,
	}); err != nil {
		return err
	}
	if err := adapter.page.Locator("body").WaitFor(playwright.LocatorWaitForOptions{
		State: playwright.WaitForSelectorStateAttached,
	}); err != nil {
		return err
	}
	return adapter.wait(700 * time.Millisecond)
}

func (adapter *Adapter) evaluate(expression string, output any) error {
	if err := adapter.ctx.Err(); err != nil {
		return err
	}
	value, err := adapter.page.Evaluate(expression)
	if err != nil || output == nil {
		return err
	}
	encoded, err := json.Marshal(value)
	if err != nil {
		return err
	}
	return json.Unmarshal(encoded, output)
}

func (adapter *Adapter) wait(duration time.Duration) error {
	timer := time.NewTimer(duration)
	defer timer.Stop()
	select {
	case <-adapter.ctx.Done():
		return adapter.ctx.Err()
	case <-timer.C:
		return nil
	}
}

func (adapter *Adapter) clickButtonExact(label string) (bool, error) {
	expression := fmt.Sprintf(`(() => {
		const expected = %s;
		const normalize = value => (value || '').replace(/\s+/g, ' ').trim();
		const element = window.__cinekoQueryAll('button')
			.find(button => !button.disabled && normalize(button.innerText || button.textContent) === expected);
		if (!element) return false;
		element.scrollIntoView({block: 'center'});
		element.click();
		return true;
	})()`, jsString(label))
	var clicked bool
	err := adapter.evaluate(expression, &clicked)
	return clicked, err
}

func (adapter *Adapter) clickButtonPrefix(prefix string) (bool, error) {
	expression := fmt.Sprintf(`(() => {
		const expected = %s;
		const normalize = value => (value || '').replace(/\s+/g, ' ').trim();
		const element = window.__cinekoQueryAll('button')
			.find(button => !button.disabled && normalize(button.innerText || button.textContent).startsWith(expected));
		if (!element) return false;
		element.scrollIntoView({block: 'center'});
		element.click();
		return true;
	})()`, jsString(prefix))
	var clicked bool
	err := adapter.evaluate(expression, &clicked)
	return clicked, err
}

func (adapter *Adapter) clickButtonMatching(pattern string) (bool, error) {
	expression := fmt.Sprintf(`(() => {
		const matcher = new RegExp(%s);
		const normalize = value => (value || '').replace(/\s+/g, ' ').trim();
		const element = window.__cinekoQueryAll('button')
			.find(button => !button.disabled && matcher.test(normalize(button.innerText || button.textContent)));
		if (!element) return false;
		element.scrollIntoView({block: 'center'});
		element.click();
		return true;
	})()`, jsString(pattern))
	var clicked bool
	err := adapter.evaluate(expression, &clicked)
	return clicked, err
}

func (adapter *Adapter) buttonExists(label string) (bool, error) {
	expression := fmt.Sprintf(`(() => {
		const expected = %s;
		const normalize = value => (value || '').replace(/\s+/g, ' ').trim();
		return window.__cinekoQueryAll('button')
			.some(button => normalize(button.innerText || button.textContent) === expected);
	})()`, jsString(label))
	var exists bool
	err := adapter.evaluate(expression, &exists)
	return exists, err
}

func (adapter *Adapter) bodyContains(text string) (bool, error) {
	expression := fmt.Sprintf(
		`document.body ? (document.body.innerText || '').includes(%s) : false`,
		jsString(text),
	)
	var contains bool
	err := adapter.evaluate(expression, &contains)
	return contains, err
}

func jsString(value string) string {
	encoded, _ := json.Marshal(value)
	return string(encoded)
}
