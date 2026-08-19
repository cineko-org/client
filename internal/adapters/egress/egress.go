package egress

import (
	"context"
	cryptorand "crypto/rand"
	"errors"
	"fmt"
	"io"
	"math/big"
	"net"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	defaultSessionTTL = 30 * time.Minute
	minimumSessionTTL = 5 * time.Minute
	maximumSessionTTL = 24 * time.Hour
)

var (
	ErrNoProxyCapacity = errors.New("no egress proxy is currently available")
	ErrLeaseRenewal    = errors.New("egress proxy lease renewal failed")
)

type Purpose string

const (
	PurposeSession Purpose = "session"
	PurposeScan    Purpose = "scan"
)

type Proxy struct {
	Server   string
	Username string
	Password string
}

type Config struct {
	SoxyURL    string
	SoxyToken  string
	SessionTTL time.Duration
	Proxies    []Proxy
	// ScanProxies is retained for environment compatibility. New callers use
	// Proxies, which applies one stable selection to either logical purpose.
	ScanProxies      []Proxy
	HTTPClient       *http.Client
	Random           io.Reader
	RenewInterval    time.Duration
	MaxRenewFailures int
	Probe            func(context.Context, Proxy) error
}

type Manager struct {
	client            *soxyClient
	sessionTTL        time.Duration
	proxies           []Proxy
	legacyScanProxies []Proxy
	random            io.Reader
	renewInterval     time.Duration
	maxRenewFailures  int
}

func NewFromEnvironment() (*Manager, error) {
	config, err := ConfigFromEnvironment()
	if err != nil {
		return nil, err
	}
	return New(config)
}

// ConfigFromEnvironment reads the optional CLI and managed-deployment egress settings.
func ConfigFromEnvironment() (Config, error) {
	return configFromLookup(os.LookupEnv)
}

func New(config Config) (*Manager, error) {
	config.SoxyURL = strings.TrimSpace(config.SoxyURL)
	config.SoxyToken = strings.TrimSpace(config.SoxyToken)
	if (config.SoxyURL == "") != (config.SoxyToken == "") {
		return nil, errors.New("configure Soxy URL and API token together")
	}
	proxies, legacyScanProxies, err := configuredProxyPools(config)
	if err != nil {
		return nil, err
	}
	if config.SessionTTL == 0 {
		config.SessionTTL = defaultSessionTTL
	}
	if config.SessionTTL < minimumSessionTTL || config.SessionTTL > maximumSessionTTL {
		return nil, fmt.Errorf("soxy session TTL must be between %s and %s", minimumSessionTTL, maximumSessionTTL)
	}
	if config.Random == nil {
		config.Random = cryptorand.Reader
	}
	if config.MaxRenewFailures == 0 {
		config.MaxRenewFailures = 3
	}
	if config.MaxRenewFailures < 1 {
		return nil, errors.New("maximum lease renewal failures must be positive")
	}
	if config.RenewInterval == 0 {
		config.RenewInterval = config.SessionTTL / 2
	}
	if config.RenewInterval < 0 {
		return nil, errors.New("lease renewal interval cannot be negative")
	}

	manager := &Manager{
		sessionTTL:        config.SessionTTL,
		proxies:           proxies,
		legacyScanProxies: legacyScanProxies,
		random:            config.Random,
		renewInterval:     config.RenewInterval,
		maxRenewFailures:  config.MaxRenewFailures,
	}
	if config.SoxyURL != "" {
		client, err := newSoxyClient(config.SoxyURL, config.SoxyToken, config.HTTPClient)
		if err != nil {
			return nil, err
		}
		manager.client = client
	}
	return manager, nil
}

func configuredProxyPools(config Config) ([]Proxy, []Proxy, error) {
	if config.SoxyURL != "" && len(config.Proxies)+len(config.ScanProxies) > 0 {
		return nil, nil, errors.New("configure either Soxy or standard proxies, not both")
	}
	proxies, err := normalizeProxies(config.Proxies)
	if err != nil {
		return nil, nil, fmt.Errorf("configure standard proxies: %w", err)
	}
	legacyScanProxies, err := normalizeProxies(config.ScanProxies)
	if err != nil {
		return nil, nil, fmt.Errorf("configure scan proxies: %w", err)
	}
	return proxies, legacyScanProxies, nil
}

func normalizeProxies(values []Proxy) ([]Proxy, error) {
	result := make([]Proxy, 0, len(values))
	for _, value := range values {
		parsed, err := ParseProxy(value.Server)
		if err != nil {
			return nil, err
		}
		if value.Username != "" {
			parsed.Username = value.Username
			parsed.Password = value.Password
		}
		result = append(result, parsed)
	}
	return result, nil
}

