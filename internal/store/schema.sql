-- pms-dummy schema v1, applied once via PRAGMA user_version = 1.
-- Later versions are ALTERs in store.go migrate() (v2: reservations.room_category).

CREATE TABLE rooms (
  id         INTEGER PRIMARY KEY,
  number     TEXT NOT NULL UNIQUE,
  status     TEXT NOT NULL DEFAULT 'vacant_clean_unchecked' CHECK (status IN
             ('vacant_clean_checked','vacant_clean_unchecked','vacant_dirty',
              'occupied_cleaned','occupied_dirty','do_not_disturb',
              'expected_departure','checked_out','out_of_order','off_market')),
  category   TEXT NOT NULL DEFAULT '',
  floor      INTEGER NOT NULL DEFAULT 0,
  created_at TEXT NOT NULL,
  updated_at TEXT NOT NULL
);

CREATE TABLE reservations (
  id                  INTEGER PRIMARY KEY,
  reservation_number  TEXT NOT NULL,
  line_number         INTEGER NOT NULL DEFAULT 1 CHECK (line_number BETWEEN 1 AND 99),
  status              TEXT NOT NULL DEFAULT 'Reserved' CHECK (status IN
                      ('Reserved','CheckedIn','CheckedOut','Cancelled')),
  guest_first_name    TEXT NOT NULL DEFAULT '',
  guest_surname       TEXT NOT NULL DEFAULT '',
  guest_email         TEXT NOT NULL DEFAULT '',
  guest_phone         TEXT NOT NULL DEFAULT '',
  nation_code         TEXT NOT NULL DEFAULT '',
  arrival_date        TEXT NOT NULL DEFAULT '',
  departure_date      TEXT NOT NULL DEFAULT '',
  room_number         TEXT NOT NULL DEFAULT '',
  adults              INTEGER NOT NULL DEFAULT 1,
  children            INTEGER NOT NULL DEFAULT 0,
  room_quantity       INTEGER NOT NULL DEFAULT 1,
  room_rate           REAL NOT NULL DEFAULT 0,
  rate_code           TEXT NOT NULL DEFAULT '',
  remark              TEXT NOT NULL DEFAULT '',
  group_code          TEXT NOT NULL DEFAULT '',
  checked_in_at       TEXT,
  checked_out_at      TEXT,
  checked_out_by      TEXT,
  cancelled_at        TEXT,
  cancel_reason_code  TEXT,
  cancel_reason_text  TEXT,
  sync_status         TEXT NOT NULL DEFAULT 'local' CHECK (sync_status IN ('local','synced','error')),
  last_synced_at      TEXT,
  created_at          TEXT NOT NULL,
  updated_at          TEXT NOT NULL,
  UNIQUE (reservation_number, line_number)
);

CREATE TABLE settings (
  key        TEXT PRIMARY KEY,
  value      TEXT NOT NULL,
  updated_at TEXT NOT NULL
);

-- Inbound deliveries: the idempotency + audit table. request_id is NOT unique
-- here on purpose — replays are recorded as fresh rows with status 'duplicate'
-- and deduped by looking up the latest prior outcome.
CREATE TABLE actions (
  id                 INTEGER PRIMARY KEY,
  request_id         TEXT NOT NULL,
  action_type        TEXT NOT NULL,
  booking_ref        TEXT NOT NULL DEFAULT '',
  reservation_number TEXT NOT NULL DEFAULT '',
  line_number        INTEGER,
  payload_json       TEXT NOT NULL,
  status             TEXT NOT NULL DEFAULT 'created' CHECK (status IN
                     ('created','synced','error','duplicate')),
  http_status        INTEGER,
  error_code         TEXT,
  error_message      TEXT,
  received_at        TEXT NOT NULL,
  responded_at       TEXT
);
CREATE INDEX idx_actions_received ON actions(received_at DESC);
CREATE INDEX idx_actions_request  ON actions(request_id, id DESC);

CREATE TABLE outbound_events (
  id               INTEGER PRIMARY KEY,
  event            TEXT NOT NULL,
  url              TEXT NOT NULL,
  payload_json     TEXT NOT NULL,
  response_status  INTEGER,
  response_body    TEXT,
  error            TEXT,
  duration_ms      INTEGER,
  created_at       TEXT NOT NULL
);
CREATE INDEX idx_outbound_created ON outbound_events(created_at DESC);

CREATE TABLE sessions (
  token      TEXT PRIMARY KEY,
  created_at TEXT NOT NULL,
  expires_at TEXT NOT NULL
);
