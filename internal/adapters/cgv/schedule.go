package cgv

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/cineko-org/client/internal/domain"
	catalogpb "github.com/cineko-org/contracts/gen/go/cineko/catalog"
	commonpb "github.com/cineko-org/contracts/gen/go/cineko/common"
	"google.golang.org/protobuf/proto"
)

type scheduleEntry struct {
	Showtime       domain.Showtime
	AuditoriumName string
	ScreenTypes    []string
}

func (adapter *Adapter) ResolveTheater(
	ctx context.Context,
	ref *catalogpb.Theater,
) (*catalogpb.Theater, error) {
	adapter.mu.Lock()
	defer adapter.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if ref == nil {
		return nil, errors.New("theater is required")
	}
	if err := adapter.selectCinemaTheater(ref.GetRegion(), ref.GetName()); err != nil {
		return nil, err
	}
	return proto.CloneOf(ref), nil
}

func (adapter *Adapter) DiscoverAuditoriums(
	ctx context.Context,
	theater *catalogpb.Theater,
	targetDates []string,
) ([]*catalogpb.Auditorium, error) {
	adapter.mu.Lock()
	defer adapter.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if theater == nil {
		return nil, errors.New("theater is required")
	}
	if err := adapter.selectCinemaTheater(theater.GetRegion(), theater.GetName()); err != nil {
		return nil, err
	}

	byName := make(map[string]*catalogpb.Auditorium)
	for _, targetDate := range targetDates {
		if err := adapter.selectDate(targetDate); err != nil {
			continue
		}
		entries, err := adapter.extractSchedules(targetDate, theaterDomainFromProto(theater))
		if err != nil {
			return nil, err
		}
		for _, entry := range entries {
			observation := byName[entry.AuditoriumName]
			if observation == nil {
				id := entry.Showtime.AuditoriumID
				theaterID := theater.GetId()
				sourceKey := entry.Showtime.SourceKey
				name := entry.AuditoriumName
				capacity := boundedInt32(entry.Showtime.Capacity)
				observation = catalogpb.Auditorium_builder{
					Id: &id, TheaterId: &theaterID, SourceKey: &sourceKey, Name: &name,
					ScreenTypes: append([]string(nil), entry.ScreenTypes...), Capacity: &capacity,
				}.Build()
				byName[entry.AuditoriumName] = observation
			}
			if capacity := boundedInt32(entry.Showtime.Capacity); capacity > observation.GetCapacity() {
				observation.SetCapacity(capacity)
			}
			observation.SetScreenTypes(mergeStrings(observation.GetScreenTypes(), entry.ScreenTypes))
		}
	}

	observations := make([]*catalogpb.Auditorium, 0, len(byName))
	for _, observation := range byName {
		observations = append(observations, observation)
	}
	sort.Slice(observations, func(i, j int) bool {
		return observations[i].GetName() < observations[j].GetName()
	})
	return observations, nil
}

func (adapter *Adapter) FindShowtimes(
	ctx context.Context,
	theater *catalogpb.Theater,
	auditorium *catalogpb.Auditorium,
	movieID string,
	targetDates []string,
	targetWeekdays []int32,
	earliestTime *commonpb.LocalTime,
	latestTime *commonpb.LocalTime,
) ([]*catalogpb.Showtime, error) {
	adapter.mu.Lock()
	defer adapter.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if theater == nil || auditorium == nil {
		return nil, errors.New("theater and auditorium are required")
	}
	if err := adapter.selectCinemaTheater(theater.GetRegion(), theater.GetName()); err != nil {
		return nil, err
	}
	if strings.TrimSpace(movieID) == "" {
		return nil, nil
	}
	var matches []*catalogpb.Showtime
	domainTheater := theaterDomainFromProto(theater)
	for _, targetDate := range targetDates {
		if err := adapter.selectDate(targetDate); err != nil {
			continue
		}
		entries, err := adapter.extractSchedules(targetDate, domainTheater)
		if err != nil {
			return nil, err
		}
		window := domain.ScheduleWindow{
			Weekdays: int32ValuesToInt(targetWeekdays),
			Earliest: localTimeToString(earliestTime),
			Latest:   localTimeToString(latestTime),
		}
		dateMatches, dateHasAvailableMatch := matchingShowtimes(entries, auditorium, movieID, window)
		matches = append(matches, dateMatches...)
		if dateHasAvailableMatch {
			// Target dates are sorted. Once the earliest matching date opens, return
			// immediately so the booking worker can enter visitor and seat selection.
			return matches, nil
		}
	}
	return matches, nil
}