func configFromLookup(lookup func(string) (string, bool)) (Config, error) {
	config := Config{SessionTTL: defaultSessionTTL}
	if value, exists := lookup("CINEKO_SOXY_URL"); exists && strings.TrimSpace(value) != "" {
		config.SoxyURL = strings.TrimSpace(value)
	}
	if value, exists := lookup("CINEKO_SOXY_API_TOKEN"); exists {
		config.SoxyToken = strings.TrimSpace(value)
	}
	if value, exists := lookup("CINEKO_SOXY_SESSION_TTL"); exists && strings.TrimSpace(value) != "" {
		ttl, err := time.ParseDuration(strings.TrimSpace(value))
		if err != nil {
			return Config{}, fmt.Errorf("parse CINEKO_SOXY_SESSION_TTL: %w", err)
		}
		config.SessionTTL = ttl
	}
	if value, exists := lookup("CINEKO_SCAN_PROXIES"); exists && strings.TrimSpace(value) != "" {
		for _, rawProxy := range strings.Split(value, ",") {
			proxy, err := ParseProxy(strings.TrimSpace(rawProxy))
			if err != nil {
				return Config{}, fmt.Errorf("parse CINEKO_SCAN_PROXIES: %w", err)
			}
			config.ScanProxies = append(config.ScanProxies, proxy)
		}
	}
	return config, nil
}

func ParseProxy(rawURL string) (Proxy, error) {
	parsed, err := url.Parse(strings.TrimSpace(rawURL))
	if err != nil {
		// url.Parse errors can contain the complete input, including embedded
		// credentials. Keep validation errors safe for logs and user-visible
		// notifications.
		return Proxy{}, errors.New("proxy URL is invalid")
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" && parsed.Scheme != "socks5" {
		return Proxy{}, fmt.Errorf("proxy scheme %q is not supported", parsed.Scheme)
	}
	if parsed.Hostname() == "" || parsed.Port() == "" {
		return Proxy{}, errors.New("proxy URL must include a host and port")
	}
	port, err := strconv.Atoi(parsed.Port())
	if err != nil || port < 1 || port > 65535 {
		return Proxy{}, errors.New("proxy URL contains an invalid port")
	}
	if parsed.Path != "" && parsed.Path != "/" || parsed.RawQuery != "" || parsed.Fragment != "" {
		return Proxy{}, errors.New("proxy URL cannot contain a path, query, or fragment")
	}
	proxy := Proxy{Server: parsed.Scheme + "://" + parsed.Host}
	if parsed.User != nil {
		proxy.Username = parsed.User.Username()
		proxy.Password, _ = parsed.User.Password()
	}
	return proxy, nil
}

func (manager *Manager) Acquire(parent context.Context, purpose Purpose) (*Lease, error) {
	if parent == nil {
		return nil, errors.New("proxy lease context is required")
	}
	if purpose != PurposeSession && purpose != PurposeScan {
		return nil, fmt.Errorf("unknown proxy purpose %q", purpose)
	}
	if len(manager.proxies) > 0 {
		index, err := randomIndex(manager.random, len(manager.proxies))
		if err != nil {
			return nil, fmt.Errorf("select proxy: %w", err)
		}
		return newLease(parent, manager.proxies[index], nil, 0, 0), nil
	}
	if purpose == PurposeScan && len(manager.legacyScanProxies) > 0 {
		index, err := randomIndex(manager.random, len(manager.legacyScanProxies))
		if err != nil {
			return nil, fmt.Errorf("select scan proxy: %w", err)
		}
		return newLease(parent, manager.legacyScanProxies[index], nil, 0, 0), nil
	}
	if manager.client == nil {
		return newLease(parent, Proxy{}, nil, 0, 0), nil
	}

	var session soxySession
	var err error
	if purpose == PurposeScan {
		session, err = manager.acquireRandomSoxySession(parent)
	} else {
		session, err = manager.client.createSession(parent, manager.sessionTTL, "")
	}
	if err != nil {
		return nil, err
	}
	proxy, err := proxyFromSoxy(session.Proxy)
	if err != nil {
		cleanupContext, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		_ = manager.client.releaseSession(cleanupContext, session.ID)
		cancel()
		return nil, fmt.Errorf("invalid proxy returned by Soxy: %w", err)
	}
	release := func(ctx context.Context) error {
		return manager.client.releaseSession(ctx, session.ID)
	}
	extend := func(ctx context.Context) error {
		return manager.client.extendSession(ctx, session.ID, manager.sessionTTL/2)
	}
	return newLease(parent, proxy, release, manager.renewInterval, manager.maxRenewFailures, extend), nil
}

