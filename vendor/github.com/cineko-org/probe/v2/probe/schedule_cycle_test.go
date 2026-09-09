package probe

import (
	"context"
	"errors"
	"testing"
	"time"

	catalogpb "github.com/cineko-org/contracts/v3/gen/go/cineko/catalog"
	observationpb "github.com/cineko-org/contracts/v3/gen/go/cineko/observation"
	"github.com/cineko-org/probe/v2/internal/provider/cgv"
)

type cycleFixtureBrowser struct{ afterPublish error }

func (*cycleFixtureBrowser) Close() {}
func (*cycleFixtureBrowser) CaptureSchedules(context.Context, cgv.ScheduleTheater, []string) ([]cgv.ScheduleCapture, error) {
	return nil, errors.New("unexpected non-cycle request")
}
func (browser *cycleFixtureBrowser) CaptureScheduleWeekdayCycle(_ context.Context, _ cgv.ScheduleTheater, _ []time.Weekday, cycle cgv.ScheduleCycle) error {
	if err := cycle.Publish(cgv.ScheduleCapture{TargetDate: "2026-09-11", Complete: true}); err != nil {
		return err
	}
	return browser.afterPublish
}

func TestScheduleCyclePublishesBeforeLaterThrottle(t *testing.T) {
	theater := &catalogpb.Theater{}
	theater.SetId(cgv.CatalogID(cgv.ProviderCGV, "theater", "0013"))
	theater.SetProviderId(cgv.ProviderCGV)
	theater.SetIdentity(cgv.NewTheaterIdentity("0013"))
	theater.SetRegion("서울")
	theater.SetName("용산")
	zone := "Asia/Seoul"
	task := &observationpb.AssignmentTask{}
	task.SetEgress(managedScanEgress())
	task.SetSchedule(observationpb.ScheduleTask_builder{Theater: theater, TimeZone: &zone}.Build())
	executor := &CGVExecutor{clock: time.Now}
	published := 0
	cycle := ScheduleCycle{Publish: func(capture *observationpb.Capture) error {
		published++
		if !capture.GetComplete() || capture.GetTargetDate().GetDay() != 11 {
			t.Fatal(capture)
		}
		return nil
	}}
	values, err := executor.captureScheduleWeekdayCycleInBrowser(t.Context(), task, []time.Weekday{time.Friday}, &cycleFixtureBrowser{afterPublish: cgv.ErrProviderThrottled}, cycle)
	if !errors.Is(err, cgv.ErrProviderThrottled) || published != 1 || len(values) != 1 {
		t.Fatal("successful date lost on later throttle", values, published, err)
	}
	storageErr := errors.New("storage failed")
	cycle.Publish = func(*observationpb.Capture) error { return storageErr }
	values, err = executor.captureScheduleWeekdayCycleInBrowser(t.Context(), task, []time.Weekday{time.Friday}, &cycleFixtureBrowser{}, cycle)
	if !errors.Is(err, storageErr) || len(values) != 0 {
		t.Fatal("unpublished data reported as stored", values, err)
	}
}
