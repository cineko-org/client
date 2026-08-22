# Client behavior contract

This inventory records the externally observable Client contract. It omits hosts, credentials, deployment topology, browser evasion details, and other implementation-only data.

## Trust, version, and ownership

- Every Cineko-owned HTTP, desktop bridge, stream, release, application-service port, event, and persistence boundary uses the latest generated Proto message directly. Custom DTO structs, renamed copies, type aliases, hand-built owned JSON, schema/protocol version fields, and reserved Proto fields are forbidden. UI-only props and local form state may remain local but cannot cross a service boundary. Domain-only algorithm values may not duplicate generated resources or appear in application ports. External provider and operating-system adapters remain isolated from this rule.
- Production startup accepts a single-use Launcher ticket through `POST /v1/client-sessions`. The returned user, installation, device, release generation, Client artifact, browser revision/artifact, and Playwright version/artifact must exactly match the generated Client launch envelope.
- After Wails startup has installed the local application supervisors, Client atomically publishes the Launcher-provided startup nonce to an owner-only marker under the shared data directory. Invalid, stale, or non-private markers never certify startup.
- Development credential exchange uses `POST /v1/auth/exchange`. Authenticated calls use bearer access tokens and refresh once through `POST /v1/auth/refresh` before retrying one rejected call.
- Every Central response, including `GET /v1/events/stream`, must carry the launch release generation. A different generation closes the update signal; Launcher exit code 75 requests an exact runtime replacement.
- All user resources are scoped by the authenticated Central namespace. A conflicting embedded `userId` is rejected as absent.
- Revisioned mutations use `If-None-Match: *` to create or `If-Match` to update/delete, plus a deterministic idempotency key. `revision_conflict` and `idempotency_conflict` map to one conflict result.
- Recognized Central errors are `not_found`, `revision_conflict`, `idempotency_conflict`, `unauthorized`, and `rate_limited`. Other error codes remain failures and are not silently converted.

## Central service points

| Domain | Service points | Preconditions and mutation | Retry/terminal behavior |
| --- | --- | --- | --- |
| Session | `POST /v1/client-sessions`, `POST /v1/auth/exchange`, `POST /v1/auth/refresh`, `POST /v1/auth/logout` | Exact launch identity or explicit development credential; logout revokes the active session | One refresh and one original-call retry; invalid launch context is terminal |
| Device | `PUT /v1/devices/{installationId}` | Authenticated installation identity; upserts current platform/version | Conflict/error is surfaced; release-generation change requests update |
| Settings | `GET /v1/settings`, `PUT /v1/settings` | User-owned settings; create/update is revision fenced and idempotent | Conflict leaves previous live settings intact |
| Embedded probe | `POST /v1/probe-bootstrap-tickets` | Authenticated installation/device and current release identity | Ticket failure prevents probe startup |
| Execution | `POST /v1/executions:claim`, `PUT /v1/executions/{executionId}/heartbeat`, `PUT /v1/executions/{executionId}/result` | Client claims once at startup and thereafter only on durable `execution.ready`, stream reconnect, or local payment-handoff release; lease token fences heartbeat/result | No-work is an event wait, not polling; retryable claim transport/server failures use bounded backoff or an earlier reconnect wake; authentication/contract failures terminate the supervisor; heartbeat loss cancels browser work and completes failed |
| Catalog | `GET /v1/catalog`, `POST /v1/catalog/auditoriums/{auditoriumId}/seat-map:resolve`, `GET /v1/catalog/auditoriums/{auditoriumId}/seat-map:watch` | Client requests or watches a layout by auditorium ID only. Central immediately streams the cached layout and current generated collection state, then emits only durable changes. Client has no catalog or seat-layout write service point. | Missing layout never starts a Client-side scan or polling loop. The Client does not select dates, Probe instances, provider pages, or refresh policy. |
| Presets | `POST /v1/presets`, `GET /v1/presets`, `PUT /v1/presets/{id}`, `GET /v1/presets/{id}`, `DELETE /v1/presets/{id}` | Owner/revision checks apply | Delete is idempotent when already absent; conflicts require reload |
| Monitors | `POST /v1/monitors`, `GET /v1/monitors`, `PUT /v1/monitors/{id}`, `GET /v1/monitors/{id}`, `DELETE /v1/monitors/{id}` | Owner/revision checks apply | Delete is idempotent when already absent; conflicts require reload |
| Reservations | `POST /v1/reservations`, `GET /v1/reservations`, `PUT /v1/reservations/{id}`, `GET /v1/reservations/{id}`, `DELETE /v1/reservations/{id}` | Owner/revision checks apply | Delete is idempotent when already absent; conflicts require reload |
| External operations | `POST /v1/external-operations`, `GET /v1/external-operations`, `PUT /v1/external-operations/{id}`, `GET /v1/external-operations/{id}`, `DELETE /v1/external-operations/{id}` | Owner/revision checks apply | Delete is idempotent when already absent; conflicts require reload |
| App events | `POST /v1/app-events`, `GET /v1/app-events`, `PUT /v1/app-events/{id}`, `GET /v1/app-events/{id}`, `DELETE /v1/app-events/{id}` | Owner/revision checks apply | Delete is idempotent when already absent; conflicts require reload |
| Change stream | `GET /v1/events/stream` | Bearer session, Last-Event-ID cursor, generated Proto control events | Transport and 5xx reconnect with jittered backoff; contract/4xx failure is surfaced and terminates Client instead of running stale |

