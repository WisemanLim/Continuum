-- P3 security leftovers: TOTP MFA, two-person approval, SSO-provisioned users (FR-S02, S05).
ALTER TABLE users ADD COLUMN totp_secret TEXT;
ALTER TABLE users ADD COLUMN totp_pending TEXT;
ALTER TABLE users ADD COLUMN totp_last BIGINT NOT NULL DEFAULT 0;
ALTER TABLE users ADD COLUMN mfa_enabled INTEGER NOT NULL DEFAULT 0;
ALTER TABLE users ADD COLUMN auth_source TEXT NOT NULL DEFAULT 'local'; -- local | oidc

CREATE TABLE approvals (
  approval_id  TEXT PRIMARY KEY,
  action       TEXT NOT NULL,          -- disk.wipe | disk.drain | volume.delete | user.delete | node.revoke
  target       TEXT NOT NULL,
  requested_by TEXT NOT NULL,
  state        TEXT NOT NULL,          -- pending | approved | rejected | used | expired
  decided_by   TEXT,
  created_at   TEXT NOT NULL,
  decided_at   TEXT,
  expires_at   TEXT NOT NULL
);
CREATE INDEX ix_approvals_state ON approvals (state);
