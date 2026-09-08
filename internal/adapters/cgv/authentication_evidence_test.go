package cgv

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/mxschmitt/playwright-go"
)

func TestAuthenticationResponseRequiresServerEvidence(t *testing.T) {
	for _, tc := range []struct {
		name         string
		status       int
		body         string
		known, valid bool
	}{
		{"expired_http", 401, `{"statusCode":-1001}`, true, false},
		{"expired_business", 200, `{"statusCode":-1001}`, true, false},
		{"authenticated", 200, `{"statusCode":0}`, true, true},
		{"authenticated_string", 200, `{"statusCode":"0"}`, true, true},
		{"unexpected_schema", 200, `{"anything":true}`, false, false},
		{"html", 200, `<button>로그아웃</button>`, false, false},
		{"throttled", 429, `{"statusCode":0}`, false, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			known, valid := authenticationResponse(tc.status, []byte(tc.body))
			if known != tc.known || valid != tc.valid {
				t.Fatalf("got %v/%v", known, valid)
			}
		})
	}
}

type logoutButtonPage struct{ playwright.Page }

func (logoutButtonPage) Evaluate(string, ...any) (any, error) {
	return map[string]any{"hasLogout": true}, nil
}

func TestLogoutButtonDoesNotOverrideExpiredOrMissingAuthentication(t *testing.T) {
	adapter := &Adapter{ctx: context.Background(), page: logoutButtonPage{}}
	for _, valid := range []bool{false, true, false} {
		adapter.authEvidence.observe(time.Now(), valid)
		got, err := adapter.authenticatedState()
		if err != nil || got != valid {
			t.Fatalf("DOM logout + server=%v yielded %v/%v", valid, got, err)
		}
	}
	adapter.authEvidence.reset()
	if got, err := adapter.authenticatedState(); err != nil || got {
		t.Fatalf("no evidence yielded %v/%v", got, err)
	}
}

func TestProviderExpiryRemainsTypedThroughSeatAndScheduleParsers(t *testing.T) {
	if !errors.Is(providerHTTPError(401), ErrAuthenticationRequired) {
		t.Fatal("HTTP401 lost authentication classification")
	}
	if _, err := parseScheduleResponse([]byte(`{"statusCode":-1001}`)); !errors.Is(err, ErrAuthenticationRequired) {
		t.Fatalf("schedule error=%v", err)
	}
	if _, err := parseSeatSnapshot([]byte(`{"statusCode":-1001}`), "auditorium", time.Now()); !errors.Is(err, ErrAuthenticationRequired) {
		t.Fatalf("seat error=%v", err)
	}
}

func TestAuthenticationEvidenceRejectsStaleAndSharesTabRevocation(t *testing.T) {
	owner := &Adapter{}
	owner.authEvidence.reset()
	old := time.Now().Add(-time.Minute)
	owner.authEvidence.observe(old, true)
	if owner.authEvidence.verified() {
		t.Fatal("previous navigation authenticated current page")
	}
	now := time.Now()
	owner.authEvidence.observe(now, true)
	if !owner.authEvidence.verified() {
		t.Fatal("fresh success not recognized")
	}
	owner.authEvidence.observe(now.Add(time.Second), false)
	owner.authEvidence.observe(now, true)
	tab := &Adapter{owner: owner}
	if !errors.Is(tab.sessionAuthenticationError(), ErrAuthenticationRequired) {
		t.Fatal("tab ignored expired parent session")
	}
	owner.authEvidence.reset()
	if owner.authEvidence.verified() {
		t.Fatal("reset retained success")
	}
}

func TestAuthenticationEndpointRejectsForeignAndPublicResponses(t *testing.T) {
	for _, raw := range []string{"https://evil.test/api/v1/mypage/tkt/mblTkt/searchMblTktTabPrdtypList", "https://cgv.co.kr/api/v1/booking/searchMovScnInfo", "https://cdn.cgv.co.kr/poster.jpg"} {
		if _, trusted := authenticationEndpoint(raw); trusted {
			t.Fatalf("trusted %s", raw)
		}
	}
}
