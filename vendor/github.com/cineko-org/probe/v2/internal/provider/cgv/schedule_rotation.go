package cgv

import "time"

const scheduleCalendarMaxAge = 5 * time.Minute

func reusableCinemaSelection(selectedRegion, selectedTheater, region, theater string, refreshed, now time.Time) bool {
	if selectedRegion != region || selectedTheater != theater || refreshed.IsZero() {
		return false
	}
	age := now.Sub(refreshed)
	if age < 0 || age >= scheduleCalendarMaxAge {
		return false
	}
	// Date-button labels can change their meaning at midnight. Never map
	// yesterday's cached labels against today's date.
	kst := time.FixedZone("KST", 9*60*60)
	return refreshed.In(kst).Format(time.DateOnly) == now.In(kst).Format(time.DateOnly)
}