func (manager *Manager) acquireRandomSoxySession(ctx context.Context) (soxySession, error) {
	slots, err := manager.client.availableSlots(ctx)
	if err != nil {
		return soxySession{}, err
	}
	for len(slots) > 0 {
		index, selectErr := randomIndex(manager.random, len(slots))
		if selectErr != nil {
			return soxySession{}, fmt.Errorf("select Soxy slot: %w", selectErr)
		}
		slot := slots[index]
		slots[index] = slots[len(slots)-1]
		slots = slots[:len(slots)-1]
		session, createErr := manager.client.createSession(ctx, manager.sessionTTL, slot.ID)
		if createErr == nil {
			return session, nil
		}
		var apiErr *soxyAPIError
		if !errors.As(createErr, &apiErr) || apiErr.Status != http.StatusConflict && apiErr.Status != http.StatusNotFound {
			return soxySession{}, createErr
		}
	}
	return soxySession{}, ErrNoProxyCapacity
}

func proxyFromSoxy(proxy soxyProxy) (Proxy, error) {
	if proxy.Host == "" || proxy.Port < 1 || proxy.Port > 65535 {
		return Proxy{}, errors.New("proxy host or port is invalid")
	}
	return ParseProxy((&url.URL{
		Scheme: proxy.Scheme,
		Host:   net.JoinHostPort(proxy.Host, strconv.Itoa(proxy.Port)),
		User:   proxyUser(proxy.Username, proxy.Password),
	}).String())
}

func proxyUser(username, password string) *url.Userinfo {
	if username == "" {
		return nil
	}
	if password == "" {
		return url.User(username)
	}
	return url.UserPassword(username, password)
}

func randomIndex(random io.Reader, size int) (int, error) {
	if size < 1 {
		return 0, errors.New("cannot choose from an empty proxy set")
	}
	value, err := cryptorand.Int(random, big.NewInt(int64(size)))
	if err != nil {
		return 0, err
	}
	return int(value.Int64()), nil
}

type releaseFunc func(context.Context) error
type extendFunc func(context.Context) error

type Lease struct {
	proxy     Proxy
	ctx       context.Context
	cancel    context.CancelCauseFunc
	release   releaseFunc
	extend    extendFunc
	interval  time.Duration
	maxErrors int
	done      chan struct{}
	closeOnce sync.Once
	closeErr  error
}

func newLease(
	parent context.Context,
	proxy Proxy,
	release releaseFunc,
	interval time.Duration,
	maxErrors int,
	extenders ...extendFunc,
) *Lease {
	ctx, cancel := context.WithCancelCause(parent)
	lease := &Lease{proxy: proxy, ctx: ctx, cancel: cancel, release: release, interval: interval, maxErrors: maxErrors}
	if len(extenders) > 0 && extenders[0] != nil && interval > 0 {
		lease.extend = extenders[0]
		lease.done = make(chan struct{})
		go lease.keepAlive()
	}
	return lease
}

func (lease *Lease) Context() context.Context { return lease.ctx }

func (lease *Lease) Proxy() *Proxy {
	if lease.proxy.Server == "" {
		return nil
	}
	proxy := lease.proxy
	return &proxy
}

func (lease *Lease) Close() error {
	lease.closeOnce.Do(func() {
		lease.cancel(nil)
		if lease.done != nil {
			<-lease.done
		}
		if lease.release != nil {
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			lease.closeErr = lease.release(ctx)
			cancel()
		}
	})
	return lease.closeErr
}

func (lease *Lease) keepAlive() {
	defer close(lease.done)
	timer := time.NewTimer(lease.interval)
	defer timer.Stop()
	consecutiveFailures := 0
	for {
		select {
		case <-lease.ctx.Done():
			return
		case <-timer.C:
			if err := lease.extend(lease.ctx); err != nil {
				consecutiveFailures++
				if consecutiveFailures >= lease.maxErrors {
					lease.cancel(fmt.Errorf("%w: %w", ErrLeaseRenewal, err))
					return
				}
				timer.Reset(renewalRetryDelay(lease.interval))
			} else {
				consecutiveFailures = 0
				timer.Reset(lease.interval)
			}
		}
	}
}

func renewalRetryDelay(interval time.Duration) time.Duration {
	delay := interval / 10
	if delay < time.Millisecond {
		return time.Millisecond
	}
	if delay > 5*time.Second {
		return 5 * time.Second
	}
	return delay
}
