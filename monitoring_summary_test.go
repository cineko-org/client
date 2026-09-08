package main

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"testing/synctest"
	"time"

	"github.com/cineko-org/client/internal/logging"
	commonpb "github.com/cineko-org/contracts/v3/gen/go/cineko/common"
	observationpb "github.com/cineko-org/contracts/v3/gen/go/cineko/observation"
	"github.com/cineko-org/probe/v2/probe"
	"google.golang.org/protobuf/proto"
)

func summaryCapture(complete bool) *observationpb.Capture {
	return observationpb.Capture_builder{Complete: &complete, TargetDate: commonpb.LocalDate_builder{Year: proto.Int32(2026), Month: proto.Int32(9), Day: proto.Int32(11)}.Build()}.Build()
}

func TestMonitoringSummaryAttributesOutcomes(t *testing.T) {
	now := time.Now()
	summary := newMonitoringSummary(now)
	cases := []struct {
		captures []*observationpb.Capture
		err      error
	}{
		{[]*observationpb.Capture{summaryCapture(true)}, nil},
		{[]*observationpb.Capture{summaryCapture(false)}, nil},
		{[]*observationpb.Capture{summaryCapture(true)}, errors.New("store failed")},
		{nil, errors.New("no response")}, {nil, nil}, {nil, context.Canceled}, {nil, probe.ErrProviderThrottled},
	}
	for _, c := range cases {
		summary.scanStarted("theater", now)
		summary.scanFinished("theater", now, now.Add(time.Second), c.captures, c.err)
	}
	s := summary.take(now.Add(5*time.Minute), false).Theaters["theater"]
	if s.Started != 7 || s.Completed != 7 || s.InFlight != 0 || s.Succeeded != 2 || s.Partial != 1 || s.Failed != 2 || s.Canceled != 1 || s.Throttled != 1 || s.NoDetailCapture != 1 || s.UnknownDateFailures != 1 || s.DurationMS != 7000 {
		t.Fatalf("counts=%+v", s)
	}
	if s.Dates["2026-09-11"] != (dateScanSummary{Succeeded: 1, Failed: 1, Partial: 1}) {
		t.Fatalf("dates=%+v", s.Dates)
	}
}

func TestMonitoringSummaryWindowCarryAndHeartbeat(t *testing.T) {
	now := time.Now()
	summary := newMonitoringSummary(now)
	if summary.take(now, false) != nil {
		t.Fatal("idle summary")
	}
	summary.inventory([]string{"monitor"}, now)
	summary.scanStarted("theater", now)
	first := summary.take(now.Add(time.Minute), false)
	summary.scanFinished("theater", now, now.Add(2*time.Minute), nil, nil)
	second := summary.take(now.Add(5*time.Minute), false)
	if first.Theaters["theater"].InFlight != 1 || second.Theaters["theater"].InFlight != 0 || second.Theaters["theater"].Started != 0 || second.Theaters["theater"].Completed != 1 {
		t.Fatal("cross-window capture lost")
	}
	heartbeat := summary.take(now.Add(10*time.Minute), false)
	if heartbeat == nil || heartbeat.Theaters["theater"].Completed != 0 || heartbeat.Theaters["theater"].LastSuccessAt == "" {
		t.Fatal("heartbeat or last success lost")
	}
}

func TestMonitoringSummaryDeduplicatesNewSchedules(t *testing.T) {
	now := time.Now()
	summary := newMonitoringSummary(now)
	worker := &desktopMonitorWorker{summary: summary}
	old := testLocalMonitorTarget("old", now.Add(time.Hour))
	opened := testLocalMonitorTarget("new", now.Add(time.Hour))
	worker.observeInventory(t.Context(), &localMonitorInventory{activeMonitorIDs: []string{old.monitorID}, targets: []*localMonitorTarget{old}}, now)
	for range 3 {
		worker.observeInventory(t.Context(), &localMonitorInventory{activeMonitorIDs: []string{old.monitorID}, targets: []*localMonitorTarget{old, opened}}, now)
	}
	if summary.take(now, false).Discovered[old.monitorID] != 1 {
		t.Fatal("baseline or duplicate counted")
	}
}

func TestMonitoringSummaryConcurrentAggregationAndInfoFlush(t *testing.T) {
	var output strings.Builder
	restore := logging.SetOutput(&output)
	defer restore()
	restoreDebug := logging.SetDebug(false)
	defer restoreDebug()
	now := time.Now()
	summary := newMonitoringSummary(now)
	var wg sync.WaitGroup
	for range 600 {
		wg.Go(func() { summary.scanStarted("theater", now); summary.scanFinished("theater", now, now, nil, nil) })
	}
	wg.Wait()
	stop := summary.start(time.Hour)
	stop()
	if strings.Count(output.String(), `"event":"monitoring.summary"`) != 1 || !strings.Contains(output.String(), `"completed":600`) || !strings.Contains(output.String(), `"level":"INFO"`) {
		t.Fatalf("summary=%s", output.String())
	}
	t.Log("600 completed captures -> 1 INFO summary; per-capture INFO logs=0")
}

func TestScannerStatusTracksActualWork(t *testing.T) {
	embedded := &embeddedProbe{}
	if embedded.ScanStatus() != "off" {
		t.Fatal("unstarted scan must be off")
	}
	err := embedded.withScan(t.Context(), func(context.Context) error {
		if embedded.ScanStatus() != "checking" {
			t.Fatal("in-flight scan not checking")
		}
		return errors.New("proxy unavailable")
	})
	if err == nil || embedded.ScanStatus() != "failed" {
		t.Fatal("failed scan shown healthy")
	}
	_ = embedded.withScan(t.Context(), func(context.Context) error { return context.Canceled })
	if embedded.ScanStatus() != "failed" {
		t.Fatal("cancellation erased failure")
	}
	_ = embedded.withScan(t.Context(), func(context.Context) error { return nil })
	if embedded.ScanStatus() != "ready" {
		t.Fatal("successful scan did not recover")
	}
}

func TestConfiguredScannerStartsCheckingNotOff(t *testing.T) {
	configured := probe.ScannerSettings{URL: "http://soxy.local:8080", HasToken: true}
	if initialScannerStatus(configured, nil) != "checking" {
		t.Fatal("configured SOXY shown off while waiting for first scan")
	}
	if initialScannerStatus(probe.ScannerSettings{}, nil) != "off" {
		t.Fatal("missing settings not off")
	}
	if initialScannerStatus(configured, errors.New("invalid config")) != "failed" {
		t.Fatal("invalid configuration hidden")
	}
}

func TestMonitoringSummaryFiveMinuteHeartbeatWithoutScanProgress(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		var output strings.Builder
		restore := logging.SetOutput(&output)
		defer restore()
		summary := newMonitoringSummary(time.Now())
		summary.inventory([]string{"monitor"}, time.Now())
		stop := summary.start(5 * time.Minute)
		synctest.Wait()
		time.Sleep(5 * time.Minute)
		synctest.Wait()
		if strings.Count(output.String(), `"event":"monitoring.summary"`) != 1 || !strings.Contains(output.String(), `"final":false`) {
			t.Fatalf("missing scheduled heartbeat: %s", output.String())
		}
		stop()
	})
}
