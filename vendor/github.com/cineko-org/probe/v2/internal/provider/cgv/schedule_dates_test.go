package cgv

import (
	"errors"
	"net/url"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/mxschmitt/playwright-go"
)

func TestProviderDateInventoryUsesExactDates(t *testing.T) {
	dates, err := parseScheduleDatesResponse([]byte(`{"statusCode":0,"data":[{"scnYmd":"20270102","hldyYn":"Y"},{"scnYmd":"20260911"},{"scnYmd":"20260911"}]}`))
	if err != nil || len(dates) != 2 || dates[0] != "2026-09-11" || dates[1] != "2027-01-02" {
		t.Fatal(dates, err)
	}
	for _, body := range []string{`{}`, `{"statusCode":1,"data":[]}`, `{"statusCode":0,"data":null}`, `{"statusCode":0,"data":[{}]}`, `{"statusCode":0,"data":[{"scnYmd":"20260230"}]}`} {
		if _, err := parseScheduleDatesResponse([]byte(body)); err == nil {
			t.Fatalf("accepted invalid inventory %s", body)
		}
	}
	if dates, err := parseScheduleDatesResponse([]byte(`{"statusCode":0,"data":[]}`)); err != nil || len(dates) != 0 {
		t.Fatal(dates, err)
	}
}

func TestScheduleMovieInventoryDoesNotFilterCompleteDetails(t *testing.T) {
	for _, movie := range []string{"", "30001323"} {
		path, err := scheduleInventoryPath("0013", movie)
		if err != nil {
			t.Fatal(err)
		}
		parsed, err := url.Parse(path)
		if err != nil || parsed.IsAbs() || parsed.Host != "" {
			t.Fatal("inventory must remain on the CGV browser origin", path, err)
		}
		want := scheduleDatesResponsePath
		if movie != "" {
			want = movieScheduleDatesResponsePath
		}
		if parsed.Path != want || parsed.Query().Get("movNo") != movie || parsed.Query().Get("siteNo") != "0013" {
			t.Fatal(path)
		}
	}
	if _, err := scheduleInventoryPath("0013", "not-a-movie"); err == nil {
		t.Fatal("accepted invalid movie identity")
	}
	path, err := scheduleRequestPath("2026-09-11", "0013")
	if err != nil {
		t.Fatal(err)
	}
	parsed, err := url.Parse(path)
	if err != nil || parsed.Path != scheduleResponsePath || parsed.Query().Has("movNo") || parsed.Query().Has("attrCd") {
		t.Fatal("complete theater/date snapshot became movie-filtered", path, err)
	}
}

// Every URL is fulfilled locally. This exercises the real browser boundary
// without sending any requests to CGV or using the user's browser profile.
func TestScheduleInventoryBrowserRequestBudget(t *testing.T) {
	if testing.Short() {
		t.Skip("requires installed Chromium; all responses are local fixtures")
	}
	for _, movieNo := range []string{"", "30001323"} {
		name := "theater"
		if movieNo != "" {
			name = "movie"
		}
		t.Run(name, func(t *testing.T) { testScheduleInventoryBrowserRequestBudget(t, movieNo) })
	}
}

