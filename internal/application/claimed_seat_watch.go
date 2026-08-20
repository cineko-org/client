package application

import "time"

const (
	defaultClaimedSeatWatchWindow       = 45 * time.Second
	defaultClaimedSeatWatchRefreshLimit = 20
	defaultClaimedSeatWatchMinInterval  = 1500 * time.Millisecond
	defaultClaimedSeatWatchMaxInterval  = 2500 * time.Millisecond
)

// ClaimedSeatWatchPolicy bounds same-page seat refreshes after Central has
// leased one exact showtime to this Client. A single active browser performs
// the refreshes; any additional warm browser remains a failure standby.
type ClaimedSeatWatchPolicy struct {
	Window       time.Duration
	RefreshLimit int
	MinInterval  time.Duration
	MaxInterval  time.Duration
}

func defaultClaimedSeatWatchPolicy() ClaimedSeatWatchPolicy {
	return ClaimedSeatWatchPolicy{
		Window:       defaultClaimedSeatWatchWindow,
		RefreshLimit: defaultClaimedSeatWatchRefreshLimit,
		MinInterval:  defaultClaimedSeatWatchMinInterval,
		MaxInterval:  defaultClaimedSeatWatchMaxInterval,
	}
}

func (policy ClaimedSeatWatchPolicy) normalized() ClaimedSeatWatchPolicy {
	if policy == (ClaimedSeatWatchPolicy{}) {
		return defaultClaimedSeatWatchPolicy()
	}
	if policy.Window <= 0 || policy.RefreshLimit <= 0 || policy.MinInterval <= 0 {
		return ClaimedSeatWatchPolicy{}
	}
	if policy.MaxInterval < policy.MinInterval {
		policy.MaxInterval = policy.MinInterval
	}
	return policy
}
