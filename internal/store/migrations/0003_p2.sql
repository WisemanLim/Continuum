-- P2: SCAN (orphan policy, last scan), SMART cache, Conflict Center (PRD §13.1).
ALTER TABLE volumes ADD COLUMN orphan_policy TEXT NOT NULL DEFAULT 'auto';
ALTER TABLE volumes ADD COLUMN last_scan_at TEXT;
ALTER TABLE disks ADD COLUMN smart_json TEXT;

CREATE TABLE conflicts (
  conflict_id     TEXT PRIMARY KEY,
  volume_id       TEXT NOT NULL REFERENCES volumes(volume_id),
  logical_path    TEXT NOT NULL,
  kind            TEXT NOT NULL,             -- duplicate | conflict
  candidates_json TEXT NOT NULL,
  state           TEXT NOT NULL DEFAULT 'open', -- open | resolved
  resolution      TEXT,
  resolved_by     TEXT,
  resolved_at     TEXT,
  created_at      TEXT NOT NULL
);
CREATE INDEX ix_conflicts_volume ON conflicts (volume_id, state);
CREATE INDEX ix_objects_path ON objects (volume_id, logical_path);
