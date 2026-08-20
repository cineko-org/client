package cgv

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/cineko-org/client/internal/application"
	"github.com/cineko-org/client/internal/domain"
)

var schedulePattern = regexp.MustCompile(
	`^(\d{2,}:\d{2})\s*-\s*(\d{2,}:\d{2})\s*(?:(\d+)\s*/\s*(\d+)\s*석|(매진|예매종료))(?:\s*(.*))?$`,
)

type rawSchedule struct {
	Label      string `json:"label"`
	MovieID    string `json:"movieId"`
	Movie      string `json:"movie"`
	PosterURL  string `json:"posterUrl"`
	Group      string `json:"group"`
	Auditorium string `json:"auditorium"`
	Disabled   bool   `json:"disabled"`
}

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
			if !movieMatches(query.MovieID, query.Movie, entry.Showtime.MovieID, entry.Showtime.Movie) ||
				!auditoriumMatches(query.Auditorium.Name, entry.AuditoriumName) {
				continue
			}
			if !domain.TimeWindowContains(entry.Showtime.StartsAt, query.EarliestTime, query.LatestTime) {
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
	location := time.FixedZone("KST", 9*60*60)
	parsed, err := time.ParseInLocation("2006-01-02", isoDate, location)
	if err != nil {
		return err
	}
	weekdays := []string{"일", "월", "화", "수", "목", "금", "토"}
	markers := []string{weekdays[parsed.Weekday()]}
	if time.Now().In(location).Format("2006-01-02") == isoDate {
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
	const expression = `(() => {
		const normalize = value => (value || '').replace(/\s+/g, ' ').trim();
		const previous = (element, selector) => {
			let current = element;
			while (current) {
				let sibling = current.previousElementSibling;
				while (sibling) {
					if (sibling.matches && sibling.matches(selector)) return sibling;
					const nested = sibling.querySelectorAll ? window.__cinekoQueryAll(selector, sibling) : [];
					if (nested.length) return nested[nested.length - 1];
					sibling = sibling.previousElementSibling;
				}
				current = current.parentElement;
			}
			return null;
		};
		return window.__cinekoQueryAll('button').map(button => {
			const label = normalize(button.innerText || button.textContent);
			if (!/^\d{2,}:\d{2}-/.test(label)) return null;
			const group = previous(button, 'h3');
			const movieHeading = previous(button, 'h2');
			const movieIdentity = movieHeading && (movieHeading.closest('[data-mov-no], [data-movie-id]') || movieHeading);
			const poster = movieHeading && window.__cinekoQuery('img[alt*="포스터"]', movieHeading);
			const auditorium = window.__cinekoQuery('[class*="_theater__"]', button);
			return {
				label,
				movieId: normalize((movieIdentity && (movieIdentity.getAttribute('data-mov-no') || movieIdentity.getAttribute('data-movie-id'))) || (movieHeading && movieHeading.getAttribute('data-mov-no'))),
				movie: poster ? normalize(poster.getAttribute('alt')).replace(/\s*포스터$/, '') : normalize(movieHeading && movieHeading.innerText),
				posterUrl: poster ? normalize(poster.currentSrc || poster.getAttribute('src')) : '',
				group: normalize(group && group.innerText),
				auditorium: normalize(auditorium && auditorium.innerText),
				disabled: !!button.disabled
			};
		}).filter(Boolean);
	})()`
	var raw []rawSchedule
	if err := adapter.evaluate(expression, &raw); err != nil {
		return nil, fmt.Errorf("extract CGV schedules: %w", err)
	}
	entries := make([]scheduleEntry, 0, len(raw))
	for _, item := range raw {
		entry, ok := parseSchedule(item, date, theater)
		if ok {
			entries = append(entries, entry)
		}
	}
	return entries, nil
}

func parseSchedule(item rawSchedule, date string, theater domain.Theater) (scheduleEntry, bool) {
	match := schedulePattern.FindStringSubmatch(normalize(item.Label))
	if match == nil {
		return scheduleEntry{}, false
	}
	showDate, startsAt, ok := normalizeCGVShowtime(date, match[1])
	if !ok {
		return scheduleEntry{}, false
	}
	_, endsAt, ok := normalizeCGVShowtime(date, match[2])
	if !ok {
		return scheduleEntry{}, false
	}
	available, capacity := 0, 0
	if match[3] != "" {
		_, _ = fmt.Sscanf(match[3], "%d", &available)
		_, _ = fmt.Sscanf(match[4], "%d", &capacity)
	}
	auditoriumName, screenTypes := parseAuditorium(item.Group, item.Auditorium)
	if auditoriumName == "" {
		return scheduleEntry{}, false
	}
	identityTheater := theater.ID
	if identityTheater == "" {
		identityTheater = theater.Name
	}
	identityMovie := item.Movie
	if item.MovieID != "" {
		identityMovie = item.MovieID
	}
	showtime := domain.Showtime{
		ID:      stableSourceID(identityTheater, showDate, identityMovie, auditoriumName, startsAt),
		MovieID: item.MovieID,
		Movie:   item.Movie, PosterURL: item.PosterURL, TheaterID: theater.ID, TheaterName: theater.Name,
		AuditoriumName: auditoriumName, ScreenTypes: screenTypes,
		Date: showDate, StartsAt: startsAt, EndsAt: endsAt,
		AvailableSeats: available, Capacity: capacity,
		SoldOut: item.Disabled || match[5] != "", ObservedAt: time.Now(),
		SourceLabel: normalize(item.Label),
	}
	return scheduleEntry{Showtime: showtime, AuditoriumName: auditoriumName, ScreenTypes: screenTypes}, true
}

// normalizeCGVShowtime converts CGV's post-midnight clock notation (for
// example, Friday's 25:00) into the actual KST calendar date and a normal
// 24-hour clock. Matching and weekday filtering must use the resulting date.
func normalizeCGVShowtime(date, clock string) (string, string, bool) {
	parts := strings.Split(clock, ":")
	if len(parts) != 2 {
		return "", "", false
	}
	hour, errHour := strconv.Atoi(parts[0])
	minute, errMinute := strconv.Atoi(parts[1])
	if errHour != nil || errMinute != nil || hour < 0 || hour > 47 || minute < 0 || minute > 59 {
		return "", "", false
	}
	base, err := time.Parse("2006-01-02", date)
	if err != nil {
		return "", "", false
	}
	actualDate := base.AddDate(0, 0, hour/24)
	return actualDate.Format("2006-01-02"), fmt.Sprintf("%02d:%02d", hour%24, minute), true
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

func movieMatches(requestedID, requestedTitle, observedID, observedTitle string) bool {
	if requestedID != "" {
		return observedID != "" && requestedID == observedID
	}
	requested, observed := requestedTitle, observedTitle
	requested = strings.ToLower(normalize(requested))
	observed = strings.ToLower(normalize(observed))
	return requested == observed || strings.Contains(observed, requested)
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

func stableSourceID(parts ...string) string {
	hash := sha256.Sum256([]byte(strings.Join(parts, "\x00")))
	return hex.EncodeToString(hash[:12])
}
