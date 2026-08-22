package eventhook

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	clientpb "github.com/cineko-org/contracts/gen/go/cineko/client"
)

type fixedResolver struct {
	addresses []net.IPAddr
	err       error
}

func (resolver fixedResolver) LookupIPAddr(context.Context, string) ([]net.IPAddr, error) {
	return resolver.addresses, resolver.err
}

func TestConfigureValidatesTargets(t *testing.T) {
	dispatcher := New(nil)
	defer dispatcher.Close()
	valid := targetForTest("one", "Custom", "http://127.0.0.1:1234", "", nil)
	if err := dispatcher.Configure([]*clientpb.WebhookTarget{valid}); err != nil {
		t.Fatalf("Configure() error = %v", err)
	}
	for _, target := range []*clientpb.WebhookTarget{
		targetForTest("", "missing ID", valid.GetUrl(), "", nil),
		targetForTest("one", "", valid.GetUrl(), "", nil),
		targetForTest("one", "bad URL", "://bad", "", nil),
		targetForTest("one", "credentials", "https://user:secret@example.com/hook", "", nil), // #nosec G101 -- validation fixture.
		targetForTest("one", "insecure", "http://example.com/hook", "", nil),
		targetForTest("one", "discord incomplete", "https://discord.com/api/webhooks/1", "", nil),
		targetForTest("one", "slack incomplete", "https://hooks.slack.com/services/1/2", "", nil),
	} {
		if err := dispatcher.Configure([]*clientpb.WebhookTarget{target}); err == nil {
			t.Errorf("Configure(%+v) error = nil", target)
		}
	}
	if err := dispatcher.Configure([]*clientpb.WebhookTarget{valid, valid}); err == nil {
		t.Fatal("Configure(duplicate) error = nil")
	}
}

func TestDispatcherDeliversSignedFilteredWebhook(t *testing.T) {
	received := make(chan *http.Request, 1)
	payloads := make(chan []byte, 1)
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		encoded, _ := io.ReadAll(request.Body)
		received <- request.Clone(context.Background())
		payloads <- encoded
		writer.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()
	dispatcher := New(&http.Client{Timeout: time.Second})
	defer dispatcher.Close()
	if err := dispatcher.Configure([]*clientpb.WebhookTarget{
		targetForTest("custom", "Custom", server.URL, "secret", []string{"monitor.completed", "monitor.completed", ""}),
	}); err != nil {
		t.Fatal(err)
	}
	if err := dispatcher.Publish(context.Background(), eventForTest("ignored", "", "monitor.started", "", false)); err != nil {
		t.Fatal(err)
	}
	event := eventForTest("delivery", "user", "monitor.completed", "done", true)
	if err := dispatcher.Publish(context.Background(), event); err != nil {
		t.Fatal(err)
	}
	select {
	case request := <-received:
		payload := <-payloads
		mac := hmac.New(sha256.New, []byte("secret"))
		_, _ = mac.Write(payload)
		want := "sha256=" + hex.EncodeToString(mac.Sum(nil))
		if request.Header.Get("X-Cineko-Event") != event.GetKind() || request.Header.Get("X-Cineko-Delivery") != event.GetId() || request.Header.Get("X-Cineko-Signature-256") != want {
			t.Fatalf("headers = %+v", request.Header)
		}
	case <-time.After(time.Second):
		t.Fatal("webhook was not delivered")
	}
}

func TestDispatcherReportsFailuresAndAdapters(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		http.Error(writer, "down", http.StatusBadGateway)
	}))
	defer server.Close()
	dispatcher := New(&http.Client{})
	failures := make(chan Failure, 1)
	dispatcher.SetFailureHandler(func(failure Failure) { failures <- failure })
	if err := dispatcher.Configure([]*clientpb.WebhookTarget{targetForTest("bad", "Bad", server.URL, "", nil)}); err != nil {
		t.Fatal(err)
	}
	if err := dispatcher.Publish(context.Background(), eventForTest("event", "", "failed", "", false)); err != nil {
		t.Fatal(err)
	}
	select {
	case failure := <-failures:
		if !strings.Contains(failure.Err.Error(), "502") {
			t.Fatalf("failure = %v", failure.Err)
		}
	case <-time.After(time.Second):
		t.Fatal("failure was not reported")
	}
	dispatcher.Close()
	if err := dispatcher.Publish(context.Background(), &clientpb.AppEvent{}); err == nil {
		t.Fatal("Publish(closed) error = nil")
	}
	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	if err := dispatcher.Publish(cancelled, &clientpb.AppEvent{}); !errors.Is(err, context.Canceled) {
		t.Fatalf("Publish(cancelled) error = %v", err)
	}

	requestServer := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		var payload struct {
			Content string `json:"content"`
			Text    string `json:"text"`
		}
		_ = json.NewDecoder(request.Body).Decode(&payload)
		if payload.Content != "[success] done" && payload.Text != "[success] done" {
			t.Errorf("payload = %+v", payload)
		}
		writer.WriteHeader(http.StatusNoContent)
	}))
	defer requestServer.Close()
	if err := (discordAdapter{}).Deliver(context.Background(), http.DefaultClient,
		targetForTest("", "", requestServer.URL, "", nil), eventForTest("", "", "", "done", true)); err != nil {
		t.Fatalf("Discord Deliver() error = %v", err)
	}
	if err := (slackAdapter{}).Deliver(context.Background(), http.DefaultClient,
		targetForTest("", "", requestServer.URL, "", nil), eventForTest("", "", "", "done", true)); err != nil {
		t.Fatalf("Slack Deliver() error = %v", err)
	}
}

