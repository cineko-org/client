package application

import "time"

const (
	defaultClaimedSeatWatchMinInterval = 1500 * time.Millisecond
	defaultClaimedSeatWatchMaxInterval = 2500 * time.Millisecond
)

// ClaimedSeatWatchPolicy controls same-page seat refreshes for one exact local
// showtime. Production keeps the page until showtime; explicit bounded policies
// keep deterministic tests and diagnostics.
type ClaimedSeatWatchPolicy struct {
	UntilShowtime bool
	Window        time.Duration
	RefreshLimit  int
	MinInterval   time.Duration
	MaxInterval   time.Duration
}

func defaultClaimedSeatWatchPolicy() ClaimedSeatWatchPolicy {
	return ClaimedSeatWatchPolicy{
		UntilShowtime: true,
		MinInterval:   defaultClaimedSeatWatchMinInterval,
		MaxInterval:   defaultClaimedSeatWatchMaxInterval,
	}
}

func (policy ClaimedSeatWatchPolicy) normalized() ClaimedSeatWatchPolicy {
	if policy == (ClaimedSeatWatchPolicy{}) {
		return defaultClaimedSeatWatchPolicy()
	}
	if policy.MinInterval <= 0 || (!policy.UntilShowtime && (policy.Window <= 0 || policy.RefreshLimit <= 0)) {
		return ClaimedSeatWatchPolicy{}
	}
	if policy.UntilShowtime {
		policy.Window = 0
		policy.RefreshLimit = 0
	}
	if policy.MaxInterval < policy.MinInterval {
		policy.MaxInterval = policy.MinInterval
	}
	return policy
}
