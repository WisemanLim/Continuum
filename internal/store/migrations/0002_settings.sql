-- Controller-local key/value settings (controller_id, local_node_id, …).
CREATE TABLE settings (
  key   TEXT PRIMARY KEY,
  value TEXT NOT NULL
);