func matchingShowtimes(
	entries []scheduleEntry,
	auditorium *catalogpb.Auditorium,
	movieID string,
	window domain.ScheduleWindow,
) ([]*catalogpb.Showtime, bool) {
	matches := make([]*catalogpb.Showtime, 0)
	hasAvailable := false
	for _, entry := range entries {
		if !scheduleEntryMatches(entry, auditorium.GetName(), movieID, window) {
			continue
		}
		showtime := entry.Showtime
		showtime.AuditoriumID = auditorium.GetId()
		showtime.AuditoriumName = auditorium.GetName()
		matches = append(matches, showtimeProtoFromDomain(showtime))
		hasAvailable = hasAvailable || !showtime.SoldOut
	}
	return matches, hasAvailable
}

func scheduleEntryMatches(entry scheduleEntry, auditoriumName, movieID string, window domain.ScheduleWindow) bool {
	return entry.Showtime.MovieID != "" &&
		entry.Showtime.MovieID == movieID &&
		auditoriumMatches(auditoriumName, entry.AuditoriumName) &&
		window.MatchesShowtime(entry.Showtime)
}

// CaptureSchedules returns a complete, unfiltered snapshot for every requested
// date. Date-level failures stay attached to that date so an unavailable page
// can never be mistaken for evidence that no showtimes existed.
func (adapter *Adapter) CaptureSchedules(
	ctx context.Context,
	theater domain.Theater,
	targetDates []string,
) ([]domain.ScheduleCapture, error) {
	adapter.mu.Lock()
	defer adapter.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if err := adapter.selectCinemaTheater(theater.Region, theater.Name); err != nil {
		return nil, err
	}
	result := make([]domain.ScheduleCapture, 0, len(targetDates))
	for _, targetDate := range targetDates {
		capture := domain.ScheduleCapture{TargetDate: targetDate}
		if err := adapter.selectDate(targetDate); err != nil {
			capture.Error = err.Error()
			result = append(result, capture)
			continue
		}
		entries, err := adapter.extractSchedules(targetDate, theater)
		if err != nil {
			capture.Error = err.Error()
			result = append(result, capture)
			continue
		}
		capture.Complete = true
		capture.Showtimes = make([]domain.Showtime, 0, len(entries))
		for _, entry := range entries {
			capture.Showtimes = append(capture.Showtimes, entry.Showtime)
		}
		sort.Slice(capture.Showtimes, func(i, j int) bool {
			return domain.ShowtimeOccurrenceKey(capture.Showtimes[i]) < domain.ShowtimeOccurrenceKey(capture.Showtimes[j])
		})
		result = append(result, capture)
	}
	return result, nil
}

func (adapter *Adapter) selectCinemaTheater(region, theater string) error {
	if err := adapter.navigate(bookingCinemaURL); err != nil {
		return fmt.Errorf("open CGV cinema booking: %w", err)
	}
	clicked, err := adapter.clickButtonPrefix(region + "(")
	if err != nil {
		return err
	}
	if !clicked {
		opened, openErr := adapter.clickButtonExact("자주가는 CGV 목록 수정")
		if openErr != nil {
			return openErr
		}
		if opened {
			if err := adapter.wait(200 * time.Millisecond); err != nil {
				return err
			}
			clicked, err = adapter.clickButtonPrefix(region + "(")
			if err != nil {
				return err
			}
		}
	}
	if !clicked {
		return fmt.Errorf("%w: region button %q not found", ErrUIContractChanged, region)
	}
	if err := adapter.wait(150 * time.Millisecond); err != nil {
		return err
	}
	clicked, err = adapter.clickButtonExact(theater)
	if err != nil {
		return err
	}
	if !clicked {
		return fmt.Errorf("%w: theater button %q not found", ErrUIContractChanged, theater)
	}
	if err := adapter.wait(200 * time.Millisecond); err != nil {
		return err
	}
	if exists, _ := adapter.buttonExists("극장선택"); exists {
		_, _ = adapter.clickButtonExact("극장선택")
	}
	if err := adapter.wait(800 * time.Millisecond); err != nil {
		return err
	}
	adapter.selectedTheater = theater
	adapter.selectedRegion = region
	return nil
}

