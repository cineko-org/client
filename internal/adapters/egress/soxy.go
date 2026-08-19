package egress

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"
)

type soxyClient struct {
	baseURL    string
	token      string
	httpClient *http.Client
}

type soxySlot struct {
	ID        string `json:"id"`
	Status    string `json:"status"`
	CurrentIP string `json:"current_ip"`
}

type soxyProxy struct {
	Scheme   string `json:"scheme"`
	Host     string `json:"host"`
	Port     int    `json:"port"`
	Username string `json:"username"`
	Password string `json:"password"`
}

type soxySession struct {
	ID     string    `json:"id"`
	Status string    `json:"status"`
	Ready  bool      `json:"ready"`
	Proxy  soxyProxy `json:"proxy"`
}

type soxyAPIError struct {
	Status int
	Code   string
}

func (err *soxyAPIError) Error() string {
	if err.Code != "" {
		return fmt.Sprintf("Soxy returned HTTP %d (%s)", err.Status, err.Code)
	}
	return fmt.Sprintf("Soxy returned HTTP %d", err.Status)
}

func newSoxyClient(rawURL, token string, httpClient *http.Client) (*soxyClient, error) {
	parsed, err := url.Parse(strings.TrimSpace(rawURL))
	if err != nil {
		// Do not return url.Parse's error: it can echo credentials supplied in
		// the raw URL into logs and durable application events.
		return nil, errors.New("soxy URL is invalid")
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" || parsed.Host == "" {
		return nil, errors.New("soxy URL must be an HTTP or HTTPS origin")
	}
	if parsed.Scheme != "https" && !isLoopbackOrigin(parsed.Hostname()) {
		return nil, errors.New("soxy URL must use HTTPS outside loopback development")
	}
	if parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" || parsed.Path != "" && parsed.Path != "/" {
		return nil, errors.New("soxy URL must not contain credentials, a path, query, or fragment")
	}
	if httpClient == nil {
		httpClient = &http.Client{Timeout: 15 * time.Second}
	}
	return &soxyClient{
		baseURL: strings.TrimRight(parsed.String(), "/"), token: strings.TrimSpace(token), httpClient: httpClient,
	}, nil
}

func isLoopbackOrigin(host string) bool {
	if strings.EqualFold(host, "localhost") {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

func (client *soxyClient) availableSlots(ctx context.Context) ([]soxySlot, error) {
	var response struct {
		Slots []soxySlot `json:"slots"`
	}
	if err := client.request(ctx, http.MethodGet, "/v1/slots", nil, http.StatusOK, &response); err != nil {
		return nil, err
	}
	available := make([]soxySlot, 0, len(response.Slots))
	for _, slot := range response.Slots {
		if slot.ID != "" && slot.Status == "available" && slot.CurrentIP != "" {
			available = append(available, slot)
		}
	}
	if len(available) == 0 {
		return nil, ErrNoProxyCapacity
	}
	return available, nil
}

func (client *soxyClient) createSession(ctx context.Context, ttl time.Duration, slotID string) (soxySession, error) {
	payload := struct {
		TTLSeconds int64  `json:"ttl_seconds"`
		SlotID     string `json:"slot_id,omitempty"`
	}{TTLSeconds: int64(ttl / time.Second), SlotID: slotID}
	var session soxySession
	if err := client.request(ctx, http.MethodPost, "/v1/sessions", payload, http.StatusCreated, &session); err != nil {
		return soxySession{}, err
	}
	if session.ID == "" || session.Status != "active" || !session.Ready {
		if session.ID != "" {
			cleanupContext, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			_ = client.releaseSession(cleanupContext, session.ID)
			cancel()
		}
		return soxySession{}, errors.New("soxy returned a session that is not ready")
	}
	return session, nil
}

func (client *soxyClient) extendSession(ctx context.Context, sessionID string, extension time.Duration) error {
	payload := struct {
		ExtendBySeconds int64 `json:"extend_by_seconds"`
	}{ExtendBySeconds: int64(extension / time.Second)}
	return client.request(
		ctx, http.MethodPost, "/v1/sessions/"+url.PathEscape(sessionID)+"/extend", payload, http.StatusOK, nil,
	)
}

func (client *soxyClient) releaseSession(ctx context.Context, sessionID string) error {
	err := client.request(ctx, http.MethodDelete, "/v1/sessions/"+url.PathEscape(sessionID), nil, http.StatusNoContent, nil)
	var apiErr *soxyAPIError
	if errors.As(err, &apiErr) && apiErr.Status == http.StatusNotFound {
		return nil
	}
	return err
}

func (client *soxyClient) request(
	ctx context.Context,
	method, path string,
	payload any,
	wantStatus int,
	destination any,
) error {
	var body io.Reader
	if payload != nil {
		encoded, err := json.Marshal(payload)
		if err != nil {
			return fmt.Errorf("encode Soxy request: %w", err)
		}
		body = bytes.NewReader(encoded)
	}
	request, err := http.NewRequestWithContext(ctx, method, client.baseURL+path, body)
	if err != nil {
		return fmt.Errorf("create Soxy request: %w", err)
	}
	request.Header.Set("Authorization", "Bearer "+client.token)
	request.Header.Set("Accept", "application/json")
	if payload != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	response, err := client.httpClient.Do(request)
	if err != nil {
		return fmt.Errorf("call Soxy: %w", err)
	}
	defer func() { _ = response.Body.Close() }()
	if response.StatusCode != wantStatus {
		limited, _ := io.ReadAll(io.LimitReader(response.Body, 4096))
		var envelope struct {
			Error struct {
				Code string `json:"code"`
			} `json:"error"`
		}
		_ = json.Unmarshal(limited, &envelope)
		return &soxyAPIError{Status: response.StatusCode, Code: envelope.Error.Code}
	}
	if destination == nil || response.StatusCode == http.StatusNoContent {
		_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, 4096))
		return nil
	}
	decoder := json.NewDecoder(io.LimitReader(response.Body, 1<<20))
	if err := decoder.Decode(destination); err != nil {
		return fmt.Errorf("decode Soxy response: %w", err)
	}
	return nil
}
