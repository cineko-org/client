package main

import (
	"context"
	"testing"
	"time"

	"github.com/cineko-org/client/internal/interfaces/webui"
)

func TestEmbeddedProbeKeepsScanningWhileBookingOpens(t *testing.T) {
	t.Parallel()
	embedded := &embeddedProbe{}
	scanStarted := make(chan struct{})
	releaseScan := make(chan struct{})
	scanDone := make(chan error, 1)
	go func() {
		scanDone <- embedded.withScan(context.Background(), func(ctx context.Context) error {
			close(scanStarted)
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-releaseScan:
				return nil
			}
		})
	}()
	<-scanStarted

	if _, err := embedded.OpenBooking(func() (webui.Automation, error) { return nil, nil }); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-scanDone:
		t.Fatalf("booking interrupted active scan: %v", err)
	case <-time.After(20 * time.Millisecond):
	}
	close(releaseScan)
	if err := <-scanDone; err != nil {
		t.Fatal(err)
	}
}

func TestMonitorProviderWeekdaysIncludeExtendedHourSourceDay(t *testing.T) {
	t.Parallel()
	weekdays := make(map[int32]struct{})
	addMonitorProviderWeekdays(weekdays, []int32{int32(time.Monday), int32(time.Friday)})
	for _, want := range []struct {
		value int32
		name  time.Weekday
	}{{0, time.Sunday}, {1, time.Monday}, {4, time.Thursday}, {5, time.Friday}} {
		if _, exists := weekdays[want.value]; !exists {
			t.Fatalf("provider weekday %s missing from %v", want.name, weekdays)
		}
	}
	if len(weekdays) != 4 {
		t.Fatalf("provider weekdays = %v, want four unique days", weekdays)
	}
}