func (adapter *Adapter) selectDate(isoDate string) error {
	parsed, err := time.ParseInLocation(time.DateOnly, isoDate, domain.KoreaLocation)
	if err != nil {
		return err
	}
	weekdays := []string{"일", "월", "화", "수", "목", "금", "토"}
	markers := []string{weekdays[parsed.Weekday()]}
	if time.Now().In(domain.KoreaLocation).Format(time.DateOnly) == isoDate {
		markers = append([]string{"오늘"}, markers...)
	}
	var labels []string
	if err := adapter.evaluate(`(() => window.__cinekoQueryAll('button')
		.filter(button => !button.disabled)
		.map(button => (button.innerText || button.textContent || '').replace(/\s+/g, ' ').trim())
		.filter(Boolean))()`, &labels); err != nil {
		return fmt.Errorf("read CGV date buttons: %w", err)
	}
	for _, label := range labels {
		if !dateButtonMatches(label, parsed.Day(), markers) {
			continue
		}
		adapter.resetScheduleResponses()
		clicked, clickErr := adapter.clickButtonExact(normalize(label))
		if clickErr != nil {
			return clickErr
		}
		if clicked {
			return adapter.wait(500 * time.Millisecond)
		}
	}
	return fmt.Errorf("%w: target date %s is not selectable", ErrUIContractChanged, isoDate)
}

func dateButtonMatches(label string, day int, markers []string) bool {
	compact := strings.Map(func(character rune) rune {
		if character >= '0' && character <= '9' || character >= '가' && character <= '힣' {
			return character
		}
		return -1
	}, label)
	digits := strings.Map(func(character rune) rune {
		if character >= '0' && character <= '9' {
			return character
		}
		return -1
	}, compact)
	observedDay, err := strconv.Atoi(digits)
	if err != nil || observedDay != day {
		return false
	}
	text := strings.Map(func(character rune) rune {
		if character >= '0' && character <= '9' {
			return -1
		}
		return character
	}, compact)
	matchedMarker := false
	for _, marker := range markers {
		if strings.Contains(text, marker) {
			matchedMarker = true
			text = strings.ReplaceAll(text, marker, "")
		}
	}
	return matchedMarker && text == ""
}

func (adapter *Adapter) extractSchedules(
	date string,
	theater domain.Theater,
) ([]scheduleEntry, error) {
	raw, err := adapter.captureScheduleRows()
	if err != nil {
		return nil, err
	}
	entries := make([]scheduleEntry, 0, len(raw))
	for _, row := range raw {
		if row.Date != date {
			continue
		}
		entry, err := scheduleEntryFromProviderRow(row, theater)
		if err != nil {
			return nil, err
		}
		entries = append(entries, entry)
	}
	return entries, nil
}

func scheduleEntryFromProviderRow(row providerScheduleRow, theater domain.Theater) (scheduleEntry, error) {
	auditoriumName, screenTypes := parseAuditorium("", row.AuditoriumName)
	if auditoriumName == "" {
		return scheduleEntry{}, fmt.Errorf("CGV schedule row %q has no auditorium display name", row.Sequence)
	}
	showtimeSource := showtimeSourceKey(row.SiteNo, row.Date, row.AuditoriumNo, row.Sequence)
	movieSource := strings.TrimSpace(row.MovieNo)
	auditoriumSource := auditoriumSourceKey(row.SiteNo, row.AuditoriumNo)
	startClock := providerClockDisplay(row.StartClock)
	endClock := providerClockDisplay(row.EndClock)
	civilDate := providerCivilDate(row.Date, row.StartClock)
	if startClock == "" || endClock == "" || civilDate == "" {
		return scheduleEntry{}, fmt.Errorf("CGV schedule row %q has invalid clock range", row.Sequence)
	}
	showtime := domain.Showtime{
		ID:         catalogID(providerCGV, "showtime", showtimeSource),
		ProviderID: providerCGV, SourceKey: showtimeSource,
		MovieID: catalogID(providerCGV, "movie", movieSource), Movie: row.MovieTitle,
		TheaterID: theater.ID, TheaterName: theater.Name,
		AuditoriumID:   catalogID(providerCGV, "auditorium", auditoriumSource),
		AuditoriumName: auditoriumName, ScreenTypes: screenTypes,
		Date: row.Date, CivilDate: civilDate, StartsAt: startClock, EndsAt: endClock,
		AvailableSeats: row.Available, Capacity: row.Capacity,
		SoldOut: row.Available == 0, ObservedAt: time.Now(),
		SourceLabel: strings.Join([]string{startClock, endClock, row.MovieTitle, auditoriumName}, " "),
	}
	return scheduleEntry{Showtime: showtime, AuditoriumName: auditoriumName, ScreenTypes: screenTypes}, nil
}

