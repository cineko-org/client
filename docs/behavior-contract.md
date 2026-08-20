# Client behavior contract

This inventory records the externally observable Client contract. It omits hosts, credentials, deployment topology, browser evasion details, and other implementation-only data.

## Trust, version, and ownership

- Production startup accepts a single-use Launcher ticket through `POST /v1/client-sessions`. The returned user, installation, device, release generation, Client artifact, browser revision/artifact, Playwright version/artifact, and contracts v3 protocol must exactly match the launch envelope.
- After Wails startup has installed the local application supervisors, Client atomically publishes the launch nonce to an owner-only marker under the shared data directory. Invalid, stale, or non-private markers never certify startup.
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
| Execution | `POST /v1/executions:claim`, `PUT /v1/executions/{executionId}/heartbeat`, `PUT /v1/executions/{executionId}/result` | Client claims once at startup and thereafter only on durable `execution.ready.v1`, stream reconnect, or local payment-handoff release; lease token fences heartbeat/result | No-work is an event wait, not polling; retryable claim transport/server failures use bounded backoff or an earlier reconnect wake; authentication/protocol failures terminate the supervisor; heartbeat loss cancels browser work and completes failed |
| Catalog | `GET /v1/catalog`, `POST /v1/catalog/snapshots`, `GET /v1/catalog/auditoriums/{auditoriumId}/seat-map`, `PUT /v1/catalog/seat-map-versions/{versionId}`, `POST /v1/catalog/auditoriums/{auditoriumId}/seat-map:request` | Direct snapshot and seat-map writes require the authenticated launch installation in `X-Cineko-Installation-Id`; snapshots and immutable layout hashes are idempotent; seat-map backfill is a request, not a local browser promise | A missing installation identity fails locally before mutation; missing seat map is a waiting state; changed layout creates a new version |
| Presets | `POST /v1/presets`, `GET /v1/presets`, `PUT /v1/presets/{id}`, `GET /v1/presets/{id}`, `DELETE /v1/presets/{id}` | Owner/revision checks apply | Delete is idempotent when already absent; conflicts require reload |
| Monitors | `POST /v1/monitors`, `GET /v1/monitors`, `PUT /v1/monitors/{id}`, `GET /v1/monitors/{id}`, `DELETE /v1/monitors/{id}` | Owner/revision checks apply | Delete is idempotent when already absent; conflicts require reload |
| Reservations | `POST /v1/reservations`, `GET /v1/reservations`, `PUT /v1/reservations/{id}`, `GET /v1/reservations/{id}`, `DELETE /v1/reservations/{id}` | Owner/revision checks apply | Delete is idempotent when already absent; conflicts require reload |
| External operations | `POST /v1/external-operations`, `GET /v1/external-operations`, `PUT /v1/external-operations/{id}`, `GET /v1/external-operations/{id}`, `DELETE /v1/external-operations/{id}` | Owner/revision checks apply | Delete is idempotent when already absent; conflicts require reload |
| App events | `POST /v1/app-events`, `GET /v1/app-events`, `PUT /v1/app-events/{id}`, `GET /v1/app-events/{id}`, `DELETE /v1/app-events/{id}` | Owner/revision checks apply | Delete is idempotent when already absent; conflicts require reload |
| Configuration | `GET /v1/configuration`, `PUT /v1/configuration` | Portable non-secret snapshot contains presets and monitors only; whole replacement is revision fenced | Conflict preserves both current Central state and imported file |
| Change stream | `GET /v1/events/stream` | Bearer session, Last-Event-ID cursor, contracts v3 control events | Transport and 5xx reconnect with jittered backoff; protocol/4xx failure is surfaced and terminates Client instead of running stale |

## Domain state machines

### Monitor and booking

- `pending` is eligible. Execution or a local retry moves it to `running`.
- No matching showtime or seat keeps an opening monitor eligible; transient failures back off. Cancellation mode with no open booking is terminal `failed`.
- A prepared payment handoff stores reservation status `prepared` and moves the monitor to `triggered`.
- User abandonment, the 15-minute handoff deadline, or Client shutdown moves the reservation to `unknown` and monitor to `payment_unknown`. This is terminal until the user explicitly retries.
- A confirmed reservation would be `booked`, but the current manual payment handoff does not claim confirmation without an authoritative receipt.
- Cancellation/expiry moves active work to `stopped`; unrecoverable application failure moves it to `failed`.
- Retry is permitted only from `triggered`, `payment_unknown`, `failed`, or `stopped`; it abandons a retained payment browser first, then returns the monitor to `pending` before starting work.

### Execution command

- Claim result with no ID means no work. A claimed command must match the user-owned monitor, preset, theater, auditorium, movie, date, time window, and positive availability.
- Heartbeat renews before lease expiry. Lost lease cancels the browser and reports `failed` with `execution_lease_lost`.
- Invalid payload reports `failed` without opening a browser. Successful payment preparation reports `completed` after the handoff is retained.
- While any payment handoff is open, the Client does not claim another command; Central remains the queue owner.
- `execution.ready.v1` is durable and replayable. Buffered wake coalescing prevents notification loss before a waiter attaches; stream readiness retriggers a claim after reconnect. Releasing the last local payment handoff publishes a separate local wake without consuming the Central wake.

### Cancellation operation

