//go:build darwin

package cgv

import (
	"context"
	"fmt"
	"os"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mxschmitt/playwright-go"
)

// No foreground baseline or payment presentation. Sample from BEFORE startup
// so a later hide cannot conceal startup focus theft. Only local data URLs.
func TestWindowControlBackgroundLifecycle(t *testing.T) {
	if os.Getenv("CINEKO_TEST_BACKGROUND_WINDOW") != "1" {
		t.Skip("explicit background-only desktop verification required")
	}
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	var mu sync.Mutex
	samples := make(map[int]int)
	var sampleErr error
	done := make(chan struct{})
	go func() {
		defer close(done)
		for ctx.Err() == nil {
			front, err := runLaunchServices("front")
			var pid int
			if err == nil {
				var info []byte
				info, err = runLaunchServices("info", "-only", "pid", strings.TrimSpace(string(front)))
				if err == nil {
					pid, err = strconv.Atoi(strings.TrimSpace(strings.TrimPrefix(string(info), `"pid"=`)))
				}
			}
			mu.Lock()
			if err != nil {
				sampleErr = err
			} else {
				samples[pid]++
			}
			mu.Unlock()
			select {
			case <-ctx.Done():
				return
			case <-time.After(10 * time.Millisecond):
			}
		}
	}()
	defer func() { cancel(); <-done }()
	config := localBrowserTestConfig(t)
	config.Headless, config.StartMinimized, config.RestoreSession = false, true, true
	started := time.Now()
	root, err := NewAdapter(ctx, config)
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()
	if err := root.browserContext.Route("**/*", func(route playwright.Route) { _ = route.Abort() }); err != nil {
		t.Fatal(err)
	}
	pid, err := root.browserApplicationPID()
	if err != nil {
		t.Fatal(err)
	}
	windowID, err := root.browserWindowID()
	if err != nil {
		t.Fatal(err)
	}
	check := func(stage string) {
		t.Helper()
		mu.Lock()
		count, err := samples[pid], sampleErr
		mu.Unlock()
		if err != nil {
			t.Fatalf("focus sampler: %v", err)
		}
		if count != 0 {
			t.Fatalf("STOP: test Chrome took focus at %s (%d samples)", stage, count)
		}
		bounds, err := root.readBrowserWindowBounds(windowID)
		if err != nil {
			t.Fatal(err)
		}
		hidden, err := browserApplicationHidden(pid)
		if err != nil || !hidden || bounds.Bounds.WindowState != "minimized" {
			t.Fatalf("%s: hidden=%v window=%s err=%v", stage, hidden, bounds.Bounds.WindowState, err)
		}
	}
	check("startup")
	t.Logf("startup %.3fs; Chrome PID=%d; no Chrome foreground samples", time.Since(started).Seconds(), pid)
	for index := range 6 {
		tab, err := root.OpenTab(ctx)
		if err != nil {
			t.Fatal(err)
		}
		check("OpenTab")
		if _, err := tab.page.Goto(fmt.Sprintf("data:text/html,<title>Cineko local %d</title>", index)); err != nil {
			t.Fatal(err)
		}
		check("navigation")
		tab.Close()
		check("close")
	}
	// Disable corrective hide callbacks WITHOUT showing anything. Exercise the
	// very same page factory; otherwise post-create hiding can mask regressions.
	root.paymentHandoff.Store(true)
	for range 6 {
		page, err := root.newBookingPage(ctx)
		if err != nil {
			t.Fatal(err)
		}
		check("inactive create without corrective hiding")
		if _, err := page.Goto("data:text/html,<title>Cineko background fixture</title>"); err != nil {
			t.Fatal(err)
		}
		check("navigation without corrective hiding")
		if err := page.Close(); err != nil {
			t.Fatal(err)
		}
		check("close without corrective hiding")
	}
	if count := root.ProcessPageCount(); count != 1 {
		t.Fatalf("leaked pages: %d", count)
	}
	t.Log("checking the same controller after 35 seconds idle; no visible handoff")
	select {
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	case <-time.After(35 * time.Second):
	}
	page, err := root.newBookingPage(ctx)
	if err != nil {
		t.Fatalf("post-idle tab: %v", err)
	}
	check("post-idle creation")
	if err := page.Close(); err != nil {
		t.Fatal(err)
	}
	firstPID := pid
	root.Close()
	started = time.Now()
	root, err = NewAdapter(ctx, config)
	if err != nil {
		t.Fatalf("restart same profile: %v", err)
	}
	defer root.Close()
	if err := root.browserContext.Route("**/*", func(route playwright.Route) { _ = route.Abort() }); err != nil {
		t.Fatal(err)
	}
	pid, err = root.browserApplicationPID()
	if err != nil {
		t.Fatal(err)
	}
	windowID, err = root.browserWindowID()
	if err != nil {
		t.Fatal(err)
	}
	check("same profile restart")
	t.Logf("same profile restart %.3fs; Chrome PID=%d; no foreground samples", time.Since(started).Seconds(), pid)
	tab, err := root.OpenTab(ctx)
	if err != nil {
		t.Fatal(err)
	}
	check("restarted OpenTab")
	tab.Close()
	check("restarted close")
	root.Close()
	cancel()
	<-done
	mu.Lock()
	total := 0
	for _, count := range samples {
		total += count
	}
	foreground := samples[firstPID] + samples[pid]
	mu.Unlock()
	if foreground != 0 {
		t.Fatalf("Chrome foreground samples=%d", foreground)
	}
	t.Logf("12 create/navigate/close rounds + idle + profile restart; foreground Chrome samples=0/%d; no tab leaks or visible handoff", total)
}
