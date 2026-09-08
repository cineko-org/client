package cgv

import (
	"context"
	"encoding/json"
	"errors"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/cineko-org/client/internal/logging"
	"github.com/mxschmitt/playwright-go"
)

var ErrAuthenticationUnverified = errors.New("CGV authentication could not be verified from browser responses")

// Only observe responses that CGV's own page requested. Never issue a separate
// fetch, replay a token, or infer server authentication from a logout button.
type authenticationEvidence struct {
	mu     sync.Mutex
	since  time.Time
	latest time.Time
	known  bool
	valid  bool
}

func (e *authenticationEvidence) reset() {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.since, e.latest, e.known, e.valid = time.Now(), time.Time{}, false, false
}

func (e *authenticationEvidence) observe(started time.Time, valid bool) bool {
	e.mu.Lock()
	defer e.mu.Unlock()
	if started.Before(e.since) || started.Before(e.latest) {
		return false
	}
	changed := !e.known || e.valid != valid
	e.latest, e.known, e.valid = started, true, valid
	return changed
}

func (e *authenticationEvidence) snapshot() (bool, bool) {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.known, e.valid
}

func (e *authenticationEvidence) verified() bool {
	known, valid := e.snapshot()
	return known && valid
}

func authenticationEndpoint(raw string) (string, bool) {
	u, err := url.Parse(raw)
	if err != nil || (u.Hostname() != "cgv.co.kr" && u.Hostname() != "www.cgv.co.kr") {
		return "", false
	}
	switch u.Path {
	case "/api/v1/mypage/tkt/mblTkt/searchMblTktTabPrdtypList",
		"/api/v1/content/festival/bznsCom/mobFdUser/searchLoginFdUserProflDtl":
		return u.Path, true
	default:
		return u.Path, false
	}
}

func authenticationResponse(status int, body []byte) (known, valid bool) {
	if status == 401 {
		return true, false
	}
	if status < 200 || status >= 300 {
		return false, false
	}
	var payload struct {
		StatusCode json.RawMessage `json:"statusCode"`
	}
	if json.Unmarshal(body, &payload) != nil {
		return false, false
	}
	code, err := requiredInteger(payload.StatusCode, "statusCode")
	if err != nil {
		return false, false
	}
	switch code {
	case -1001:
		return true, false
	case 0:
		return true, true
	default:
		return false, false
	}
}

func (adapter *Adapter) observeAuthenticationResponse(response playwright.Response) {
	if response == nil {
		return
	}
	path, trusted := authenticationEndpoint(response.URL())
	if !trusted && (response.Status() != 401 || !strings.HasPrefix(path, "/api/v1/")) {
		return
	}
	started := time.Now()
	if request := response.Request(); request != nil {
		if timing := request.Timing(); timing != nil && timing.StartTime > 0 {
			started = time.UnixMilli(int64(timing.StartTime))
		}
	}
	var body []byte
	if response.Status() >= 200 && response.Status() < 300 {
		var err error
		body, err = response.Body()
		if err != nil {
			return
		}
	}
	known, valid := authenticationResponse(response.Status(), body)
	if known && adapter.authEvidence.observe(started, valid) && !valid {
		logging.WarnUnexpected(adapter.ctx, "account.authentication.rejected", "authentication", "observe_account_response",
			"a valid authenticated CGV session", "CGV rejected the session; login is required",
			"request_path", path, "status", response.Status())
	}
}

func (adapter *Adapter) waitForAuthentication(ctx context.Context) (bool, error) {
	timer := time.NewTimer(8 * time.Second)
	defer timer.Stop()
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	for {
		if known, valid := adapter.authEvidence.snapshot(); known {
			if valid {
				if authenticated, err := adapter.authenticatedState(); err != nil || authenticated {
					return authenticated, err
				}
			}
		}
		select {
		case <-ctx.Done():
			return false, ctx.Err()
		case <-adapter.ctx.Done():
			return false, adapter.ctx.Err()
		case <-timer.C:
			// Allow CGV's own token refresh to complete before treating an
			// initial expired response as the final authentication outcome.
			if known, valid := adapter.authEvidence.snapshot(); known && !valid {
				return false, nil
			}
			return false, ErrAuthenticationUnverified
		case <-ticker.C:
		}
	}
}

func (adapter *Adapter) sessionAuthenticationError() error {
	owner := adapter
	for owner.owner != nil {
		owner = owner.owner
	}
	if known, valid := owner.authEvidence.snapshot(); known && !valid {
		return ErrAuthenticationRequired
	}
	return nil
}