func parseAuditorium(group, structuredName string) (string, []string) {
	group = normalize(group)
	structuredName = normalize(structuredName)
	types := detectScreenTypes(group + " " + structuredName)
	if structuredName != "" {
		return structuredName, types
	}
	if group == "" || group == "2D" || group == "3D" {
		return "", types
	}
	name := group
	for _, token := range []string{
		"SCREENX DOLBY ATMOS mix 2D", "IMAX LASER 2D", "ULTRA 4DX 2D",
		"SCREENX 2D", "4DX 2D", "DOLBY ATMOS 2D", "2D", "3D",
	} {
		name = strings.TrimSpace(strings.TrimSuffix(name, token))
	}
	return name, types
}

func detectScreenTypes(value string) []string {
	upper := strings.ToUpper(value)
	var types []string
	for _, screenType := range []string{
		"ULTRA 4DX", "SCREENX", "IMAX", "4DX", "DOLBY ATMOS", "PREMIUM",
		"CINE DE CHEF", "CGV아트하우스", "LASER", "2D", "3D",
	} {
		if strings.Contains(upper, strings.ToUpper(screenType)) {
			types = append(types, screenType)
		}
	}
	return types
}

func auditoriumMatches(requested, observed string) bool {
	requested = strings.ToLower(strings.ReplaceAll(normalize(requested), " ", ""))
	observed = strings.ToLower(strings.ReplaceAll(normalize(observed), " ", ""))
	return requested == observed || strings.HasPrefix(observed, requested) || strings.HasPrefix(requested, observed)
}

func normalize(value string) string { return strings.Join(strings.Fields(value), " ") }

func mergeStrings(left, right []string) []string {
	seen := make(map[string]struct{}, len(left)+len(right))
	merged := make([]string, 0, len(left)+len(right))
	for _, values := range [][]string{left, right} {
		for _, value := range values {
			if _, exists := seen[value]; exists {
				continue
			}
			seen[value] = struct{}{}
			merged = append(merged, value)
		}
	}
	return merged
}

func providerClockDisplay(raw string) string {
	hour, minute, err := parseProviderClock(raw)
	if err != nil {
		return ""
	}
	return fmt.Sprintf("%02d:%02d", hour%24, minute)
}

func providerCivilDate(date, rawClock string) string {
	hour, minute, err := parseProviderClock(rawClock)
	if err != nil {
		return ""
	}
	base, err := time.ParseInLocation(time.DateOnly, strings.TrimSpace(date), domain.KoreaLocation)
	if err != nil {
		return ""
	}
	return base.Add(time.Duration(hour*60+minute) * time.Minute).Format(time.DateOnly)
}

func theaterSourceKey(siteNo string) string { return strings.TrimSpace(siteNo) }

func auditoriumSourceKey(siteNo, auditoriumNo string) string {
	return strings.Join([]string{theaterSourceKey(siteNo), strings.TrimSpace(auditoriumNo)}, "/")
}

func showtimeSourceKey(siteNo, date, auditoriumNo, sequence string) string {
	return strings.Join([]string{
		theaterSourceKey(siteNo), strings.TrimSpace(date), strings.TrimSpace(auditoriumNo), strings.TrimSpace(sequence),
	}, "/")
}
