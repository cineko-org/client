package cgv

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"time"

	"github.com/cineko-org/client/internal/domain"
)

type Credentials = domain.AccountCredentials

type CaptchaPrompt func() error

func (adapter *Adapter) AuthenticateManuallyUntil(ctx context.Context, timeout time.Duration) error {
	adapter.mu.Lock()
	defer adapter.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return err
	}
	if timeout <= 0 {
		return errors.New("manual login timeout must be positive")
	}
	authenticated, err := adapter.openAccountHome()
	if err != nil {
		return err
	}
	if authenticated {
		if err := adapter.saveSessionState(); err != nil {
			return err
		}
		return adapter.waitForVisibleBrowser(ctx, timeout)
	}
	return adapter.waitForManualLogin(ctx, timeout)
}

func (adapter *Adapter) openAccountHome() (bool, error) {
	if err := adapter.navigate(homeURL); err != nil {
		return false, fmt.Errorf("open CGV: %w", err)
	}
	return adapter.authenticatedState()
}

func (adapter *Adapter) waitForManualLogin(ctx context.Context, timeout time.Duration) error {
	if err := adapter.navigate(loginURL); err != nil {
		return fmt.Errorf("open CGV login: %w", err)
	}
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	ticker := time.NewTicker(500 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-timer.C:
			return errors.New("manual CGV login timed out")
		case <-ticker.C:
			if adapter.browserContext.IsClosed() {
				return errors.New("CGV login browser was closed before login completed")
			}
			currentURL := adapter.page.URL()
			parsed, err := url.Parse(currentURL)
			if err != nil || parsed.Hostname() != "cgv.co.kr" || containsPath(parsed.Path, "/login") {
				continue
			}
			authenticated, err := adapter.authenticatedState()
			if err != nil {
				return err
			}
			if authenticated {
				return adapter.saveSessionState()
			}
		}
	}
}

func (adapter *Adapter) waitForVisibleBrowser(ctx context.Context, timeout time.Duration) error {
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	ticker := time.NewTicker(250 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-timer.C:
			return nil
		case <-ticker.C:
			if adapter.browserContext.IsClosed() {
				return nil
			}
		}
	}
}

func (adapter *Adapter) AuthenticateManually(ctx context.Context, prompt CaptchaPrompt) error {
	adapter.mu.Lock()
	defer adapter.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return err
	}
	if prompt == nil {
		return errors.New("manual login prompt is required")
	}
	if err := adapter.navigate(loginURL); err != nil {
		return fmt.Errorf("open CGV login: %w", err)
	}
	if err := prompt(); err != nil {
		return err
	}
	if err := adapter.verifyAuthenticatedUnlocked(); err != nil {
		return err
	}
	return adapter.saveSessionState()
}

func (adapter *Adapter) Authenticate(
	ctx context.Context,
	credentials Credentials,
	prompt CaptchaPrompt,
) error {
	adapter.mu.Lock()
	defer adapter.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := adapter.navigate(loginURL); err != nil {
		return fmt.Errorf("open CGV login: %w", err)
	}
	if err := adapter.fillLoginForm(credentials); err != nil {
		return err
	}
	if err := adapter.resolveCaptcha(prompt); err != nil {
		return err
	}
	if err := adapter.submitLogin(); err != nil {
		return err
	}
	if err := adapter.verifyAuthenticatedUnlocked(); err != nil {
		return err
	}
	return adapter.saveSessionState()
}

// AuthenticateSavedUntil prefills credentials from the operating system vault
// and waits for the user to complete CAPTCHA and submit the visible login form.
func (adapter *Adapter) AuthenticateSavedUntil(
	ctx context.Context,
	credentials domain.AccountCredentials,
	timeout time.Duration,
) error {
	adapter.mu.Lock()
	defer adapter.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return err
	}
	if timeout <= 0 {
		return errors.New("saved login timeout must be positive")
	}
	if err := credentials.Validate(); err != nil {
		return err
	}
	if err := adapter.navigate(loginURL); err != nil {
		return fmt.Errorf("open CGV login: %w", err)
	}
	if err := adapter.fillLoginForm(credentials); err != nil {
		return err
	}
	deadline := time.Now().Add(timeout)
	if err := adapter.waitForAuthenticatedState(ctx, deadline); err != nil {
		return err
	}
	return adapter.saveSessionState()
}

