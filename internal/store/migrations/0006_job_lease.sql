-- P3 central mode: a job runs on exactly one Controller worker (lease renewed while running, FR-N08).
ALTER TABLE jobs ADD COLUMN claimed_by TEXT;
ALTER TABLE jobs ADD COLUMN lease_until TEXT;
