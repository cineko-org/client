package cgv

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"time"
)

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
