package egress

import (
	"context"
	cryptorand "crypto/rand"
	"errors"
	"fmt"
	"io"
	"math/big"
	"net/url"
	"os"
	"strconv"
	"strings"
	"sync"
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
	Proxies []Proxy
	// ScanProxies is retained for environment compatibility. New callers use
	// Proxies, which applies one stable selection to either logical purpose.
	ScanProxies []Proxy
	Random      io.Reader
	Probe       func(context.Context, Proxy) error
}

type Manager struct {
	proxies           []Proxy
	legacyScanProxies []Proxy
	random            io.Reader
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
	proxies, legacyScanProxies, err := configuredProxyPools(config)
	if err != nil {
		return nil, err
	}
	if config.Random == nil {
		config.Random = cryptorand.Reader
	}
	return &Manager{
		proxies:           proxies,
		legacyScanProxies: legacyScanProxies,
		random:            config.Random,
	}, nil
}

func configuredProxyPools(config Config) ([]Proxy, []Proxy, error) {
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
	config := Config{}
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
		return newLease(parent, manager.proxies[index]), nil
	}
	if purpose == PurposeScan && len(manager.legacyScanProxies) > 0 {
		index, err := randomIndex(manager.random, len(manager.legacyScanProxies))
		if err != nil {
			return nil, fmt.Errorf("select scan proxy: %w", err)
		}
		return newLease(parent, manager.legacyScanProxies[index]), nil
	}
	return newLease(parent, Proxy{}), nil
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

type Lease struct {
	proxy     Proxy
	ctx       context.Context
	cancel    context.CancelCauseFunc
	closeOnce sync.Once
}

func newLease(parent context.Context, proxy Proxy) *Lease {
	ctx, cancel := context.WithCancelCause(parent)
	return &Lease{proxy: proxy, ctx: ctx, cancel: cancel}
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
	})
	return nil
}
