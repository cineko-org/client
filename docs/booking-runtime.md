# Client booking runtime

The Client owns the warm booking-browser capability. Launcher and Central do
not create or retain browser processes.

## Capacity and ownership

- An active booking monitor requests two warm slots, including when the supported
  nonmember path has no saved CGV credentials. One monitor covers both opening
  detection and later cancellation-seat availability.
- Three is the hard process cap. With no active booking monitor, demand is zero
  and no browser is started.
- Every slot has one Playwright driver process, one persistent profile, one
  page, and one logical booking task. Slots are acquired exclusively.
- A Central-leased showtime opens one seat page. If preferred seats are not yet
  available, that same page may refresh for at most 45 seconds and 20 requests,
  with a randomized 1.5-2.5 second interval. A second warm slot remains a
  failure standby and never races the same showtime.
- HTTP 403, HTTP 429, a CAPTCHA, a page-identity mismatch, or any refresh error
  stops the same-page loop immediately. Cineko does not solve challenges,
  rotate identity mid-session, or continue sending requests after protection
  is observed.
- The Client records the check and keeps the monitor `running` when preferred
  seats or the showtime is unavailable. Central waits for an exact
  false-to-true availability edge; generic preparation failures use its retry
  policy and Central terminally marks the monitor failed after that policy is
  exhausted.
- Authentication, CAPTCHA, provider access/throttling, and provider-contract
  changes are terminal failures with stable reason codes
  (`authentication_required`, `captcha_required`, `provider_contract_changed`,
  `provider_access_blocked`, `provider_throttled`). Lease loss and
  `client_interrupted` are ambiguous; Central moves the monitor to
  `payment_unknown` until the user verifies CGV history.
- A prepared reservation changes the lease to payment-retained. It is released
  only when payment is completed, abandoned, expires, or the browser fails.

## Lifecycle contract

The pool admits a process only after validating PID, profile, one page, and
readiness. A crash closes the lease immediately, but replacement waits until
the original driver has been waited/reaped. Shutdown and slot rotation use a
bounded graceful close, process-tree kill fallback, and exactly one `Wait`.
Startup retries use bounded exponential backoff. A missing member session does
not suppress nonmember capacity; an execution that requires authentication
surfaces that reason to the user instead of attempting a background login.

## Execution wake-up

The desktop execution worker claims Central work only while a ready slot exists.
Central resource events and local capacity events wake it; the production path
does not use a fixed claim poll timer. Payment release and browser failure also
wake the worker so it can re-evaluate capacity.

## Performance evidence

Before this capability, a booking request opened a fresh Playwright adapter in
the command hot path. After this capability, the hot path acquires an already
ready slot and starts zero new browser processes (`internal/booking` tests
assert no factory call at zero demand and distinct ready process/profile
leases). For two preferred-seat misses followed by success, the previous
Central rearm path required three seat-page opens; same-page refresh requires
one, reducing full seat-page navigation by 66.7%. Real CGV end-to-end latency is
intentionally unmeasured here because this test environment has no authenticated
live CGV booking run.
