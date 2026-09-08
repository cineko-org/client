package cgv

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"sort"
	"strings"
)

const scheduleDatesResponsePath = "/api/v1/booking/searchSiteScnscYmdListBySite"
const movieScheduleDatesResponsePath = "/api/v1/booking/searchSiteScnscYmdListByMov"

func scheduleInventoryPath(siteNo, movieNo string) (string, error) {
	siteNo = strings.TrimSpace(siteNo)
	if !providerSiteIdentifier(siteNo) {
		return "", fmt.Errorf("%w: invalid theater siteNo", ErrIdentityMismatch)
	}
	query := url.Values{"coCd": {"A420"}, "siteNo": {siteNo}}
	path := scheduleDatesResponsePath
	if movieNo != "" {
		if !numericIdentifier(movieNo) {
			return "", fmt.Errorf("%w: invalid movie number", ErrIdentityMismatch)
		}
		query.Set("movNo", movieNo)
		path = movieScheduleDatesResponsePath
	}
	return path + "?" + query.Encode(), nil
}

// Use CGV's cinema date-inventory request on the existing scan slot: one
// inventory plus at most one date-detail request, with no reload or fan-out.
func (adapter *Adapter) requestScheduleDatesFromPage(siteNo string, movieNo ...string) ([]string, error) {
	if len(movieNo) > 1 {
		return nil, fmt.Errorf("%w: expected at most one schedule movie", ErrIdentityMismatch)
	}
	movie := ""
	if len(movieNo) == 1 {
		movie = movieNo[0]
	}
	path, err := scheduleInventoryPath(siteNo, movie)
	if err != nil {
		return nil, err
	}
	if err := adapter.providerRateLimitError("https://cgv.co.kr" + path); err != nil {
		return nil, err
	}
	var response struct {
		Status int    `json:"status"`
		Body   string `json:"body"`
	}
	expression := fmt.Sprintf(`(async () => {
		if (window.location.origin !== 'https://cgv.co.kr') throw new Error('CGV booking page origin changed');
		const response = await window.fetch(%s, {method:'GET', headers:{accept:'application/json'}, credentials:'same-origin', cache:'no-store', redirect:'error'});
		return {status:response.status, body:await response.text()};
	})()`, jsString(path))
	if err := adapter.evaluate(expression, &response); err != nil {
		return nil, fmt.Errorf("read CGV schedule date inventory: %w", err)
	}
	if response.Status < 200 || response.Status > 299 {
		return nil, adapter.handleProviderFailure(providerHTTPError(response.Status))
	}
	return parseScheduleDatesResponse([]byte(response.Body))
}

func parseScheduleDatesResponse(payload []byte) ([]string, error) {
	if len(payload) == 0 || len(payload) > maxScheduleResponseBytes {
		return nil, errors.New("invalid CGV schedule date inventory size")
	}
	var envelope struct {
		Status json.RawMessage `json:"statusCode"`
		Data   json.RawMessage `json:"data"`
	}
	if err := json.Unmarshal(payload, &envelope); err != nil {
		return nil, fmt.Errorf("decode CGV date inventory: %w", err)
	}
	status, err := requiredInteger(envelope.Status, "statusCode")
	if err != nil {
		return nil, err
	}
	if status != 0 {
		return nil, fmt.Errorf("CGV date inventory status %d", status)
	}
	if len(envelope.Data) == 0 || string(envelope.Data) == "null" {
		return nil, errors.New("CGV date inventory data missing")
	}
	var rows []struct {
		Date string `json:"scnYmd"`
	}
	if err := json.Unmarshal(envelope.Data, &rows); err != nil {
		return nil, fmt.Errorf("decode CGV inventory rows: %w", err)
	}
	dates := make([]string, 0, len(rows))
	seen := make(map[string]bool, len(rows))
	for _, row := range rows {
		date, err := canonicalProviderDate(row.Date)
		if err != nil {
			return nil, err
		}
		if !seen[date] {
			seen[date] = true
			dates = append(dates, date)
		}
	}
	sort.Strings(dates)
	return dates, nil
}
