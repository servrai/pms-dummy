# pms-dummy

A disposable cloud-PMS **test polygon** for servr.ai's PMS integration, webhook lane.
Single Go binary, SQLite, and a JavaScript-free forms-only web UI. It mirrors the
[PMS webhook guide](https://api-dev.servr.ai/docs/guides/pms-webhook.html) from the PMS side:

- **Receives** servr's signed action deliveries (`POST {base}/actions`) — `check_in`,
  `edit_reservation`, `cancel_reservation`, `checkout`, `get_reservation` — with
  `request_id` idempotency and the documented `action_error_code` semantics.
- **Answers** reservation reads (`GET {base}/reservations/{booking_ref}`); a 404 means
  "deleted from the PMS".
- **Sends** signed events to servr's inbound webhook:
  `reservation.sync` (full manifest + room inventory), `reservation.record`,
  `reservation.deleted`, `action.confirm`.
- **Observability**: inbound action log with exact wire payloads and outcomes
  (`created → synced | error`, duplicates marked), outbound event log with responses,
  per-reservation sync status, and fault-injection controls (`force_500`, `timeout`,
  `delay`, `respond_error_code`) to exercise servr's retry/backoff behavior.

The RPA lane ([PMS RPA guide](https://api-dev.servr.ai/docs/guides/pms-rpa.html)) is the
same contract over five polled endpoints for on-premise desktop PMSes; pms-dummy
implements the webhook lane only.

## Signature scheme

Every request in both directions is signed:

```
x-servr-timestamp: <unix seconds, ±5 min window>
x-servr-signature: sha256=hex(HMAC_SHA256(secret, timestamp + "." + rawBody))
```

The MAC covers the timestamp, a literal `.`, and the **raw** request bytes (GET requests
sign the empty body). pms-dummy rejects stale or mismatching signatures with 401.

## Running

```sh
go run . -addr :8080 -db /tmp/pms.db
# UI at http://127.0.0.1:8080 — "Enter as admin" (the login/password inputs are
# deliberately disabled and not wired; there is a single admin identity).
```

In production (dev): docker container, port 80, SQLite on a volume, behind the
Cloudflare tunnel at `https://pms-dummy-dev.servr.ai`.

`PMS_DUMMY_OWN_BASE_URL` is display-only: it feeds the "what to paste into servr"
hints on the settings page.

## Wiring it into servr (dev)

1. Open <https://pms-dummy-dev.servr.ai>, enter as admin.
2. Webhook settings → regenerate (or paste your own) signing secret.
3. In servr hotel-dev: Hotel Settings → Integrations → PMS → mode **Webhook**, base URL
   `https://pms-dummy-dev.servr.ai`, deliveries path `/actions`, reservations path
   `/pms/reservations/{booking_ref}` (defaults; both configurable on both sides), and the secret.
4. Check-in a guest in servr guest lane → watch the delivery land in the inbound log.

## E2E reservations API (test data for mobile e2e)

`POST /api/e2e/reservations` creates a reservation programmatically — through the same
service code as the "New reservation" form — and then pushes it to servr, so CI (servr-mobile
e2e) can seed a guest booking without driving the UI.

**Auth.** Same scheme and headers as every other signed request (see above), keyed with the
wired hotel's signing secret (Webhook settings): `x-servr-timestamp` + `x-servr-signature:
sha256=hex(HMAC_SHA256(secret, timestamp + "." + rawBody))`.

**Request** (`Content-Type: application/json`):

| field | |
|---|---|
| `hotel_code` | required; must equal the hotel code configured in Webhook settings |
| `booking_ref` | optional; `[A-Za-z0-9_-]{1,64}`, not ending in `-<digits>`. Generated (`E2E…`) when absent |
| `guest` | required: `first_name`, `last_name`, `email`, `phone`; optional `nationality` (ISO 2–3 letters) |
| `arrival_date`, `departure_date` | required, `YYYY-MM-DD`, departure ≥ arrival |
| `adults`, `children` | optional, default 1 / 0 |
| `room_type` | optional; stored on the reservation, sent to servr as `RoomCategoryCode` |
| `status` | optional; only `Reserved` (the form's new-reservation status; aliases `pending`, `confirmed`) |

**Behaviour.** Creates the row, then synchronously sends `reservation.sync` (full manifest —
this is what creates the booking in servr) and, if that was accepted, `reservation.record`
(full guest fields). Both show up in the outbound event log; the reservation's sync status
becomes `synced` or `error`. servr's `booking_ref` is the returned `booking_ref`.

**Responses.**

| status | when |
|---|---|
| `201` | created: `{"booking_ref","reservation_id","status","delivered","events":{"reservation.sync","reservation.record"}}` — `delivered` is true when servr accepted both; each event is `delivered`, `failed` or `skipped` |
| `401` | signature missing, stale (±5 min) or mismatching |
| `404` | `hotel_code` is not the hotel this instance is wired to, or no signing secret is set |
| `409` | `booking_ref` already exists |
| `422` | validation failed: `{"error":"validation failed","fields":{"guest.email":"…"}}` |

The reservation is created even when servr refuses the push (`201` with `delivered:false`);
the outbound event log has servr's answer. Each push can take up to 60 s on servr's side.

```sh
BASE=https://pms-dummy-dev.servr.ai
SECRET='<hotel signing secret>'   # from CI secrets, never committed
BODY='{"hotel_code":"POL0001","guest":{"first_name":"Ada","last_name":"Lovelace","email":"ada@example.com","phone":"+15550100","nationality":"GB"},"arrival_date":"2026-10-20","departure_date":"2026-10-22","adults":2,"children":0,"room_type":"STD"}'
TS=$(date +%s)
SIG=$(printf '%s.%s' "$TS" "$BODY" | openssl dgst -sha256 -hmac "$SECRET" -hex | sed 's/^.*= //')
curl -sS -X POST "$BASE/api/e2e/reservations" \
  -H 'Content-Type: application/json' \
  -H "x-servr-timestamp: $TS" \
  -H "x-servr-signature: sha256=$SIG" \
  --data-raw "$BODY"
# → 201 {"booking_ref":"E2E…","reservation_id":42,"status":"Reserved","delivered":true,
#        "events":{"reservation.sync":"delivered","reservation.record":"delivered"}}
```

Sign exactly the bytes you send (`--data-raw "$BODY"`, no re-serialisation). Note the
blackbox suite below rotates the signing secret; CI that uses this API must re-read it
(or re-paste the known secret) afterwards.

## Error-code map (PMS → servr)

| code | pms-dummy emits when |
|---|---|
| `reservation_not_found` | no such reservationNumber-lineNumber |
| `reservation_already_checked_in` | check_in on a CheckedIn stay |
| `superseded_by_check_in` | check_in on a Cancelled stay |
| `stale_cancelled` | checkout/cancel on a non-live stay, check_in on CheckedOut |
| `cancelled_by_staff` | edit/cancel on a Cancelled stay |
| `room_unavailable` | check_in moving into a room occupied by another stay |

## Testing

- `go test ./internal/...` — unit tests (signature vectors, action state machine, builders).
- `go test ./e2e -count=1` with `BASE_URL` (default `http://127.0.0.1:8080`) — blackbox
  suite: drives the forms UI over HTTP, verifies signatures both ways, dedupe, error
  codes, fault injection, and the outbound self-loop (`/dev/echo`). CI runs it against
  the live dev deployment after every rollout.

The e2e suite rotates the signing secret via the settings form (it needs a known secret
to sign with). If you wired servr against a previous secret, re-paste afterwards.

## Repo layout

```
internal/sign      HMAC scheme, both directions
internal/store     SQLite: rooms, reservations, settings, actions, events, sessions
internal/pms       wire contract, action state machine, record/manifest builders
internal/webhook   inbound signature gate, outbound signed sender
internal/web       HTTP: signature-gated API (incl. /api/e2e/reservations) + session-gated no-JS UI
e2e/               blackbox tests (stdlib only, no internal/ imports)
```
