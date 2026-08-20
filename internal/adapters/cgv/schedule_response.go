package cgv

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/mxschmitt/playwright-go"
)

const (
	// These are the CGV response paths that carry the provider showtime tuple.
	scheduleResponsePath       = "/api/v1/booking/searchMovScnInfo"
	legacyScheduleResponsePath = "/cnm/atkt/searchMovScnInfo"
	maxScheduleResponseBytes   = 8 << 20
	maximumProviderClockHour   = 47
)

var errScheduleResponseMissing = errors.New("CGV schedule response was not captured")

type capturedProviderResponse struct {
	path   string
	status int
	body   []byte
	err    error
}

type providerScheduleRow struct {
	SiteNo         string
	SiteName       string
	MovieNo        string
	MovieFileNo    string
	MovieTitle     string
	AuditoriumNo   string
	AuditoriumName string
	Date           string
	Sequence       string
	ProductNo      string
	StartClock     string
	EndClock       string
	Available      int
	Capacity       int
}

func (adapter *Adapter) captureProviderResponse(response playwright.Response) {
	if response == nil {
		return
	}
	path, ok := providerResponsePath(response.URL())
	if !ok || (path != scheduleResponsePath && path != legacyScheduleResponsePath) {
		return
	}
	captured := capturedProviderResponse{path: path, status: response.Status()}
	if captured.status < 200 || captured.status > 299 {
		captured.err = providerHTTPError(captured.status)
	} else {
		captured.body, captured.err = response.Body()
		if captured.err == nil && len(captured.body) > maxScheduleResponseBytes {
			captured.err = fmt.Errorf("CGV provider response exceeds %d bytes", maxScheduleResponseBytes)
			captured.body = nil
		}
	}
	adapter.scheduleResponseMu.Lock()
	adapter.providerResponses = append(adapter.providerResponses, captured)
	adapter.scheduleResponseMu.Unlock()
}

func (adapter *Adapter) resetScheduleResponses() {
	adapter.scheduleResponseMu.Lock()
	adapter.providerResponses = nil
	adapter.scheduleResponseMu.Unlock()
}

func (adapter *Adapter) captureScheduleRows() ([]providerScheduleRow, error) {
	adapter.scheduleResponseMu.Lock()
	captures := append([]capturedProviderResponse(nil), adapter.providerResponses...)
	adapter.providerResponses = nil
	adapter.scheduleResponseMu.Unlock()
	if len(captures) == 0 {
		return nil, errScheduleResponseMissing
	}
	var rows []providerScheduleRow
	seen := make(map[string]struct{})
	for _, captured := range captures {
		if captured.path != scheduleResponsePath && captured.path != legacyScheduleResponsePath {
			continue
		}
		if captured.err != nil {
			return nil, captured.err
		}
		parsed, err := parseScheduleResponse(captured.body)
		if err != nil {
			return nil, err
		}
		for _, row := range parsed {
			key := showtimeSourceKey(row.SiteNo, row.Date, row.AuditoriumNo, row.Sequence)
			if _, exists := seen[key]; exists {
				continue
			}
			seen[key] = struct{}{}
			rows = append(rows, row)
		}
	}
	if len(rows) == 0 {
		return nil, errScheduleResponseMissing
	}
	return rows, nil
}

func providerResponsePath(rawURL string) (string, bool) {
	parsed, err := url.Parse(rawURL)
	if err != nil {
		return "", false
	}
	host := strings.ToLower(parsed.Hostname())
	if host != "cgv.co.kr" && host != "www.cgv.co.kr" {
		return "", false
	}
	switch parsed.Path {
	case scheduleResponsePath, legacyScheduleResponsePath:
		return parsed.Path, true
	default:
		return "", false
	}
}

// parseScheduleResponse rejects incomplete provider data before it can become
// a display-derived showtime identity.
func parseScheduleResponse(payload []byte) ([]providerScheduleRow, error) {
	if len(payload) == 0 || len(payload) > maxScheduleResponseBytes {
		return nil, fmt.Errorf("invalid CGV schedule response size %d", len(payload))
	}
	var envelope struct {
		StatusCode    json.RawMessage `json:"statusCode"`
		StatusMessage string          `json:"statusMessage"`
		Data          json.RawMessage `json:"data"`
	}
	if err := json.Unmarshal(payload, &envelope); err != nil {
		return nil, fmt.Errorf("decode CGV schedule response: %w", err)
	}
	statusCode, err := requiredInteger(envelope.StatusCode, "statusCode")
	if err != nil {
		return nil, err
	}
	if statusCode != 0 {
		message := strings.TrimSpace(envelope.StatusMessage)
		if message == "" {
			message = "provider returned a non-zero status"
		}
		return nil, fmt.Errorf("CGV schedule response status %d: %s", statusCode, message)
	}
	if len(envelope.Data) == 0 || bytes.Equal(bytes.TrimSpace(envelope.Data), []byte("null")) {
		return nil, errors.New("CGV schedule response data is missing")
	}
	var rawRows []map[string]json.RawMessage
	if err := json.Unmarshal(envelope.Data, &rawRows); err != nil {
		return nil, fmt.Errorf("decode CGV schedule rows: %w", err)
	}
	rows := make([]providerScheduleRow, 0, len(rawRows))
	for index, rawRow := range rawRows {
		row, err := parseScheduleRow(rawRow)
		if err != nil {
			return nil, fmt.Errorf("CGV schedule row %d: %w", index, err)
		}
		rows = append(rows, row)
	}
	return rows, nil
}

