package cgv

import (
	"encoding/json"
	"errors"
	"testing"

	"github.com/cineko-org/client/internal/domain"
)

func TestProviderHTTPErrorClassifiesProtectionResponses(t *testing.T) {
	if err := providerHTTPError(403); !errors.Is(err, ErrProviderAccessBlocked) {
		t.Fatalf("403 = %v", err)
	}
	if err := providerHTTPError(429); !errors.Is(err, ErrProviderThrottled) {
		t.Fatalf("429 = %v", err)
	}
	if err := providerHTTPError(503); errors.Is(err, ErrProviderAccessBlocked) ||
		errors.Is(err, ErrProviderThrottled) {
		t.Fatalf("503 misclassified = %v", err)
	}
}

func TestParseScheduleResponseKeepsProviderTupleWhenDisplayChanges(t *testing.T) {
	rows, err := parseScheduleResponse([]byte(`{
		"statusCode":0,
		"data":[
			{"siteNo":"0056","movNo":"00001234","movNm":"첫 표시명","scnsNo":"0007","scnsNm":"IMAX관","scnYmd":"20260820","scnSseq":"0003","scnsrtTm":"2130","scnendTm":"2350","frSeatCnt":"2","stcnt":"624"},
			{"siteNo":"0056","movNo":"00001234","movNm":"바뀐 표시명","scnsNo":"0007","scnsNm":"IMAX관","scnYmd":"20260820","scnSseq":"0004","scnsrtTm":"2130","scnendTm":"2350","frSeatCnt":"2","stcnt":"624"}
		]
	}`))
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 2 || rows[0].Sequence == rows[1].Sequence {
		t.Fatalf("provider sequence was not preserved: %#v", rows)
	}
	theater := domain.Theater{ID: "theater", Name: "용산아이파크몰"}
	first, err := scheduleEntryFromProviderRow(rows[0], theater)
	if err != nil {
		t.Fatal(err)
	}
	second, err := scheduleEntryFromProviderRow(rows[1], theater)
	if err != nil {
		t.Fatal(err)
	}
	if first.Showtime.SourceKey == second.Showtime.SourceKey || first.Showtime.ID == second.Showtime.ID {
		t.Fatalf("same-time showtimes collapsed: first=%#v second=%#v", first.Showtime, second.Showtime)
	}
	if first.Showtime.MovieID != second.Showtime.MovieID {
		t.Fatalf("display rename changed movie identity: %q != %q", first.Showtime.MovieID, second.Showtime.MovieID)
	}
	if first.Showtime.Movie != "첫 표시명" || second.Showtime.Movie != "바뀐 표시명" {
		t.Fatalf("display snapshots were not retained: %#v %#v", first.Showtime, second.Showtime)
	}
}

func TestParseScheduleResponseRejectsMissingProviderTupleField(t *testing.T) {
	for _, field := range []string{"siteNo", "movNo", "scnsNo", "scnYmd", "scnSseq"} {
		t.Run(field, func(t *testing.T) {
			row := map[string]any{
				"siteNo": "0056", "movNo": "00001234", "scnsNo": "0007", "scnYmd": "20260820",
				"scnSseq": "0003", "scnsrtTm": "2130", "scnendTm": "2350",
			}
			delete(row, field)
			data, err := json.Marshal(map[string]any{"statusCode": 0, "data": []any{row}})
			if err != nil {
				t.Fatal(err)
			}
			if _, err := parseScheduleResponse(data); err == nil {
				t.Fatalf("accepted response without %s", field)
			}
		})
	}
}