## Domain state machines

### Monitor and booking

- `pending` is eligible. Central execution claims move it to `running`; user retry rearms the same durable monitor in `pending`.
- One monitor covers opening detection and later cancellation-seat availability without creating duplicate theater scans. Transient execution failures retry immediately within the lease budget; unavailable preferred seats wait for a later Central availability observation instead of spinning a local discovery loop.
- The Client records `last_checked_at` and leaves the monitor `running` for `preferred_seats_unavailable` or `showtime_unavailable`; Central waits for an exact false-to-true availability edge before issuing another execution. Generic `booking_preparation_failed` results request retry, and Central terminally marks the monitor failed only after its retry policy is exhausted.
- A weekday monitor's rolling search horizon is validated at 1–14 days. Authentication, CAPTCHA, provider access/throttling, and provider-contract changes are terminal `failed` results with stable reason codes (`authentication_required`, `captcha_required`, `provider_contract_changed`, `provider_access_blocked`, `provider_throttled`). Lease loss and `client_interrupted` are ambiguous terminal results; Central moves the monitor to `payment_unknown` until the user verifies CGV history.
- A prepared payment handoff stores reservation status `prepared` and moves the monitor to `triggered`.
- User abandonment leaves the prepared reservation unchanged, unlinks it from the monitor, and returns the monitor to `pending`. The 15-minute handoff deadline, browser failure, or Client shutdown leaves the reservation `prepared` and moves the monitor to `payment_unknown`; this is terminal until the user explicitly retries.
- A confirmed reservation would be `booked`, but the current manual payment handoff does not claim confirmation without an authoritative receipt.
- Cancellation/expiry moves active work to `stopped`; unrecoverable application failure moves it to `failed`.
- Retry is permitted only from `triggered`, `payment_unknown`, `failed`, or `stopped`; it abandons a retained payment browser first, then returns the monitor to `pending` before starting work.

### Execution command

- Claim result with no ID means no work. A claimed command must match the user-owned monitor, preset, theater, auditorium, movie, date, time window, and positive availability.
- Heartbeat renews before lease expiry. Lost lease cancels the browser and reports `failed` with `execution_lease_lost`.
- Invalid payload reports `failed` without opening a browser. Successful payment preparation reports `completed` after the handoff is retained.
- Generic preparation failure reports `retry_requested` with `booking_preparation_failed`; unavailable, user-action, and ambiguous results use `failed` so Central owns their distinct terminal or availability-edge policy.
- While any payment handoff is open, the Client does not claim another command; Central remains the queue owner.
- `execution.ready` is durable and replayable. Buffered wake coalescing prevents notification loss before a waiter attaches; stream readiness retriggers a claim after reconnect. Releasing the last local payment handoff publishes a separate local wake without consuming the Central wake.

### Cancellation operation

- Reservation moves to `cancellation_committing` before the irreversible browser commit. An ambiguous commit becomes `cancellation_unknown`; only confirmed completion becomes `cancelled`.
- External operation moves `prepared` to `unknown` on ambiguous commit failure, `attention_required` when manual review is required, or `confirmed` after browser confirmation and `reconciled` after reservation becomes `cancelled`.
- Unknown results are never reported as cancelled.

### Account, task, and notification

- Account state is `checking`, then `authenticated`, `unauthenticated`, or `error`. CGV credentials stay in the OS credential vault and never enter Central resources. Checks and monitor mutations never start login: CAPTCHA completion always requires an explicit user action in the visible browser.
- Background UI task state is `running`, then `completed`, `stopped`, or `failed`; duplicate task IDs return conflict.
- The Client connection is `loading` during startup, `ready` after a successful refresh, `stale` while retaining previously loaded data after a transient failure, and `unavailable` when no usable state exists. A requested catalog backfill can return `waiting` without claiming completion.
- App events are Central-owned, can be marked read or cleared, and records older than six months are eligible for cleanup.

## Local UI mutations

The loopback API is same-process only and enforces host/origin and security headers. React owns form and in-flight state; Central remains the durable owner.

