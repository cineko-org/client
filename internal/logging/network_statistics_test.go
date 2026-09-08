package logging

import (
	"context"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/cineko-org/probe/v2/networkcapture"
	"github.com/cineko-org/probe/v2/networkcapture/playwrightcapture"
	"github.com/mxschmitt/playwright-go"
)

type routineRequest struct{ playwright.Request }

func (routineRequest) Failure() error                                 { return nil }
func (routineRequest) Timing() *playwright.RequestTiming              { return nil }
func (routineRequest) Method() string                                 { return "GET" }
func (routineRequest) URL() string                                    { return "https://cgv.co.kr/example" }
func (routineRequest) ExistingResponse() (playwright.Response, error) { return routineResponse{}, nil }

type routineResponse struct{ playwright.Response }

func (routineResponse) Status() int { return 200 }

func TestRoutineBrowserRequestsCountWithoutReadingBodies(t *testing.T) {
	store, err := networkcapture.NewStore(t.TempDir(), nil)
	if err != nil {
		t.Fatal(err)
	}
	for range 100 {
		// Every unimplemented method panics: a successful request must not read
		// its body, headers or protocol metadata just to increment a counter.
		record := playwrightcapture.PlaywrightRecordForStore(store, routineRequest{}, false)
		if _, err := store.Save(context.Background(), record); err != nil {
			t.Fatal(err)
		}
	}
	for _, record := range []networkcapture.Record{
		{Exchange: networkcapture.Exchange{Outcome: "failed", Request: networkcapture.Request{URL: "https://cgv.co.kr/api"}, Response: &networkcapture.Response{Status: 401}}},
		{Exchange: networkcapture.Exchange{Outcome: "failed", Request: networkcapture.Request{URL: "https://api.cgv.co.kr/api"}, Response: &networkcapture.Response{Status: 429}}},
		{Exchange: networkcapture.Exchange{Outcome: "blocked", Request: networkcapture.Request{URL: "https://cdn.cgv.co.kr/poster"}}},
	} {
		if _, err := store.Save(context.Background(), record); err != nil {
			t.Fatal(err)
		}
	}
	stats := store.SessionStatistics()
	if stats.ProviderSent != 102 || stats.Blocked != 1 || stats.Failed != 2 || stats.Status429 != 1 {
		t.Fatalf("stats = %+v", stats)
	}
	entries, err := networkcapture.List(store.Root(), networkcapture.Query{})
	if err != nil || len(entries) != 2 {
		t.Fatalf("durable records = %d, %v", len(entries), err)
	}
	legacy, err := networkcapture.Stats(store.Root(), networkcapture.Query{})
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("same 103 completions: retained-index count=%d, independent CGV count=%d, routine body reads=0, durable records=%d", legacy.ProviderSent, stats.ProviderSent, len(entries))
	old := time.Now().Add(-time.Second)
	if err := store.Clear(); err != nil {
		t.Fatal(err)
	}
	_, err = store.Save(context.Background(), networkcapture.Record{Exchange: networkcapture.Exchange{StartedAt: old, Request: networkcapture.Request{URL: "https://cgv.co.kr/api"}}})
	if err != nil {
		t.Fatal(err)
	}
	if stats := store.SessionStatistics(); stats.Captured != 0 {
		t.Fatalf("old in-flight completion survived clear: %+v", stats)
	}
}

type successfulTransport struct{}

func (successfulTransport) RoundTrip(*http.Request) (*http.Response, error) {
	return &http.Response{StatusCode: 204, Header: make(http.Header), Body: http.NoBody}, nil
}

func TestSuccessfulHTTPCompletionIsCountedWithoutArtifact(t *testing.T) {
	store, err := networkcapture.NewStore(t.TempDir(), nil)
	if err != nil {
		t.Fatal(err)
	}
	transport := networkcapture.HTTPTransport(store, "test", nil, successfulTransport{})
	request, _ := http.NewRequestWithContext(context.Background(), "GET", "https://cgv.co.kr/test", nil)
	response, err := transport.RoundTrip(request)
	if err != nil {
		t.Fatal(err)
	}
	if err := response.Body.Close(); err != nil {
		t.Fatal(err)
	}
	if got := store.SessionStatistics().ProviderSent; got != 1 {
		t.Fatalf("count=%d", got)
	}
}

func TestCancellationAndNonCGVThrottleAreNotLocalCGVBlocks(t *testing.T) {
	store, err := networkcapture.NewStore(t.TempDir(), nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, record := range []networkcapture.Record{
		{Exchange: networkcapture.Exchange{Outcome: "canceled", Request: networkcapture.Request{URL: "https://cgv.co.kr/api"}, Response: &networkcapture.Response{Status: 200}}},
		{Exchange: networkcapture.Exchange{Outcome: "failed", Request: networkcapture.Request{URL: "https://other.test/api"}, Response: &networkcapture.Response{Status: 429}}},
	} {
		if _, err := store.Save(context.Background(), record); err != nil {
			t.Fatal(err)
		}
	}
	stats := store.SessionStatistics()
	if stats.Blocked != 0 || stats.Canceled != 1 || stats.ProviderSent != 1 || stats.Status429 != 0 {
		t.Fatalf("incorrect classifications: %+v", stats)
	}
}

func TestNetworkSummaryFlushesBeforeLoggerCloses(t *testing.T) {
	var output strings.Builder
	restore := SetOutput(&output)
	defer restore()
	store, err := networkcapture.NewStore(t.TempDir(), nil)
	if err != nil {
		t.Fatal(err)
	}
	stop := StartNetworkSummary(store, time.Hour)
	_, err = store.Save(context.Background(), networkcapture.Record{Exchange: networkcapture.Exchange{Request: networkcapture.Request{URL: "https://cgv.co.kr/example"}}})
	if err != nil {
		t.Fatal(err)
	}
	stop()
	stop()
	if strings.Count(output.String(), `"event":"network.statistics"`) != 1 || !strings.Contains(output.String(), `"provider_completed":1`) || !strings.Contains(output.String(), `"final":true`) {
		t.Fatalf("missing final summary: %s", output.String())
	}
}
