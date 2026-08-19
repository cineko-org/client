package eventhook

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"crypto/tls"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/cineko-org/client/internal/domain"
)

type Kind string

const (
	KindDiscord Kind = "discord"
	KindSlack   Kind = "slack"
	KindWebhook Kind = "webhook"

	defaultQueueSize = 128
)

type Target struct {
	ID         string   `json:"id"`
	Name       string   `json:"name"`
	Kind       Kind     `json:"kind"`
	URL        string   `json:"url"`
	Secret     string   `json:"secret,omitempty"`
	EventKinds []string `json:"eventKinds,omitempty"`
	Enabled    bool     `json:"enabled"`
}

type Adapter interface {
	Deliver(context.Context, *http.Client, Target, domain.AppEvent) error
}

type Failure struct {
	Target Target
	Event  domain.AppEvent
	Err    error
}

type Dispatcher struct {
	client   *http.Client
	timeout  time.Duration
	resolver ipResolver
	dial     func(context.Context, string, string) (net.Conn, error)
	adapters map[Kind]Adapter
	queue    chan domain.AppEvent
	done     chan struct{}

	mu        sync.RWMutex
	targets   []Target
	onFailure func(Failure)
	closeOnce sync.Once
}

func New(client *http.Client) *Dispatcher {
	if client == nil {
		client = &http.Client{
			Timeout: 10 * time.Second,
			CheckRedirect: func(*http.Request, []*http.Request) error {
				return http.ErrUseLastResponse
			},
		}
	}
	timeout := client.Timeout
	if timeout <= 0 {
		timeout = 10 * time.Second
	}
	dispatcher := &Dispatcher{
		client: client, timeout: timeout, resolver: net.DefaultResolver,
		dial: (&net.Dialer{Timeout: 10 * time.Second}).DialContext,
		adapters: map[Kind]Adapter{
			KindDiscord: discordAdapter{},
			KindSlack:   slackAdapter{},
			KindWebhook: webhookAdapter{},
		},
		queue: make(chan domain.AppEvent, defaultQueueSize),
		done:  make(chan struct{}),
	}
	go dispatcher.run()
	return dispatcher
}

func (dispatcher *Dispatcher) Configure(targets []Target) error {
	normalized, err := normalizeTargets(targets, dispatcher.adapters)
	if err != nil {
		return err
	}
	dispatcher.mu.Lock()
	dispatcher.targets = normalized
	dispatcher.mu.Unlock()
	return nil
}

func (dispatcher *Dispatcher) SetFailureHandler(handler func(Failure)) {
	dispatcher.mu.Lock()
	dispatcher.onFailure = handler
	dispatcher.mu.Unlock()
}

func (dispatcher *Dispatcher) Publish(ctx context.Context, event domain.AppEvent) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	select {
	case <-dispatcher.done:
		return errors.New("event hook dispatcher is closed")
	default:
	}
	select {
	case dispatcher.queue <- event:
		return nil
	default:
		return errors.New("event hook queue is full")
	}
}

func (dispatcher *Dispatcher) Close() {
	dispatcher.closeOnce.Do(func() { close(dispatcher.done) })
}

func (dispatcher *Dispatcher) run() {
	for {
		select {
		case event := <-dispatcher.queue:
			dispatcher.deliver(event)
		case <-dispatcher.done:
			return
		}
	}
}

func (dispatcher *Dispatcher) deliver(event domain.AppEvent) {
	dispatcher.mu.RLock()
	targets := append([]Target(nil), dispatcher.targets...)
	handler := dispatcher.onFailure
	dispatcher.mu.RUnlock()
	for _, target := range targets {
		if !target.Enabled || !matches(target.EventKinds, event.Kind) {
			continue
		}
		adapter := dispatcher.adapters[target.Kind]
		ctx, cancel := context.WithTimeout(context.Background(), dispatcher.timeout)
		client, clientErr := dispatcher.clientForTarget(ctx, target.URL)
		var err error
		if clientErr != nil {
			err = clientErr
		} else {
			err = adapter.Deliver(ctx, client, target, event)
			client.CloseIdleConnections()
		}
		cancel()
		if err != nil && handler != nil {
			handler(Failure{Target: target, Event: event, Err: err})
		}
	}
}

func normalizeTargets(targets []Target, adapters map[Kind]Adapter) ([]Target, error) {
	result := make([]Target, 0, len(targets))
	ids := make(map[string]struct{}, len(targets))
	for index, target := range targets {
		var err error
		target, err = normalizeTarget(index, target, adapters)
		if err != nil {
			return nil, err
		}
		if _, exists := ids[target.ID]; exists {
			return nil, fmt.Errorf("hook ID %q is duplicated", target.ID)
		}
		ids[target.ID] = struct{}{}
		result = append(result, target)
	}
	return result, nil
}