func eventForTest(id, userID, kind, message string, success bool) *clientpb.AppEvent {
	value := clientpb.AppEvent_builder{Id: &id, UserId: &userID, Kind: &kind, Message: &message}
	if success {
		value.Success = clientpb.EventSuccess_builder{}.Build()
	} else {
		value.Info = clientpb.EventInfo_builder{}.Build()
	}
	return value.Build()
}

func targetForTest(id, name, rawURL, secret string, eventKinds []string) *clientpb.WebhookTarget {
	enabled := true
	return clientpb.WebhookTarget_builder{
		Id: &id, Name: &name, Url: &rawURL, Secret: &secret, EventKinds: eventKinds, Enabled: &enabled,
	}.Build()
}

func TestSlackWebhookURLValidation(t *testing.T) {
	dispatcher := New(nil)
	defer dispatcher.Close()
	for _, rawURL := range []string{
		"https://hooks.slack.com/services/T000/B000/token",
		"https://hooks.slack-gov.com/services/T000/B000/token",
	} {
		if err := dispatcher.Configure([]*clientpb.WebhookTarget{
			targetForTest(rawURL, "Slack", rawURL, "", nil),
		}); err != nil {
			t.Errorf("Configure(%q) error = %v", rawURL, err)
		}
	}
}

func TestWebhookClientRejectsEveryPrivateOrReservedDNSAnswer(t *testing.T) {
	t.Parallel()
	dispatcher := New(nil)
	defer dispatcher.Close()
	for _, value := range []string{
		"10.0.0.1", "169.254.169.254", "192.168.1.1", "198.51.100.1", "::1", "fc00::1",
		"64:ff9b::a00:1", "2002:0a00:0001::", "::ffff:10.0.0.1",
	} {
		dispatcher.resolver = fixedResolver{addresses: []net.IPAddr{{IP: net.ParseIP(value)}}}
		if _, err := dispatcher.clientForTarget(t.Context(), "https://webhook.example.test/path"); err == nil {
			t.Errorf("private or reserved address %s accepted", value)
		}
	}
	dispatcher.resolver = fixedResolver{addresses: []net.IPAddr{
		{IP: net.ParseIP("8.8.8.8")}, {IP: net.ParseIP("10.0.0.1")},
	}}
	if _, err := dispatcher.clientForTarget(t.Context(), "https://webhook.example.test/path"); err == nil {
		t.Fatal("mixed public and private DNS answers accepted")
	}
}

func TestWebhookClientPinsValidatedAddressAndDisablesRedirects(t *testing.T) {
	t.Parallel()
	dispatcher := New(nil)
	defer dispatcher.Close()
	dispatcher.resolver = fixedResolver{addresses: []net.IPAddr{{IP: net.ParseIP("8.8.8.8")}}}
	var dialed string
	dialErr := errors.New("stop after pin assertion")
	dispatcher.dial = func(_ context.Context, _, address string) (net.Conn, error) {
		dialed = address
		return nil, dialErr
	}
	client, err := dispatcher.clientForTarget(t.Context(), "https://webhook.example.test/path")
	if err != nil {
		t.Fatal(err)
	}
	request, err := http.NewRequestWithContext(t.Context(), http.MethodPost, "https://webhook.example.test/path", nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := send(client, request); err == nil || dialed != "8.8.8.8:443" {
		t.Fatalf("pinned request dial = %q, error = %v", dialed, err)
	}

	var redirected bool
	destination := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { redirected = true }))
	defer destination.Close()
	redirector := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		http.Redirect(writer, request, destination.URL, http.StatusFound)
	}))
	defer redirector.Close()
	loopbackClient, err := dispatcher.clientForTarget(t.Context(), redirector.URL)
	if err != nil {
		t.Fatal(err)
	}
	request, _ = http.NewRequestWithContext(t.Context(), http.MethodPost, redirector.URL, nil)
	if err := send(loopbackClient, request); err == nil || redirected {
		t.Fatalf("redirect send error = %v, redirected = %t", err, redirected)
	}
}

func TestWebhookValidationRejectsLiteralPrivateHTTPSDestination(t *testing.T) {
	t.Parallel()
	dispatcher := New(nil)
	defer dispatcher.Close()
	if err := dispatcher.Configure([]*clientpb.WebhookTarget{
		targetForTest("metadata", "Metadata", "https://169.254.169.254/latest", "", nil),
	}); err == nil {
		t.Fatal("literal link-local webhook destination accepted")
	}
}
