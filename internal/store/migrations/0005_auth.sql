-- P3: users, sessions, RBAC 5 roles (FR-S01, S02).
CREATE TABLE users (
  user_id        TEXT PRIMARY KEY,
  username       TEXT NOT NULL UNIQUE,
  password_hash  TEXT NOT NULL,
  role           TEXT NOT NULL,          -- viewer | operator | storage-admin | security-admin | system-admin
  disabled       INTEGER NOT NULL DEFAULT 0,
  failed_logins  INTEGER NOT NULL DEFAULT 0,
  locked_until   TEXT,
  created_at     TEXT NOT NULL,
  updated_at     TEXT NOT NULL,
  last_login_at  TEXT
);

CREATE TABLE sessions (
  token_hash   TEXT PRIMARY KEY,         -- sha256 of the bearer token
  user_id      TEXT NOT NULL REFERENCES users(user_id),
  created_at   TEXT NOT NULL,
  expires_at   TEXT NOT NULL,
  last_used_at TEXT,
  client       TEXT
);
CREATE INDEX ix_sessions_user ON sessions (user_id);