func normalizeTarget(index int, target Target, adapters map[Kind]Adapter) (Target, error) {
	target.ID = strings.TrimSpace(target.ID)
	target.Name = strings.TrimSpace(target.Name)
	target.URL = strings.TrimSpace(target.URL)
	if target.ID == "" {
		return Target{}, fmt.Errorf("hook %d: ID is required", index+1)
	}
	if target.Name == "" {
		return Target{}, fmt.Errorf("hook %q: name is required", target.ID)
	}
	if _, exists := adapters[target.Kind]; !exists {
		return Target{}, fmt.Errorf("hook %q: unsupported adapter %q", target.ID, target.Kind)
	}
	parsed, err := url.Parse(target.URL)
	if err != nil || parsed.Hostname() == "" {
		return Target{}, fmt.Errorf("hook %q: valid URL is required", target.ID)
	}
	if err := validateTargetURL(target, parsed); err != nil {
		return Target{}, err
	}
	target.EventKinds = uniqueStrings(target.EventKinds)
	return target, nil
}

func validateTargetURL(target Target, parsed *url.URL) error {
	if parsed.User != nil {
		return fmt.Errorf("hook %q: URL credentials are not allowed", target.ID)
	}
	if parsed.Scheme != "https" && (parsed.Scheme != "http" || !isLoopback(parsed.Hostname())) {
		return fmt.Errorf("hook %q: HTTPS is required outside loopback", target.ID)
	}
	if ip := net.ParseIP(parsed.Hostname()); ip != nil && !isLoopback(parsed.Hostname()) && !isPublicWebhookIP(ip) {
		return fmt.Errorf("hook %q: private or reserved destinations are not allowed", target.ID)
	}
	switch {
	case target.Kind == KindDiscord && !isDiscordWebhook(parsed):
		return fmt.Errorf("hook %q: invalid Discord webhook URL", target.ID)
	case target.Kind == KindSlack && !isSlackWebhook(parsed):
		return fmt.Errorf("hook %q: invalid Slack webhook URL", target.ID)
	default:
		return nil
	}
}

func isLoopback(host string) bool {
	return strings.EqualFold(host, "localhost") || net.ParseIP(host) != nil && net.ParseIP(host).IsLoopback()
}

type ipResolver interface {
	LookupIPAddr(context.Context, string) ([]net.IPAddr, error)
}

var blockedWebhookPrefixes = []netip.Prefix{
	netip.MustParsePrefix("0.0.0.0/8"),
	netip.MustParsePrefix("10.0.0.0/8"),
	netip.MustParsePrefix("100.64.0.0/10"),
	netip.MustParsePrefix("127.0.0.0/8"),
	netip.MustParsePrefix("169.254.0.0/16"),
	netip.MustParsePrefix("172.16.0.0/12"),
	netip.MustParsePrefix("192.0.0.0/24"),
	netip.MustParsePrefix("192.0.2.0/24"),
	netip.MustParsePrefix("192.31.196.0/24"),
	netip.MustParsePrefix("192.52.193.0/24"),
	netip.MustParsePrefix("192.88.99.0/24"),
	netip.MustParsePrefix("192.168.0.0/16"),
	netip.MustParsePrefix("192.175.48.0/24"),
	netip.MustParsePrefix("198.18.0.0/15"),
	netip.MustParsePrefix("198.51.100.0/24"),
	netip.MustParsePrefix("203.0.113.0/24"),
	netip.MustParsePrefix("224.0.0.0/4"),
	netip.MustParsePrefix("240.0.0.0/4"),
	netip.MustParsePrefix("::/128"),
	netip.MustParsePrefix("::1/128"),
	netip.MustParsePrefix("64:ff9b::/96"),
	netip.MustParsePrefix("64:ff9b:1::/48"),
	netip.MustParsePrefix("100::/64"),
	netip.MustParsePrefix("2001::/23"),
	netip.MustParsePrefix("2001:db8::/32"),
	netip.MustParsePrefix("2002::/16"),
	netip.MustParsePrefix("3fff::/20"),
	netip.MustParsePrefix("5f00::/16"),
	netip.MustParsePrefix("fc00::/7"),
	netip.MustParsePrefix("fe80::/10"),
	netip.MustParsePrefix("ff00::/8"),
}

func isPublicWebhookIP(ip net.IP) bool {
	address, ok := netip.AddrFromSlice(ip)
	if !ok {
		return false
	}
	address = address.Unmap()
	if !address.IsGlobalUnicast() {
		return false
	}
	for _, prefix := range blockedWebhookPrefixes {
		if prefix.Contains(address) {
			return false
		}
	}
	return true
}