| User action | Local service point / bridge | Submit and success | Error, retry, and rollback |
| --- | --- | --- | --- |
| Open/restore CGV login | `POST /api/auth/open`, `POST /api/auth/restore` | Explicit user action starts one visible `authentication` task; restored credentials only prefill the form and the user completes CAPTCHA | Duplicate reports that the existing login browser is open; failure becomes account/task error |
| Save/delete CGV credentials | `PUT /api/account/credentials`, `DELETE /api/account/credentials` | Vault mutation; explicit save opens the visible login flow and delete clears saved marker | Vault error leaves previous durable value; password is never returned |
| Resolve/watch seat map | `POST /api/catalog/seat-map`, browser `GET /api/catalog/seat-map:watch`, desktop `WatchSeatMap` / `StopSeatMapWatch` | Sends only `auditoriumId` to Central. The generated `WatchSeatMapResponse` is carried by the typed `cineko.seat-map` SSE or Wails runtime event and updates `queued`, `collecting`, `waitingForShowtime`, `retryScheduled`, `blocked`, or snapshot-backed `idle` without polling. | Missing layout keeps preset editing available; Client never accepts dates, force flags, or Probe controls. Browser stream transport reconnect is handled by EventSource; the desktop bridge owns one cancelable stream. Missing/invalid ProtoJSON and idle without a snapshot fail closed. |
| Create/update/delete preset | `POST /api/presets`, `PUT /api/presets`, `DELETE /api/presets` | Save/delete is in-flight; success reloads and navigates/clears dialog | Conflict reloads authoritative state; failed form remains editable |
| Create/update/delete/retry monitor | `POST /api/monitors`, `PUT /api/monitors`, `DELETE /api/monitors`, `POST /api/monitors/retry` | Submit/retry/delete state is owned by feature hooks; success reloads | Conflict reloads; delete dialog closes after request; retry failure leaves terminal monitor |
| Prepare/commit cancellation | `POST /api/reservations/cancel` | First call previews, explicit commit performs fenced browser action | Unknown commit is preserved for reconciliation, never optimistically cancelled |
| Notifications | `POST /api/events`, `POST /api/events/read`, `DELETE /api/events` | UI updates read/clear immediately and Central persists | Persistence failure is non-blocking for transient feedback and recovers on reload |
| Network settings | Desktop `SaveNetworkSettings` | Load required; 15-second connectivity check precedes live switch and Central save | Invalid/unreachable settings are not saved; persistence failure restores previous live egress |
| Hook settings | Desktop `SaveHookSettings` | Load required; validated adapters replace the active set | Failure keeps editable form and previous active settings |

Read-only loopback points are `GET /api/state`, `GET /api/status`, `GET /api/account`, `GET /api/auditoriums`, `GET /api/seat-map`, `GET /api/catalog/seat-map:watch`, and `GET /api/events`.

## Browser, proxy, and payment boundaries

- CGV schedule discovery captures successful browser responses only from `/api/v1/booking/searchMovScnInfo`; removed legacy endpoints, incomplete rows, and malformed provider responses fail closed instead of creating display-derived identities.
- Direct networking is valid. A user may optionally configure standard HTTP, HTTPS, or SOCKS5 proxies. Managed Soxy inventory belongs only to Central's dedicated Probe infrastructure and is never configured by Client.
- Scan work uses a fresh randomized identity/proxy selection. Account and booking work reuse one user session identity. A successful visible login atomically snapshots owner-only cookies and origin storage; later account and isolated warm-booking profiles restore that snapshot before navigation. Anonymous or failed checks never overwrite it.
- Booking demand is Client-local and demand-driven: active monitors, including the supported nonmember flow, request a warm target of two disposable per-slot Playwright drivers, capped at three; no active demand requests zero. Each slot has a distinct profile and is consumed after one logical booking task, while a prepared-payment slot remains exclusively retained until release.
- When a member session snapshot exists, warm readiness restores and validates it in the isolated slot profile; without one, the slot is prepared for the nonmember path. An execution that genuinely requires authentication reports that reason distinctly and asks for visible login; warm browsers never attempt a background CAPTCHA login. Driver shutdown is bounded and reaped; a browser/context crash fails its lease closed before a replacement is started.
- Resource blocking is scan-only; interactive login, seat selection, and payment keep required resources.
- The Client selects seats against live CGV availability using the latest layout supplied by Central. Central owns layout acquisition and version history but never owns login cookies, live seat selection, payment authentication, or CGV credentials.
- The Client stops at the user-visible payment handoff. It does not infer payment success without an authoritative receipt.

The implementation boundary follows the same ownership: `internal/booking` owns
the demand-driven lease state machine and process reaping contract;
`internal/adapters/cgv` owns CGV/Playwright process details;
`internal/adapters/browserfactory` composes egress, profiles, and CGV adapters;
`internal/interfaces/webui` maps local UI mutations; and `main` only wires
these capabilities together. The booking package has no Central, UI, or CGV
adapter dependency.

## Supply-chain and drift boundary

- Go dependencies are selected by `go.mod`/`go.sum` and reproduced from committed `vendor/` metadata. Vendored stealth assets retain their upstream license.
- Frontend dependencies are selected by `frontend/package-lock.json`; generated embedded assets are rebuilt from `frontend/src` during checks/releases.
- Client, browser, and Playwright metadata are separate compatible release components. The Client publisher receives the minimum Launcher version plus the browser revision and Playwright version produced by the locked runtime workflows, then writes the generated `ClientRelease` contract directly. A Client release first ensures the immutable Playwright runtime and its official Chrome for Testing manifest are registered, then builds and registers the Client. Existing runtime assets and manifests are reused and never overwritten.
- `scripts/verify-behavior-contract.sh` fails when a Central/local service point or domain state literal appears in source without this inventory.
