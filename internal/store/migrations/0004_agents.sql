-- P3: agent enrollment + mTLS (FR-N01, N09, S03).
ALTER TABLE nodes ADD COLUMN arch TEXT;
ALTER TABLE nodes ADD COLUMN cert_serial TEXT;
ALTER TABLE nodes ADD COLUMN cert_not_after TEXT;
ALTER TABLE nodes ADD COLUMN revoked_at TEXT;
ALTER TABLE nodes ADD COLUMN protocol INTEGER;

CREATE TABLE enroll_tokens (
  token_hash  TEXT PRIMARY KEY,           -- sha256 of the secret; the secret itself is never stored
  label       TEXT,
  created_by  TEXT,
  created_at  TEXT NOT NULL,
  expires_at  TEXT NOT NULL,
  used_at     TEXT,
  node_id     TEXT
);
