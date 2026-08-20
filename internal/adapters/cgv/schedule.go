package cgv

import (
	"context"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/cineko-org/client/internal/application"
	"github.com/cineko-org/client/internal/domain"
	contracts "github.com/cineko-org/contracts/v3"
)

type scheduleEntry struct {
	Showtime       domain.Showtime
	AuditoriumName string
	ScreenTypes    []string
}

func (adapter *Adapter) ResolveTheater(
	ctx context.Context,
	ref application.TheaterRef,
) (domain.Theater, error) {
	adapter.mu.Lock()
	defer adapter.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return domain.Theater{}, err
	}
	if err := adapter.selectCinemaTheater(ref.Region, ref.Name); err != nil {
		return domain.Theater{}, err
	}
	return domain.Theater{
		Region: ref.Region, Name: ref.Name,
		SourceKey: ref.Region + "/" + ref.Name, ObservedAt: time.Now(),
	}, nil
}

func (adapter *Adapter) DiscoverAuditoriums(
	ctx context.Context,
	theater domain.Theater,
	targetDates []string,
) ([]application.AuditoriumObservation, error) {
	adapter.mu.Lock()
	defer adapter.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if err := adapter.selectCinemaTheater(theater.Region, theater.Name); err != nil {
		return nil, err
	}

	byName := make(map[string]*application.AuditoriumObservation)
	for _, targetDate := range targetDates {
		if err := adapter.selectDate(targetDate); err != nil {
			continue
		}
		entries, err := adapter.extractSchedules(targetDate, theater)
		if err != nil {
			return nil, err
		}
		for _, entry := range entries {
			observation := byName[entry.AuditoriumName]
			if observation == nil {
				observation = &application.AuditoriumObservation{
					Auditorium: domain.Auditorium{
						TheaterID: theater.ID, SourceKey: theater.SourceKey + "/" + entry.AuditoriumName,
						Name:        entry.AuditoriumName,
						ScreenTypes: append([]string(nil), entry.ScreenTypes...),
						Capacity:    entry.Showtime.Capacity, ObservedAt: time.Now(),
					},
				}
				byName[entry.AuditoriumName] = observation
			}
			observation.Auditorium.Capacity = max(observation.Auditorium.Capacity, entry.Showtime.Capacity)
			observation.Auditorium.ScreenTypes = mergeStrings(
				observation.Auditorium.ScreenTypes, entry.ScreenTypes,
			)
			if observation.RepresentativeShowing == nil && !entry.Showtime.SoldOut {
				showtime := entry.Showtime
				observation.RepresentativeShowing = &showtime
			}
		}
	}

	observations := make([]application.AuditoriumObservation, 0, len(byName))
	for _, observation := range byName {
		observations = append(observations, *observation)
	}
	sort.Slice(observations, func(i, j int) bool {
		return observations[i].Auditorium.Name < observations[j].Auditorium.Name
	})
	return observations, nil
}

func (adapter *Adapter) FindShowtimes(
	ctx context.Context,
	query application.ShowtimeQuery,
) ([]domain.Showtime, error) {
	adapter.mu.Lock()
	defer adapter.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if err := adapter.selectCinemaTheater(query.Theater.Region, query.Theater.Name); err != nil {
		return nil, err
	}
	if strings.TrimSpace(query.MovieID) == "" {
		return nil, nil
	}
	var matches []domain.Showtime
	for _, targetDate := range query.TargetDates {
		if err := adapter.selectDate(targetDate); err != nil {
			continue
		}
		entries, err := adapter.extractSchedules(targetDate, query.Theater)
		if err != nil {
			return nil, err
		}
		dateHasAvailableMatch := false
		for _, entry := range entries {
			if entry.Showtime.MovieID == "" || entry.Showtime.MovieID != query.MovieID ||
				!auditoriumMatches(query.Auditorium.Name, entry.AuditoriumName) {
				continue
			}
			if !(domain.ScheduleWindow{
				Weekdays: query.TargetWeekdays,
				Earliest: query.EarliestTime,
				Latest:   query.LatestTime,
			}.MatchesShowtime(entry.Showtime)) {
				continue
			}
			showtime := entry.Showtime
			showtime.AuditoriumID = query.Auditorium.ID
			showtime.AuditoriumName = query.Auditorium.Name
			matches = append(matches, showtime)
			dateHasAvailableMatch = dateHasAvailableMatch || !showtime.SoldOut
		}
		if dateHasAvailableMatch {
			// Target dates are sorted. Once the earliest matching date opens, return
			// immediately so the booking worker can enter visitor and seat selection.
			return matches, nil
		}
	}
	return matches, nil
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
		ID:         contracts.CatalogID(contracts.ProviderCGV, "showtime", showtimeSource),
		ProviderID: contracts.ProviderCGV, SourceKey: showtimeSource,
		MovieID: contracts.CatalogID(contracts.ProviderCGV, "movie", movieSource), Movie: row.MovieTitle,
		TheaterID: theater.ID, TheaterName: theater.Name,
		AuditoriumID:   contracts.CatalogID(contracts.ProviderCGV, "auditorium", auditoriumSource),
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
