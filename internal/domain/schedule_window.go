package domain

import (
	"fmt"
	"time"
)

const scheduleTimeLayout = "15:04"

// KoreaLocation is the timezone used for CGV dates and schedule clocks.
var KoreaLocation = time.FixedZone("Asia/Seoul", 9*60*60)

// ScheduleWindow describes the date and local-time portion of a monitor.
// A window with no clock bounds accepts every time; when the end precedes the
// start it spans midnight, for example 21:00–06:00.
type ScheduleWindow struct {
	Weekdays []int
	Earliest string
	Latest   string
}

// Validate checks the user-facing schedule window without applying it.
func (window ScheduleWindow) Validate() error {
	for name, value := range map[string]string{
		"earliest time": window.Earliest,
		"latest time":   window.Latest,
	} {
		if value == "" {
			continue
		}
		if _, err := time.Parse(scheduleTimeLayout, value); err != nil {
			return fmt.Errorf("invalid %s %q: %w", name, value, err)
		}
	}
	if window.Earliest != "" && window.Latest != "" && window.Earliest == window.Latest {
		return fmt.Errorf("earliest time and latest time must differ")
	}
	return nil
}

// Matches reports whether a schedule-date clock belongs to this window. The
// date and clock are interpreted in Korea time; the end bound is exclusive.
func (window ScheduleWindow) Matches(date, startsAt string) bool {
	start, err := parseScheduleStart(date, startsAt)
	if err != nil {
		return false
	}
	return window.matches(start)
}

// MatchesShowtime applies the same window to the civil start date preserved by
// the provider handoff. This keeps a service-day 25:30 show on its Saturday
// civil start even though its provider schedule date is Friday.
func (window ScheduleWindow) MatchesShowtime(showtime Showtime) bool {
	civilDate := showtime.CivilDate
	if civilDate == "" {
		civilDate = showtime.Date
	}
	start, err := parseScheduleStart(civilDate, showtime.StartsAt)
	if err != nil {
		return false
	}
	return window.matches(start)
}

func (window ScheduleWindow) matches(start time.Time) bool {
	clockMinutes := start.Hour()*60 + start.Minute()
	if len(window.Weekdays) > 0 && !weekdayMatches(window.Weekdays, int(start.Weekday())) {
		return false
	}
	if window.Earliest == "" && window.Latest == "" {
		return true
	}
	if window.Earliest == "" {
		latest, parseErr := parseClockMinutes(window.Latest)
		return parseErr == nil && clockMinutes < latest
	}
	if window.Latest == "" {
		earliest, parseErr := parseClockMinutes(window.Earliest)
		return parseErr == nil && clockMinutes >= earliest
	}
	earliest, earliestErr := parseClockMinutes(window.Earliest)
	latest, latestErr := parseClockMinutes(window.Latest)
	if earliestErr != nil || latestErr != nil || earliest == latest {
		return false
	}
	if earliest < latest {
		return clockMinutes >= earliest && clockMinutes < latest
	}
	return clockMinutes >= earliest || clockMinutes < latest
}

func parseClockMinutes(value string) (int, error) {
	hour, minute, err := parseExtendedClock(value)
	if err != nil {
		return 0, err
	}
	return (hour%24)*60 + minute, nil
}

func parseScheduleStart(date, startsAt string) (time.Time, error) {
	parsedDate, err := time.ParseInLocation(time.DateOnly, date, KoreaLocation)
	if err != nil {
		return time.Time{}, err
	}
	hour, minute, err := parseExtendedClock(startsAt)
	if err != nil {
		return time.Time{}, err
	}
	return parsedDate.Add(time.Duration(hour*60+minute) * time.Minute), nil
}

func parseExtendedClock(value string) (int, int, error) {
	parsed, err := time.Parse(scheduleTimeLayout, value)
	if err == nil {
		return parsed.Hour(), parsed.Minute(), nil
	}
	if len(value) != 5 || value[2] != ':' {
		return 0, 0, err
	}
	var hour, minute int
	if _, scanErr := fmt.Sscanf(value, "%d:%d", &hour, &minute); scanErr != nil || hour > 47 || minute > 59 {
		return 0, 0, fmt.Errorf("invalid schedule clock %q", value)
	}
	return hour, minute, nil
}

func weekdayMatches(weekdays []int, weekday int) bool {
	for _, candidate := range weekdays {
		if candidate == weekday {
			return true
		}
	}
	return false
}