func (adapter *Adapter) fillLoginForm(credentials Credentials) error {
	if credentials.ID != "" {
		if err := adapter.page.Locator(`input[placeholder^="CJ ONE 통합 아이디"]`).Fill(credentials.ID); err != nil {
			return fmt.Errorf("fill CGV id: %w", err)
		}
	}
	if credentials.Password != "" {
		if err := adapter.page.Locator(`input[type="password"]`).Fill(credentials.Password); err != nil {
			return fmt.Errorf("fill CGV password: %w", err)
		}
	}
	return nil
}

func (adapter *Adapter) resolveCaptcha(prompt CaptchaPrompt) error {
	captchaVisible, err := adapter.captchaVisible()
	if err != nil {
		return err
	}
	if captchaVisible {
		if prompt == nil {
			return ErrCaptchaRequired
		}
		if err := prompt(); err != nil {
			return err
		}
	}
	return nil
}

func (adapter *Adapter) captchaVisible() (bool, error) {
	var visible bool
	if err := adapter.evaluate(`(() => {
		const input = window.__cinekoQueryAll('input')
			.find(element => (element.getAttribute('placeholder') || '').includes('자동입력 방지문자'));
		return !!(input && input.offsetParent !== null);
	})()`, &visible); err != nil {
		return false, err
	}
	return visible, nil
}

func (adapter *Adapter) waitForAuthenticatedState(ctx context.Context, deadline time.Time) error {
	ticker := time.NewTicker(500 * time.Millisecond)
	defer ticker.Stop()
	for {
		if time.Now().After(deadline) {
			return errors.New("timed out waiting for CGV login")
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
			authenticated, err := adapter.authenticatedState()
			if err != nil {
				return err
			}
			if authenticated {
				return nil
			}
		}
	}
}

func (adapter *Adapter) submitLogin() error {
	currentURL := adapter.page.URL()
	if currentURL != loginURL && !containsPath(currentURL, "/mem/login") {
		return nil
	}
	clicked, err := adapter.clickButtonExact("로그인")
	if err != nil {
		return err
	}
	if !clicked {
		return fmt.Errorf("%w: login submit button not found", ErrUIContractChanged)
	}
	return adapter.wait(time.Second)
}

func (adapter *Adapter) IsAuthenticated(ctx context.Context) (bool, error) {
	adapter.mu.Lock()
	defer adapter.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return false, err
	}
	if err := adapter.navigate(homeURL); err != nil {
		return false, err
	}
	authenticated, err := adapter.authenticatedState()
	if err != nil || !authenticated {
		return authenticated, err
	}
	if err := adapter.saveSessionState(); err != nil {
		return false, err
	}
	return true, nil
}

func (adapter *Adapter) verifyAuthenticatedUnlocked() error {
	if err := adapter.navigate(homeURL); err != nil {
		return err
	}
	authenticated, err := adapter.authenticatedState()
	if err != nil {
		return err
	}
	if !authenticated {
		return ErrAuthenticationRequired
	}
	return nil
}

func (adapter *Adapter) authenticatedState() (bool, error) {
	var state struct {
		HasLogout bool `json:"hasLogout"`
	}
	err := adapter.evaluate(`(() => {
		const normalize = value => (value || '').replace(/\s+/g, ' ').trim();
		const labels = window.__cinekoQueryAll('button').map(button => normalize(button.innerText || button.textContent));
		return {hasLogout: labels.some(label => label.includes('로그아웃'))};
	})()`, &state)
	if err != nil {
		return false, err
	}
	return state.HasLogout, nil
}

func containsPath(url, path string) bool {
	for index := 0; index+len(path) <= len(url); index++ {
		if url[index:index+len(path)] == path {
			return true
		}
	}
	return false
}