- Reservation moves to `cancellation_committing` before the irreversible browser commit. An ambiguous commit becomes `cancellation_unknown`; only confirmed completion becomes `cancelled`.
- External operation moves `prepared` to `unknown` on ambiguous commit failure, `attention_required` when manual review is required, or `confirmed` after browser confirmation and `reconciled` after reservation becomes `cancelled`.
- Unknown results are never reported as cancelled.

### Account, task, and notification

- Account state is `checking`, then `authenticated`, `unauthenticated`, or `error`. CGV credentials stay in the OS credential vault and never enter Central resources or `.cnk` files.
- Background UI task state is `running`, then `completed`, `stopped`, or `failed`; duplicate task IDs return conflict.
- The Client connection is `loading` during startup, `ready` after a successful refresh, `stale` while retaining previously loaded data after a transient failure, and `unavailable` when no usable state exists. A requested catalog backfill can return `waiting` without claiming completion.
- App events are Central-owned, can be marked read or cleared, and records older than six months are eligible for cleanup.

## Local UI mutations

The loopback API is same-process only and enforces host/origin and security headers. React owns form and in-flight state; Central remains the durable owner.

| User action | Local service point / bridge | Submit and success | Error, retry, and rollback |
| --- | --- | --- | --- |
| Open/restore CGV login | `POST /api/auth/open`, `POST /api/auth/restore` | Starts one `authentication` task; UI polls status | Duplicate returns conflict; failure becomes account/task error |
| Save/delete CGV credentials | `PUT /api/account/credentials`, `DELETE /api/account/credentials` | Vault mutation; save starts login and delete clears saved marker | Vault error leaves previous durable value; password is never returned |
| Refresh catalog | `POST /api/catalog/sync`, `POST /api/catalog/auditoriums`, `POST /api/catalog/seat-map` | Button is in-flight; successful Central data reloads form choices | Cooldown returns retry guidance; waiting seat map does not block preset save |
| Create/update/delete preset | `POST /api/presets`, `PUT /api/presets`, `DELETE /api/presets` | Save/delete is in-flight; success reloads and navigates/clears dialog | Conflict reloads authoritative state; failed form remains editable |
| Create/update/delete/retry monitor | `POST /api/monitors`, `PUT /api/monitors`, `DELETE /api/monitors`, `POST /api/monitors/retry` | Submit/retry/delete state is owned by feature hooks; success reloads | Conflict reloads; delete dialog closes after request; retry failure leaves terminal monitor |
| Prepare/commit cancellation | `POST /api/reservations/cancel` | First call previews, explicit commit performs fenced browser action | Unknown commit is preserved for reconciliation, never optimistically cancelled |
| Notifications | `POST /api/events`, `POST /api/events/read`, `DELETE /api/events` | UI updates read/clear immediately and Central persists | Persistence failure is non-blocking for transient feedback and recovers on reload |
| Network settings | Desktop `SaveNetworkSettings` | Load required; 15-second connectivity check precedes live switch and Central save | Invalid/unreachable settings are not saved; persistence failure restores previous live egress |
| Hook settings | Desktop `SaveHookSettings` | Load required; validated adapters replace the active set | Failure keeps editable form and previous active settings |
| Import/export | Desktop `ImportConfiguration` / `ExportConfiguration` | User-selected `.cnk`; import is revision-fenced and reloads on success | Cancel is a no-op; conflict leaves Central and file unchanged |

Read-only loopback points are `GET /api/state`, `GET /api/status`, `GET /api/account`, `GET /api/auditoriums`, `GET /api/seat-map`, and `GET /api/events`.

## Browser, proxy, and payment boundaries

- CGV schedule discovery captures successful browser responses from `/api/v1/booking/searchMovScnInfo` and the legacy `/cnm/atkt/searchMovScnInfo`; incomplete or malformed provider rows fail closed instead of creating display-derived identities.
- Direct networking is valid. Standard HTTP, HTTPS, and SOCKS5 proxies and Soxy are optional, mutually validated choices.
- Scan work uses a fresh randomized identity/proxy selection. Account and booking work reuse one user session identity. One browser process owns one page, and lifecycle limits rotate disposable browser processes.
- Booking demand is Client-local and demand-driven: active authenticated monitors request a warm target of two disposable per-slot Playwright drivers, capped at three; no active demand requests zero. Each slot has a distinct profile and is consumed after one logical booking task, while a prepared-payment slot remains exclusively retained until release.
- Warm readiness requires an explicit authenticated-state check in the isolated slot profile. Driver shutdown is bounded and reaped; a browser/context crash fails its lease closed before a replacement is started.
- Resource blocking is scan-only; interactive login, seat selection, and payment keep required resources.
- The Client selects seats against the live CGV layout and availability. Central may store layout versions but never owns login cookies, live seat selection, payment authentication, or CGV credentials.
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
- Client, browser, and Playwright metadata are separate compatible release components. `release-compatibility.json` names the exact Playwright target and browser revision and must match the locked driver's `browsers.json`. A Client release first ensures the immutable Playwright runtime and its official Chrome for Testing manifest are registered, then builds and registers the Client. Existing runtime assets and manifests are reused and never overwritten.
- `scripts/verify-behavior-contract.sh` fails when a Central/local service point or domain state literal appears in source without this inventory.
