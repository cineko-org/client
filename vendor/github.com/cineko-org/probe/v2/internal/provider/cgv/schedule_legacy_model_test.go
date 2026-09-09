package cgv

import "time"

// Frozen 2.8.8 planner, retained only for comparable regression measurements.
type scheduleDateRotation struct {
	lastAttempt  map[string]uint64
	sequence     uint64
	lastDetailAt time.Time
}

// A newly exposed date uses the current slot. Known dates still receive detail
// reads, because an unchanged calendar says nothing about additional rounds.
// Even after a long pause this returns at most one date, never a catch-up burst.
func (rotation *scheduleDateRotation) nextDue(dates []string, shard int, now time.Time, interval time.Duration) string {
	present := make(map[string]bool, len(dates))
	unseen := false
	for _, date := range dates {
		present[date] = true
		unseen = unseen || rotation.lastAttempt[date] == 0
	}
	for date := range rotation.lastAttempt {
		if !present[date] {
			delete(rotation.lastAttempt, date)
		}
	}
	if len(dates) == 0 {
		return ""
	}
	age := now.Sub(rotation.lastDetailAt)
	if !unseen && !rotation.lastDetailAt.IsZero() && age >= 0 && age < interval {
		return ""
	}
	rotation.lastDetailAt = now
	return rotation.next(dates, shard)
}

// New dates get the next existing scan slot. Previously attempted dates then
// rotate by age, independent of the provider's changing list indices.
func (rotation *scheduleDateRotation) next(dates []string, initialShard int) string {
	if len(dates) == 0 {
		return ""
	}
	if rotation.lastAttempt == nil {
		rotation.lastAttempt = make(map[string]uint64)
	}
	present := make(map[string]bool, len(dates))
	for _, date := range dates {
		present[date] = true
	}
	for date := range rotation.lastAttempt {
		if !present[date] {
			delete(rotation.lastAttempt, date)
		}
	}
	if initialShard < 0 {
		initialShard = 0
	}
	selected := dates[initialShard%len(dates)]
	for _, date := range dates {
		if rotation.lastAttempt[date] < rotation.lastAttempt[selected] {
			selected = date
		}
	}
	rotation.sequence++
	rotation.lastAttempt[selected] = rotation.sequence
	return selected
}
