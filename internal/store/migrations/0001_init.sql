-- Continuum catalog schema v1 (PRD §13.1). Portable SQL: runs on SQLite (M1) and PostgreSQL (M2).
-- IDs are ULID strings with a type prefix. Timestamps are RFC3339 UTC text. Sizes are BIGINT bytes.

CREATE TABLE nodes (
  node_id           TEXT PRIMARY KEY,
  hostname          TEXT NOT NULL,
  os                TEXT NOT NULL,
  os_version        TEXT,
  agent_version     TEXT,
  state             TEXT NOT NULL,
  last_heartbeat    TEXT,
  capabilities_json TEXT,
  created_at        TEXT NOT NULL,
  updated_at        TEXT NOT NULL
);

CREATE TABLE volumes (
  volume_id            TEXT PRIMARY KEY,
  name                 TEXT NOT NULL UNIQUE,
  slug                 TEXT NOT NULL UNIQUE,
  mount_path           TEXT,
  placement_policy     TEXT NOT NULL DEFAULT 'topdir-affinity',
  min_free_bytes       BIGINT NOT NULL DEFAULT 1073741824,
  allow_spillover      INTEGER NOT NULL DEFAULT 1,
  access_mode          TEXT NOT NULL DEFAULT 'read-centric',
  trash_retention_days INTEGER NOT NULL DEFAULT 30,
  scan_interval_min    INTEGER NOT NULL DEFAULT 360,
  revision             BIGINT NOT NULL DEFAULT 1,
  created_at           TEXT NOT NULL,
  updated_at           TEXT NOT NULL
);

CREATE TABLE disks (
  disk_id           TEXT PRIMARY KEY,
  volume_id         TEXT REFERENCES volumes(volume_id),
  node_id           TEXT NOT NULL REFERENCES nodes(node_id),
  name              TEXT NOT NULL,
  provider_type     TEXT NOT NULL,
  filesystem_uuid   TEXT NOT NULL,
  native_disk_id    TEXT,
  volume_label      TEXT,
  filesystem_type   TEXT,
  mount_path        TEXT NOT NULL,
  data_root         TEXT NOT NULL,
  ordinal           INTEGER,
  role              TEXT NOT NULL DEFAULT 'always-on',
  state             TEXT NOT NULL DEFAULT 'ONLINE',
  writable          INTEGER NOT NULL DEFAULT 1,
  total_bytes       BIGINT NOT NULL DEFAULT 0,
  free_bytes        BIGINT NOT NULL DEFAULT 0,
  available_bytes   BIGINT NOT NULL DEFAULT 0,
  reserved_bytes    BIGINT NOT NULL DEFAULT 0,
  min_free_bytes    BIGINT,
  bus_type          TEXT,
  removable         INTEGER NOT NULL DEFAULT 0,
  network_backed    INTEGER NOT NULL DEFAULT 0,
  health            TEXT NOT NULL DEFAULT 'ONLINE',
  capabilities_json TEXT,
  last_seen         TEXT,
  created_at        TEXT NOT NULL,
  updated_at        TEXT NOT NULL,
  UNIQUE (volume_id, ordinal),
  UNIQUE (node_id, filesystem_uuid, provider_type)
);

CREATE TABLE objects (
  object_id         TEXT PRIMARY KEY,
  volume_id         TEXT NOT NULL REFERENCES volumes(volume_id),
  logical_path      TEXT NOT NULL,
  parent_path       TEXT NOT NULL,
  name              TEXT NOT NULL,
  object_type       TEXT NOT NULL,
  disk_id           TEXT REFERENCES disks(disk_id),
  physical_rel_path TEXT,
  size_bytes        BIGINT NOT NULL DEFAULT 0,
  checksum_algo     TEXT,
  checksum          TEXT,
  mtime_ns          BIGINT,
  mode              INTEGER,
  state             TEXT NOT NULL DEFAULT 'ok',
  verified_at       TEXT,
  created_at        TEXT NOT NULL,
  updated_at        TEXT NOT NULL
);
CREATE UNIQUE INDEX ux_objects_path_disk ON objects (volume_id, logical_path, disk_id);
CREATE INDEX ix_objects_parent ON objects (volume_id, parent_path);
CREATE INDEX ix_objects_disk ON objects (disk_id);

CREATE TABLE jobs (
  job_id           TEXT PRIMARY KEY,
  job_type         TEXT NOT NULL,
  volume_id        TEXT,
  state            TEXT NOT NULL,
  priority         INTEGER NOT NULL DEFAULT 5,
  source           TEXT,
  destination      TEXT,
  params_json      TEXT,
  plan_json        TEXT,
  progress_json    TEXT,
  errors_count     INTEGER NOT NULL DEFAULT 0,
  idempotency_key  TEXT UNIQUE,
  requested_by     TEXT,
  attempt          INTEGER NOT NULL DEFAULT 0,
  error            TEXT,
  created_at       TEXT NOT NULL,
  started_at       TEXT,
  finished_at      TEXT
);
CREATE INDEX ix_jobs_state ON jobs (state);

CREATE TABLE job_items (
  job_id       TEXT NOT NULL REFERENCES jobs(job_id),
  item_no      INTEGER NOT NULL,
  logical_path TEXT NOT NULL,
  source_path  TEXT,
  disk_id      TEXT,
  size_bytes   BIGINT NOT NULL DEFAULT 0,
  offset_bytes BIGINT NOT NULL DEFAULT 0,
  state        TEXT NOT NULL,
  error        TEXT,
  checksum     TEXT,
  PRIMARY KEY (job_id, item_no)
);

CREATE TABLE tombstones (
  tomb_id        TEXT PRIMARY KEY,
  volume_id      TEXT NOT NULL,
  logical_path   TEXT NOT NULL,
  disk_id        TEXT,
  trash_rel_path TEXT,
  size_bytes     BIGINT NOT NULL DEFAULT 0,
  checksum       TEXT,
  reason         TEXT NOT NULL,
  deleted_by     TEXT,
  deleted_at     TEXT NOT NULL,
  purge_after    TEXT
);

CREATE TABLE events (
  event_id     TEXT PRIMARY KEY,
  ts           TEXT NOT NULL,
  kind         TEXT NOT NULL,
  node_id      TEXT,
  volume_id    TEXT,
  disk_id      TEXT,
  job_id       TEXT,
  payload_json TEXT
);
CREATE INDEX ix_events_ts ON events (ts);

CREATE TABLE audit (
  seq         BIGINT PRIMARY KEY,
  ts          TEXT NOT NULL,
  actor       TEXT NOT NULL,
  action      TEXT NOT NULL,
  target      TEXT,
  node_id     TEXT,
  volume_id   TEXT,
  source      TEXT,
  destination TEXT,
  checksum    TEXT,
  result      TEXT NOT NULL,
  prev_hash   TEXT NOT NULL,
  hash        TEXT NOT NULL
);
