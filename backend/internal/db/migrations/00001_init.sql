-- +goose Up
CREATE TABLE settings (
  key   TEXT PRIMARY KEY,
  value BLOB NOT NULL
) WITHOUT ROWID;

CREATE TABLE sessions (
  token_hash   BLOB PRIMARY KEY,
  created_at   INTEGER NOT NULL,
  expires_at   INTEGER NOT NULL,
  last_seen_at INTEGER NOT NULL,
  user_agent   TEXT NOT NULL DEFAULT '',
  remote_addr  TEXT NOT NULL DEFAULT ''
) WITHOUT ROWID;
CREATE INDEX sessions_expires ON sessions(expires_at);

CREATE TABLE upload_sessions (
  id               TEXT PRIMARY KEY,
  place_id         TEXT NOT NULL,
  dir              TEXT NOT NULL,
  name             TEXT NOT NULL,
  target_path      TEXT NOT NULL,
  partial_name     TEXT NOT NULL,
  size             INTEGER NOT NULL,
  chunk_size       INTEGER NOT NULL,
  chunk_count      INTEGER NOT NULL,
  received_count   INTEGER NOT NULL DEFAULT 0,
  hash_list_digest BLOB NOT NULL,
  client_mtime     INTEGER,
  conflict_policy  TEXT NOT NULL CHECK (conflict_policy IN ('fail','overwrite')),
  created_at       INTEGER NOT NULL,
  last_activity_at INTEGER NOT NULL,
  UNIQUE (place_id, target_path)
);
CREATE INDEX upload_sessions_activity ON upload_sessions(last_activity_at);
CREATE INDEX upload_sessions_dir ON upload_sessions(place_id, dir);

CREATE TABLE upload_chunks (
  upload_id TEXT NOT NULL REFERENCES upload_sessions(id) ON DELETE CASCADE,
  idx       INTEGER NOT NULL,
  hash      BLOB NOT NULL,
  received  INTEGER NOT NULL DEFAULT 0,
  failures  INTEGER NOT NULL DEFAULT 0,
  PRIMARY KEY (upload_id, idx)
) WITHOUT ROWID;

CREATE TABLE jobs (
  id               TEXT PRIMARY KEY,
  type             TEXT NOT NULL CHECK (type IN ('copy','move','trash','restore','purge')),
  status           TEXT NOT NULL CHECK (status IN
                     ('queued','running','awaiting_input','completed','failed','cancelled','interrupted')),
  spec             TEXT NOT NULL,
  conflict_default TEXT CHECK (conflict_default IN ('overwrite','rename','skip')),
  bytes_total      INTEGER NOT NULL DEFAULT 0,
  bytes_done       INTEGER NOT NULL DEFAULT 0,
  items_total      INTEGER NOT NULL DEFAULT 0,
  items_done       INTEGER NOT NULL DEFAULT 0,
  current_item     TEXT,
  pending_conflict TEXT,
  temp_place_id    TEXT,
  temp_path        TEXT,
  result           TEXT,
  error_code       TEXT,
  error_message    TEXT,
  created_at       INTEGER NOT NULL,
  started_at       INTEGER,
  finished_at      INTEGER
);
CREATE INDEX jobs_status ON jobs(status);

CREATE TABLE trash_items (
  id             TEXT PRIMARY KEY,
  place_id       TEXT NOT NULL,
  original_path  TEXT NOT NULL,
  name           TEXT NOT NULL,
  is_dir         INTEGER NOT NULL,
  size           INTEGER,
  deleted_at     INTEGER NOT NULL,
  origin_unknown INTEGER NOT NULL DEFAULT 0
);
CREATE INDEX trash_items_place ON trash_items(place_id, deleted_at DESC);

CREATE TABLE share_links (
  id               TEXT PRIMARY KEY,
  place_id         TEXT NOT NULL,
  path             TEXT NOT NULL,
  is_dir           INTEGER NOT NULL,
  password_hash    BLOB,
  password_salt    BLOB,
  expires_at       INTEGER,
  created_at       INTEGER NOT NULL,
  last_accessed_at INTEGER,
  access_count     INTEGER NOT NULL DEFAULT 0
);
CREATE INDEX share_links_expires ON share_links(expires_at);

CREATE TABLE smart_snapshots (
  device    TEXT PRIMARY KEY,
  polled_at INTEGER NOT NULL,
  status    TEXT NOT NULL CHECK (status IN ('ok','warning','failing','standby','unavailable')),
  data      TEXT NOT NULL
) WITHOUT ROWID;

-- +goose Down
DROP TABLE smart_snapshots;
DROP TABLE share_links;
DROP TABLE trash_items;
DROP TABLE jobs;
DROP TABLE upload_chunks;
DROP TABLE upload_sessions;
DROP TABLE sessions;
DROP TABLE settings;
