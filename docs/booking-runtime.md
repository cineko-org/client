# Client booking runtime

The Client owns the warm booking-browser capability. Launcher and Central do
not create or retain browser processes.

## Capacity and ownership

- An active, authenticated `opening` monitor requests two warm slots.
- Three is the hard process cap. With no active opening monitor or no saved CGV
  credentials, demand is zero and no browser is started.
- Every slot has one Playwright driver process, one persistent profile, one
  page, and one logical booking task. Slots are acquired exclusively.
- A prepared reservation changes the lease to payment-retained. It is released
  only when payment is completed, abandoned, expires, or the browser fails.

## Lifecycle contract

The pool admits a process only after validating PID, profile, one page, and
readiness. A crash closes the lease immediately, but replacement waits until
the original driver has been waited/reaped. Shutdown and slot rotation use a
bounded graceful close, process-tree kill fallback, and exactly one `Wait`.
Startup retries use bounded exponential backoff; permanent credential absence
does not retry until demand is refreshed.

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
leases). Real CGV end-to-end latency is intentionally unmeasured here because
this test environment has no authenticated live CGV booking run.