func parseScheduleRow(raw map[string]json.RawMessage) (providerScheduleRow, error) {
	siteNo, err := requiredString(raw, "siteNo")
	if err != nil {
		return providerScheduleRow{}, err
	}
	movieNo, err := requiredString(raw, "movNo")
	if err != nil {
		return providerScheduleRow{}, err
	}
	auditoriumNo, err := requiredString(raw, "scnsNo")
	if err != nil {
		return providerScheduleRow{}, err
	}
	dateRaw, err := requiredString(raw, "scnYmd")
	if err != nil {
		return providerScheduleRow{}, err
	}
	date, err := canonicalProviderDate(dateRaw)
	if err != nil {
		return providerScheduleRow{}, err
	}
	sequence, err := requiredString(raw, "scnSseq")
	if err != nil {
		return providerScheduleRow{}, err
	}
	startClock, err := requiredString(raw, "scnsrtTm")
	if err != nil {
		return providerScheduleRow{}, err
	}
	endClock, err := requiredString(raw, "scnendTm")
	if err != nil {
		return providerScheduleRow{}, err
	}
	if _, _, err := parseProviderClock(startClock); err != nil {
		return providerScheduleRow{}, fmt.Errorf("invalid scnsrtTm: %w", err)
	}
	if _, _, err := parseProviderClock(endClock); err != nil {
		return providerScheduleRow{}, fmt.Errorf("invalid scnendTm: %w", err)
	}
	available, err := optionalInteger(raw, "frSeatCnt")
	if err != nil {
		return providerScheduleRow{}, err
	}
	capacity, err := optionalInteger(raw, "stcnt")
	if err != nil {
		return providerScheduleRow{}, err
	}
	return providerScheduleRow{
		SiteNo: siteNo, SiteName: optionalProviderString(raw, "siteNm"),
		MovieNo: movieNo, MovieFileNo: optionalProviderString(raw, "movfNo"), MovieTitle: optionalProviderString(raw, "movNm"),
		AuditoriumNo: auditoriumNo, AuditoriumName: optionalProviderString(raw, "scnsNm"),
		Date: date, Sequence: sequence, ProductNo: optionalProviderString(raw, "prodNo"),
		StartClock: startClock, EndClock: endClock, Available: available, Capacity: capacity,
	}, nil
}

func canonicalProviderDate(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", errors.New("provider date is empty")
	}
	for _, layout := range []string{"20060102", time.DateOnly} {
		if parsed, err := time.Parse(layout, raw); err == nil {
			return parsed.Format(time.DateOnly), nil
		}
	}
	return "", fmt.Errorf("provider date %q is not YYYYMMDD or YYYY-MM-DD", raw)
}

func parseProviderClock(raw string) (int, int, error) {
	raw = strings.TrimSpace(raw)
	if len(raw) == 5 && raw[2] == ':' {
		raw = raw[:2] + raw[3:]
	}
	if len(raw) != 4 {
		return 0, 0, fmt.Errorf("%q is not HHMM or HH:MM", raw)
	}
	hour, err := strconv.Atoi(raw[:2])
	if err != nil {
		return 0, 0, fmt.Errorf("%q has an invalid hour", raw)
	}
	minute, err := strconv.Atoi(raw[2:])
	if err != nil || minute > 59 {
		return 0, 0, fmt.Errorf("%q has an invalid minute", raw)
	}
	if hour > maximumProviderClockHour {
		return 0, 0, fmt.Errorf("%q exceeds the service-day range", raw)
	}
	return hour, minute, nil
}

func requiredString(raw map[string]json.RawMessage, field string) (string, error) {
	value, ok := raw[field]
	if !ok {
		return "", fmt.Errorf("required provider field %q is missing", field)
	}
	return providerStringValue(value, field)
}

func optionalProviderString(raw map[string]json.RawMessage, field string) string {
	value, ok := raw[field]
	if !ok {
		return ""
	}
	decoded, _ := providerStringValue(value, field)
	return decoded
}

func providerStringValue(raw json.RawMessage, field string) (string, error) {
	if len(raw) == 0 || bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		return "", fmt.Errorf("provider field %q is empty", field)
	}
	var text string
	if json.Unmarshal(raw, &text) == nil {
		text = strings.TrimSpace(text)
		if text == "" {
			return "", fmt.Errorf("provider field %q is empty", field)
		}
		return text, nil
	}
	var number json.Number
	if json.Unmarshal(raw, &number) == nil && strings.TrimSpace(number.String()) != "" {
		return strings.TrimSpace(number.String()), nil
	}
	return "", fmt.Errorf("provider field %q is not a string or number", field)
}

func requiredInteger(raw json.RawMessage, field string) (int, error) {
	if len(raw) == 0 || bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		return 0, fmt.Errorf("required provider field %q is missing", field)
	}
	value, err := providerStringValue(raw, field)
	if err != nil {
		return 0, err
	}
	parsed, err := strconv.Atoi(value)
	if err != nil {
		return 0, fmt.Errorf("provider field %q is not an integer", field)
	}
	return parsed, nil
}

func optionalInteger(raw map[string]json.RawMessage, field string) (int, error) {
	value, ok := raw[field]
	if !ok || bytes.Equal(bytes.TrimSpace(value), []byte("null")) {
		return 0, nil
	}
	return requiredInteger(value, field)
}
