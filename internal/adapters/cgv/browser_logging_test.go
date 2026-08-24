package cgv

import (
	"testing"
)

func TestBrowserRequestPathOmitsQueryAndFragment(t *testing.T) {
	if got := browserRequestPath("https://cgv.co.kr/cnm/movieBook/cinema?token=secret#seat"); got != "/cnm/movieBook/cinema" {
		t.Fatalf("browserRequestPath() = %q", got)
	}
	if got := browserRequestPath("https://cgv.co.kr/%zz"); got != "/" {
		t.Fatalf("invalid browserRequestPath() = %q", got)
	}
}

func TestBrowserRequestFieldsHaveStableCorrelationShape(t *testing.T) {
	fields := browserRequestFields(nil, "request-123")
	if len(fields) != 8 {
		t.Fatalf("fields = %#v", fields)
	}
	if fields[1] != "request-123" || fields[3] != "GET" || fields[5] != "/" || fields[7] != "/" {
		t.Fatalf("fields = %#v", fields)
	}
}

func TestReplaceBrowserDurationUpdatesExistingField(t *testing.T) {
	fields := []any{"request_id", "request-123", "duration_ms", float64(0)}
	updated := replaceBrowserDuration(fields, 12.5)
	if updated[3] != 12.5 {
		t.Fatalf("duration = %#v", updated)
	}
}

func TestBrowserRequestFailureRecognizesStatusAndRawError(t *testing.T) {
	if browserRequestFailed([]any{"status", 200}) {
		t.Fatal("successful browser request reported as failed")
	}
	if !browserRequestFailed([]any{"status", 503}) || !browserRequestFailed([]any{"status", int32(0), "error", "blockedbyclient"}) {
		t.Fatal("failed browser request was not recognized")
	}
}

func TestExpectedBrowserRequestOutcomeRecognizesFiltersAndNavigationCancellation(t *testing.T) {
	if got := expectedBrowserRequestOutcome([]any{"error", "blockedbyclient"}); got != "blocked" {
		t.Fatalf("blocked outcome = %q", got)
	}
	if got := expectedBrowserRequestOutcome([]any{"error", "net::ERR_ABORTED"}); got != "canceled" {
		t.Fatalf("canceled outcome = %q", got)
	}
	if got := expectedBrowserRequestOutcome([]any{"error", "net::ERR_CONNECTION_RESET"}); got != "" {
		t.Fatalf("unexpected failure outcome = %q", got)
	}
}