func (dispatcher *Dispatcher) clientForTarget(ctx context.Context, rawURL string) (*http.Client, error) {
	parsed, err := url.Parse(rawURL)
	if err != nil || parsed.Hostname() == "" {
		return nil, errors.New("webhook destination is invalid")
	}
	if isLoopback(parsed.Hostname()) {
		client := redirectSafeClient(dispatcher.client)
		client.Jar = nil
		client.Transport = directTransport(client.Transport)
		return client, nil
	}
	addresses, err := dispatcher.resolver.LookupIPAddr(ctx, parsed.Hostname())
	if err != nil || len(addresses) == 0 {
		return nil, errors.New("webhook destination could not be resolved")
	}
	for _, address := range addresses {
		if !isPublicWebhookIP(address.IP) {
			return nil, errors.New("webhook destination resolved to a private or reserved address")
		}
	}
	pinnedIP := addresses[0].IP.String()
	client := redirectSafeClient(dispatcher.client)
	transport := directTransport(client.Transport)
	transport.DialTLSContext = nil
	transport.DialContext = func(ctx context.Context, network, address string) (net.Conn, error) {
		_, port, splitErr := net.SplitHostPort(address)
		if splitErr != nil {
			return nil, errors.New("webhook destination port is invalid")
		}
		return dispatcher.dial(ctx, network, net.JoinHostPort(pinnedIP, port))
	}
	if transport.TLSClientConfig == nil {
		transport.TLSClientConfig = &tls.Config{}
	} else {
		transport.TLSClientConfig = transport.TLSClientConfig.Clone()
	}
	transport.TLSClientConfig.ServerName = parsed.Hostname()
	client.Transport = transport
	client.Jar = nil
	return client, nil
}

func directTransport(roundTripper http.RoundTripper) *http.Transport {
	transport := cloneTransport(roundTripper)
	transport.Proxy = nil
	return transport
}

func redirectSafeClient(base *http.Client) *http.Client {
	client := *base
	client.CheckRedirect = func(*http.Request, []*http.Request) error {
		return http.ErrUseLastResponse
	}
	return &client
}

func cloneTransport(roundTripper http.RoundTripper) *http.Transport {
	if transport, ok := roundTripper.(*http.Transport); ok {
		return transport.Clone()
	}
	return &http.Transport{
		ForceAttemptHTTP2:     true,
		TLSHandshakeTimeout:   10 * time.Second,
		ResponseHeaderTimeout: 10 * time.Second,
	}
}

func isDiscordWebhook(parsed *url.URL) bool {
	host := strings.ToLower(parsed.Hostname())
	return (host == "discord.com" || host == "discordapp.com") &&
		hasWebhookPath(parsed.Path, []string{"api", "webhooks"}, 4)
}

func isSlackWebhook(parsed *url.URL) bool {
	host := strings.ToLower(parsed.Hostname())
	return (host == "hooks.slack.com" || host == "hooks.slack-gov.com") &&
		hasWebhookPath(parsed.Path, []string{"services"}, 4)
}

func hasWebhookPath(path string, prefix []string, minimumParts int) bool {
	parts := strings.Split(strings.Trim(path, "/"), "/")
	return len(parts) >= minimumParts && slices.Equal(parts[:len(prefix)], prefix)
}

func uniqueStrings(values []string) []string {
	result := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value != "" && !slices.Contains(result, value) {
			result = append(result, value)
		}
	}
	return result
}

func matches(filters []string, kind string) bool {
	return len(filters) == 0 || slices.Contains(filters, kind)
}

type webhookAdapter struct{}

func (webhookAdapter) Deliver(ctx context.Context, client *http.Client, target Target, event domain.AppEvent) error {
	payload, err := json.Marshal(event)
	if err != nil {
		return err
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, target.URL, bytes.NewReader(payload))
	if err != nil {
		return err
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("User-Agent", "Cineko-Hook/1")
	request.Header.Set("X-Cineko-Event", event.Kind)
	request.Header.Set("X-Cineko-Delivery", event.ID)
	if target.Secret != "" {
		mac := hmac.New(sha256.New, []byte(target.Secret))
		_, _ = mac.Write(payload)
		request.Header.Set("X-Cineko-Signature-256", "sha256="+hex.EncodeToString(mac.Sum(nil)))
	}
	return send(client, request)
}

type discordAdapter struct{}

func (discordAdapter) Deliver(ctx context.Context, client *http.Client, target Target, event domain.AppEvent) error {
	payload, err := json.Marshal(map[string]any{
		"content":          fmt.Sprintf("[%s] %s", event.Tone, event.Message),
		"allowed_mentions": map[string]any{"parse": []string{}},
	})
	if err != nil {
		return err
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, target.URL, bytes.NewReader(payload))
	if err != nil {
		return err
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("User-Agent", "Cineko-Hook/1")
	return send(client, request)
}

type slackAdapter struct{}

func (slackAdapter) Deliver(ctx context.Context, client *http.Client, target Target, event domain.AppEvent) error {
	payload, err := json.Marshal(map[string]string{
		"text": fmt.Sprintf("[%s] %s", event.Tone, event.Message),
	})
	if err != nil {
		return err
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, target.URL, bytes.NewReader(payload))
	if err != nil {
		return err
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("User-Agent", "Cineko-Hook/1")
	return send(client, request)
}

func send(client *http.Client, request *http.Request) error {
	response, err := client.Do(request)
	if err != nil {
		// url.Error includes the complete webhook URL. Discord, Slack, and
		// custom webhook paths frequently contain credentials.
		return errors.New("webhook request failed")
	}
	defer func() { _ = response.Body.Close() }()
	_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, 4096))
	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		return fmt.Errorf("webhook returned HTTP %d", response.StatusCode)
	}
	return nil
}