func testScheduleInventoryBrowserRequestBudget(t *testing.T, movieNo string) {
	t.Helper()
	config := DefaultBrowserConfig()
	config.ChromePath = os.Getenv("CINEKO_CHROME_PATH")
	config.ProfileDir = t.TempDir()
	config.ArtifactsDir = t.TempDir()
	adapter, err := NewAdapter(t.Context(), config)
	if err != nil {
		t.Fatal(err)
	}
	defer adapter.Close()

	var mu sync.Mutex
	var requests []string
	body := `{"statusCode":0,"data":[{"scnYmd":"20260911"},{"scnYmd":"20260912"},{"scnYmd":"20260913"}]}`
	status := 200
	if err := adapter.page.Route("**/*", func(route playwright.Route) {
		parsed, parseErr := url.Parse(route.Request().URL())
		if parseErr != nil {
			t.Error(parseErr)
			_ = route.Abort()
			return
		}
		mu.Lock()
		requests = append(requests, parsed.RequestURI())
		responseBody, responseStatus := body, status
		mu.Unlock()
		options := playwright.RouteFulfillOptions{Status: playwright.Int(200), ContentType: playwright.String("application/json")}
		switch parsed.Path {
		case "/cnm/movieBook/cinema":
			options.ContentType = playwright.String("text/html")
			options.Body = "<html><body>local fixture</body></html>"
		case scheduleDatesResponsePath, movieScheduleDatesResponsePath:
			wantPath := scheduleDatesResponsePath
			if movieNo != "" {
				wantPath = movieScheduleDatesResponsePath
			}
			if parsed.Path != wantPath || parsed.Query().Get("siteNo") != "0013" || parsed.Query().Get("movNo") != movieNo {
				t.Error("wrong inventory scope", parsed.RequestURI())
			}
			options.Body, options.Status = responseBody, playwright.Int(responseStatus)
			if responseStatus == 429 {
				options.Headers = map[string]string{"Retry-After": "60"}
			}
		case scheduleResponsePath:
			if parsed.Query().Has("movNo") || parsed.Query().Has("attrCd") {
				t.Error("complete detail request was filtered", parsed.RequestURI())
			}
			options.Body = `{"statusCode":0,"data":[]}`
		default:
			t.Errorf("unexpected request: %s", parsed.RequestURI())
			_ = route.Abort()
			return
		}
		if err := route.Fulfill(options); err != nil {
			t.Error(err)
		}
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := adapter.page.Goto(bookingCinemaURL); err != nil {
		t.Fatal(err)
	}
	adapter.selectedRegion, adapter.selectedTheater = "서울", "용산"
	adapter.selectedTheaterAt = time.Now().Add(-6 * time.Minute)
	theater := ScheduleTheater{ID: "yongsan", SourceKey: "0013", Region: "서울", Name: "용산"}
	weekdays := []time.Weekday{time.Friday, time.Saturday, time.Sunday}
	for slot := range 4 {
		if slot == 3 {
			mu.Lock()
			body = `{"statusCode":0,"data":[{"scnYmd":"20260911"},{"scnYmd":"20260912"},{"scnYmd":"20260913"},{"scnYmd":"20260918"}]}`
			mu.Unlock()
		}
		captures, err := adapter.CaptureScheduleWeekdayShard(t.Context(), theater, weekdays, slot, movieNo)
		if err != nil || len(captures) != 1 || !captures[0].Complete {
			t.Fatalf("slot %d: %+v, %v", slot, captures, err)
		}
		if slot == 3 && captures[0].TargetDate != "2026-09-18" {
			t.Fatal("new inventory date was not checked in the same slot")
		}
	}
	mu.Lock()
	count := len(requests)
	mu.Unlock()
	if count != 9 {
		t.Fatalf("four slots: got %d requests, want 1 bootstrap + 4 inventory + 4 detail", count)
	}
	if movieNo != "" {
		captures, err := adapter.CaptureScheduleWeekdayShard(t.Context(), theater, weekdays, 4, movieNo)
		if err != nil || len(captures) != 0 {
			t.Fatal("unchanged movie inventory triggered an early detail", captures, err)
		}
		mu.Lock()
		count = len(requests)
		mu.Unlock()
		if count != 10 {
			t.Fatalf("inventory-only round: got %d requests, want 10 total", count)
		}
	}
	mu.Lock()
	requests = nil
	body = `{"statusCode":0,"data":[]}`
	mu.Unlock()
	captures, err := adapter.CaptureScheduleWeekdayShard(t.Context(), theater, weekdays, 5, movieNo)
	if err != nil || len(captures) != 0 {
		t.Fatal(captures, err)
	}
	mu.Lock()
	count = len(requests)
	status = 429
	mu.Unlock()
	if count != 1 {
		t.Fatalf("empty inventory triggered extra requests: %d", count)
	}
	if _, err := adapter.CaptureScheduleWeekdayShard(t.Context(), theater, weekdays, 6, movieNo); !errors.Is(err, ErrProviderThrottled) {
		t.Fatalf("429: %v", err)
	}
	for range 3 {
		if _, err := adapter.CaptureScheduleWeekdayShard(t.Context(), theater, weekdays, 7, movieNo); !errors.Is(err, ErrProviderThrottled) {
			t.Fatalf("cooldown: %v", err)
		}
	}
	mu.Lock()
	count = len(requests)
	mu.Unlock()
	if count != 2 {
		t.Fatalf("429 must stop details and later requests, got %d", count)
	}
	t.Log("4 scan slots: 4 inventories + 4 details, 0 reloads despite expired DOM age; empty inventory: 1 request; 429 + 3 retries: 1 request total")
}

func TestNewInventoryDateWinsSameScanSlot(t *testing.T) {
	var rotation scheduleDateRotation
	for i := range 3 {
		rotation.next([]string{"2026-09-11", "2026-09-12", "2026-09-13"}, i)
	}
	dates, err := parseScheduleDatesResponse([]byte(`{"statusCode":0,"data":[{"scnYmd":"20260911"},{"scnYmd":"20260912"},{"scnYmd":"20260913"},{"scnYmd":"20260918"}]}`))
	if err != nil {
		t.Fatal(err)
	}
	dates, err = filterScheduleDatesByWeekdays(dates, []time.Weekday{time.Friday, time.Saturday, time.Sunday})
	if err != nil || rotation.next(dates, 3) != "2026-09-18" {
		t.Fatal("new date not inspected immediately", dates, err)
	}
	t.Log("same 30s scan slots: new date inventory observed next slot, not after a 300s page reload; one inventory + at most one detail request per slot")
}
