// Controller REST client (same API as CLI and extension — PRD DP05).

export type Role = "always-on" | "semi-permanent" | "portable" | "import" | "archive";
export type DiskState = "ONLINE" | "DRAINING" | "READ_ONLY" | "OFFLINE" | "FAILED" | "DISABLED";

export interface Disk {
  disk_id: string;
  volume_id?: string;
  node_id: string;
  node_name?: string;
  name: string;
  provider_type: string;
  filesystem_uuid: string;
  volume_label?: string;
  filesystem_type?: string;
  mount_path: string;
  data_root: string;
  last_seen?: string;
  ordinal?: number;
  role: Role;
  state: DiskState;
  writable: boolean;
  total_bytes: number;
  free_bytes: number;
  available_bytes: number;
  reserved_bytes: number;
  min_free_bytes?: number;
  bus_type?: string;
  removable: boolean;
  network_backed: boolean;
  capabilities?: string[];
  effective_free_bytes: number;
  effective_min_free_bytes: number;
  used_pct: number;
  warning?: string;
}

export interface Summary {
  disk_count: number;
  online_count: number;
  total_bytes: number;
  usable_bytes: number;
  free_bytes: number;
  object_count: number;
  stored_bytes: number;
}

export interface Volume {
  volume_id: string;
  name: string;
  slug: string;
  mount_path?: string;
  placement_policy: string;
  min_free_bytes: number;
  allow_spillover: boolean;
  access_mode: "read-centric" | "full-direct" | "blocked";
  trash_retention_days: number;
  scan_interval_min: number;
  orphan_policy: "auto" | "review";
  last_scan_at?: string;
  revision: number;
  disks: Disk[];
  summary: Summary;
  warnings?: string[];
}

export interface Node {
  node_id: string;
  hostname: string;
  os: string;
  os_version?: string;
  agent_version?: string;
  state: string;
  last_heartbeat?: string;
  disk_count: number;
  remote: boolean;
  arch?: string;
  cert_not_after?: string;
  protocol?: number;
}

export interface EnrollToken {
  token: string;
  expires_at: string;
  command: string;
  ca_sha256: string;
}

export interface NodeInventory {
  online: boolean;
  disks: Discovered[];
  roots: string[];
}

export interface Discovered {
  mount_path: string;
  volume_label: string;
  filesystem_type: string;
  filesystem_uuid: string;
  bus_type: string;
  total_bytes: number;
  available_bytes: number;
  removable: boolean;
  network_backed: boolean;
  disk_id?: string;
  volume_id?: string;
}

export interface Check {
  no: number;
  name: string;
  status: "pass" | "warn" | "fail";
  detail?: string;
}
export interface Preflight {
  path: string;
  data_root: string;
  checks: Check[];
  ok: boolean;
  suggest_role?: string;
}

export interface CatalogObject {
  object_id: string;
  logical_path: string;
  name: string;
  type: "file" | "dir";
  disk_id?: string;
  disk_name?: string;
  ordinal?: number;
  physical_path?: string;
  size: number;
  checksum?: string;
  mtime_ns?: number;
  state: string;
}

export interface DiskPlacement {
  ordinal: number;
  disk_id: string;
  name: string;
  node: string;
  files: number;
  bytes: number;
  remaining_above_reserve: number;
}

export interface Plan {
  files: number;
  dirs: number;
  bytes: number;
  policy: string;
  placements: DiskPlacement[];
  excluded: { ordinal: number; disk_id: string; name: string; reason: string }[];
  sufficient: boolean;
  unplaced_files: number;
  unplaced_bytes: number;
  error?: string;
  eta_seconds: number;
  warnings: { code: string; path: string; fix?: string; message: string }[];
  volume: string;
  destination: string;
  existing_paths: number;
  min_free_bytes: number;
  spillover: boolean;
}

export interface Progress {
  job_id: string;
  state: string;
  files_total: number;
  files_done: number;
  files_skipped: number;
  files_failed: number;
  bytes_total: number;
  bytes_done: number;
  rate_bps: number;
  eta_seconds: number;
  current?: string;
  placements: Record<string, { name: string; files: number; bytes: number }>;
  reason?: string;
}

export interface Job {
  job_id: string;
  type: string;
  volume_id?: string;
  state: string;
  source?: string;
  destination?: string;
  progress?: Progress;
  errors_count: number;
  error?: string;
  created_at: string;
  started_at?: string;
  finished_at?: string;
}

export interface JobItem {
  item_no: number;
  logical_path: string;
  source: string;
  disk_id?: string;
  size: number;
  offset: number;
  state: string;
  error?: string;
}

export interface Browse {
  path: string;
  parent: string;
  dirs: { name: string; path: string }[];
  files: number;
  roots: { name: string; path: string }[];
}

export interface Tomb {
  tomb_id: string;
  logical_path: string;
  size: number;
  deleted_at: string;
  purge_after: string;
}

export interface ScanEntry {
  logical_path: string;
  disk: string;
  size?: number;
  note?: string;
}

export interface ScanReport {
  volume: string;
  mode: "incremental" | "full";
  disks: number;
  files_seen: number;
  added: ScanEntry[];
  removed: ScanEntry[];
  modified: ScanEntry[];
  corrupt: ScanEntry[];
  partials: ScanEntry[];
  partials_removed: number;
  conflicts_open: number;
  skipped_disks?: string[];
  duration_ms: number;
}

export interface ConflictCandidate {
  object_id: string;
  disk_id: string;
  disk: string;
  ordinal: number;
  size: number;
  mtime_ns: number;
  checksum?: string;
  physical_path: string;
}

export interface Conflict {
  conflict_id: string;
  volume_id: string;
  logical_path: string;
  kind: "duplicate" | "conflict";
  candidates: ConflictCandidate[];
  state: "open" | "resolved";
  resolution?: string;
  created_at: string;
  resolved_at?: string;
}

export interface SmartAttr {
  id?: number;
  name: string;
  value: number;
  worst?: number;
  thresh?: number;
  raw?: string;
  failed?: boolean;
}

export interface Smart {
  available: boolean;
  source: string;
  status: string;
  passed?: boolean;
  temperature_c?: number;
  power_on_hours?: number;
  model?: string;
  serial?: string;
  device?: string;
  attributes: SmartAttr[];
  note?: string;
  checked_at: string;
}

export interface Bench {
  size_bytes: number;
  seq_write_mb_s: number;
  seq_read_mb_s: number;
  rand_read_4k_iops: number;
  cache_bypass: boolean;
  duration_ms: number;
  at: string;
}

export interface UsageNode {
  name: string;
  path: string;
  bytes: number;
  files: number;
  children?: UsageNode[];
}

export interface Usage {
  bytes: number;
  files: number;
  tree: UsageNode;
  top_files: { logical_path: string; disk: string; size: number }[];
  types: { ext: string; bytes: number; files: number }[];
}

export interface DupeGroup {
  size: number;
  checksum: string;
  wasted_bytes: number;
  files: { logical_path: string; disk: string; ordinal: number; physical_path: string }[];
}

export interface DupeReport {
  groups: DupeGroup[];
  wasted_bytes: number;
  files_hashed: number;
  files_scanned: number;
}

export interface Share {
  name: string;
  kind: "volume" | "disk";
  volume: string;
  disk?: string;
  ordinal?: number;
  read_only: boolean;
  url: string;
  branches?: string[];
}

export interface AccessStatus {
  enabled: boolean;
  running: boolean;
  image_present: boolean;
  image: string;
  bind: string;
  port: number;
  user: string;
  password?: string;
  clients: number;
  shares: Share[];
  error?: string;
}

export interface EventItem {
  event_id: string;
  ts: string;
  kind: string;
  volume_id?: string;
  disk_id?: string;
  job_id?: string;
  payload?: Record<string, unknown>;
}

export interface Notice extends EventItem {
  message: string;
}

export interface AuditEntry {
  seq: number;
  ts: string;
  actor: string;
  action: string;
  target?: string;
  result: string;
  hash: string;
}

export interface CopyRequest {
  type?: "COPY" | "MOVE" | "DRAIN" | "REBALANCE";
  volume: string;
  source: string;
  destination: string;
  start_ordinal?: number;
  forced_ordinal?: number;
  placement_policy?: string;
  spillover?: boolean;
  min_free_bytes?: number;
  verify?: string;
  on_conflict?: string;
  on_insufficient?: string;
  window?: string;
}

export interface OverviewTiles {
  total_bytes: number;
  free_bytes: number;
  volumes: number;
  nodes_online: number;
  nodes_total: number;
  running_jobs: number;
  warnings: number;
  offline_disks: number;
  conflicts: number;
}

export interface Me {
  username: string;
  role: string;
  permissions: string[];
  auth_enabled: boolean;
  mfa_enabled?: boolean;
  auth_source?: string;
}

export interface User {
  user_id: string;
  username: string;
  role: string;
  disabled: boolean;
  created_at: string;
  last_login_at?: string;
  mfa_enabled?: boolean;
  auth_source?: string;
}

export interface Approval {
  approval_id: string;
  action: string;
  target: string;
  requested_by: string;
  state: string;
  decided_by?: string;
  created_at: string;
  expires_at: string;
}

export const APPROVAL_ACTIONS = ["disk.wipe", "disk.drain", "volume.delete", "user.delete", "node.revoke"] as const;

export const ROLES = ["viewer", "operator", "storage-admin", "security-admin", "system-admin"] as const;

export class ApiError extends Error {
  constructor(public status: number, public code: string, message: string, public info?: Record<string, unknown>) {
    super(message);
  }
}

const LS_URL = "continuum.controller";
const LS_TOKEN = "continuum.token";

function safeGet(k: string): string | null {
  try {
    return localStorage.getItem(k);
  } catch {
    return null;
  }
}

export function controllerURL(): string {
  const fromEnv = (import.meta.env?.VITE_CONTROLLER_URL as string | undefined) ?? "";
  // When the UI is served by the Controller itself, use same origin.
  const sameOrigin = typeof location !== "undefined" && location.protocol.startsWith("http") && location.port !== "5173" ? location.origin : "";
  // Served by the Controller (http, not the Vite dev port) → same origin wins over the build-time env.
  return safeGet(LS_URL) || sameOrigin || fromEnv || "http://127.0.0.1:8080";
}

export function setController(url: string, token: string) {
  try {
    localStorage.setItem(LS_URL, url);
    localStorage.setItem(LS_TOKEN, token);
  } catch {
    /* private mode: ignore */
  }
}

/** Store (or clear) the session token for the current controller. */
export function setToken(tok: string) {
  try {
    if (tok) localStorage.setItem(LS_TOKEN, tok);
    else localStorage.removeItem(LS_TOKEN);
  } catch {
    /* private mode */
  }
}

export function token(): string {
  return safeGet(LS_TOKEN) || "";
}

const approvals = new Map<string, string>();

/** Browser entry point of the SSO flow; the Controller redirects back with #oidc_token=. */
export function ssoURL(): string {
  return `${controllerURL()}/v1/auth/oidc/login?return=${encodeURIComponent(location.origin + location.pathname)}`;
}

async function call<T>(method: string, path: string, body?: unknown): Promise<T> {
  const headers: Record<string, string> = {};
  if (body !== undefined) headers["Content-Type"] = "application/json";
  const t = token();
  if (t) headers["Authorization"] = `Bearer ${t}`;
  const apKey = `${method} ${path}`;
  const ap = approvals.get(apKey); // two-person approval: retry carries the request id (FR-S05)
  if (ap) headers["X-Approval-Id"] = ap;
  let res: Response;
  try {
    res = await fetch(controllerURL() + path, { method, headers, body: body === undefined ? undefined : JSON.stringify(body) });
  } catch {
    throw new ApiError(0, "offline", "Controller disconnected");
  }
  if (!res.ok) {
    let e: { error?: { code: string; message: string; info?: Record<string, unknown> } } = {};
    try {
      e = await res.json();
    } catch {
      /* non-JSON */
    }
    if (e.error?.code === "approval_required" && typeof e.error.info?.approval_id === "string") approvals.set(apKey, e.error.info.approval_id);
    else if (ap) approvals.delete(apKey);
    if (res.status === 401 && typeof window !== "undefined" && !e.error?.info?.mfa_required) window.dispatchEvent(new Event("continuum:auth-required"));
    throw new ApiError(res.status, e.error?.code ?? "internal", e.error?.message ?? res.statusText, e.error?.info);
  }
  approvals.delete(apKey);
  if (res.status === 204) return undefined as T;
  return (await res.json()) as T;
}

export const api = {
  info: () => call<{ controller_id: string; version: string; agents_online: number; agents_total: number; local_node: Node }>("GET", "/v1/info"),
  overview: () =>
    call<{ tiles: OverviewTiles; volumes: Volume[]; warnings: string[]; recent: EventItem[] }>("GET", "/v1/overview"),
  nodes: () => call<Node[]>("GET", "/v1/nodes"),
  disks: (q = "") => call<Disk[]>("GET", "/v1/disks" + q),
  discover: () => call<Discovered[]>("GET", "/v1/disks/discover"),
  preflight: (path: string, min_free_bytes: number, node_id?: string) => call<Preflight>("POST", "/v1/disks/preflight", { path, min_free_bytes, node_id }),
  nodeToken: (label: string, ttl_min = 60) => call<EnrollToken>("POST", "/v1/nodes/tokens", { label, ttl_min }),
  revokeNode: (id: string) => call<{ revoked: boolean }>("POST", `/v1/nodes/${id}/revoke`, {}),
  inventory: (id: string) => call<NodeInventory>("GET", `/v1/nodes/${id}/inventory`),
  attach: (req: { path: string; name?: string; role?: string; min_free_bytes?: number; node_id?: string }) =>
    call<{ disk: Disk; preflight: Preflight; reattached: boolean }>("POST", "/v1/disks", req),
  patchDisk: (id: string, body: { role?: string }) => call<Disk>("PATCH", `/v1/disks/${id}`, body),
  detach: (id: string) => call<Disk>("POST", `/v1/disks/${id}/detach`),
  enable: (id: string) => call<Disk>("POST", `/v1/disks/${id}/enable`),
  disk: (id: string) => call<Disk>("GET", `/v1/disks/${id}`),
  smartHistory: (id: string) => call<{ at: string; status: string; temperature_c?: number }[]>("GET", `/v1/disks/${id}/smart/history`),
  smart: (id: string, refresh = false) => call<Smart>("GET", `/v1/disks/${id}/smart${refresh ? "?refresh=1" : ""}`),
  bench: (id: string, size_bytes: number) => call<Bench>("POST", `/v1/disks/${id}/bench`, { size_bytes }),
  benchHistory: (id: string) => call<Bench[]>("GET", `/v1/disks/${id}/bench`),
  diskUsage: (id: string) => call<Usage>("GET", `/v1/disks/${id}/usage?top=20`),
  diskDupes: (id: string) => call<DupeReport>("GET", `/v1/disks/${id}/dupes`),
  wipe: (id: string, confirm: string, passes = 1) => call<{ disk: string; files: number; bytes: number; passes: number }>("POST", `/v1/disks/${id}/wipe`, { confirm, passes }),
  reveal: (id: string) => call<{ opened: string }>("POST", `/v1/disks/${id}/reveal`, {}),
  authStatus: () => call<{ enabled: boolean; oidc?: boolean }>("GET", "/v1/auth/status"),
  me: () => call<Me>("GET", "/v1/auth/me"),
  login: (username: string, password: string, code = "") =>
    call<{ token: string; expires_at: string; user: User }>("POST", "/v1/auth/login", { username, password, code, client: "web" }),
  mfaSetup: () => call<{ secret: string; otpauth_url: string }>("POST", "/v1/auth/mfa/setup", {}),
  mfaEnable: (code: string) => call("POST", "/v1/auth/mfa/enable", { code }),
  mfaDisable: (code: string) => call("POST", "/v1/auth/mfa/disable", { code }),
  approvals: (state = "pending") => call<Approval[]>("GET", `/v1/approvals?state=${state}`),
  decide: (id: string, approve: boolean) => call<Approval>("POST", `/v1/approvals/${id}/${approve ? "approve" : "reject"}`, {}),
  logout: () => call("POST", "/v1/auth/logout", {}),
  changePassword: (old_password: string, new_password: string) => call("PUT", "/v1/auth/password", { old_password, new_password }),
  users: () => call<User[]>("GET", "/v1/users"),
  createUser: (username: string, password: string, role: string) => call<User>("POST", "/v1/users", { username, password, role }),
  patchUser: (id: string, body: { role?: string; disabled?: boolean; password?: string }) => call<User>("PATCH", `/v1/users/${id}`, body),
  deleteUser: (id: string) => call("DELETE", `/v1/users/${id}`),
  notifications: () => call<Notice[]>("GET", "/v1/notifications"),
  notifyTest: () => call<{ sent: boolean }>("POST", "/v1/notifications/test", {}),
  settings: () => call<Record<string, string>>("GET", "/v1/settings"),
  saveSettings: (s: Record<string, string>) => call<Record<string, string>>("PUT", "/v1/settings", s),
  access: () => call<AccessStatus>("GET", "/v1/access"),
  accessCredentials: () => call<AccessStatus>("GET", "/v1/access/credentials"),
  accessEnable: (bind?: string, port?: number) => call<AccessStatus>("POST", "/v1/access/enable", { bind: bind ?? "", port: port ?? 0 }),
  accessAction: (act: "disable" | "reload") => call<AccessStatus>("POST", `/v1/access/${act}`, {}),
  volumes: () => call<Volume[]>("GET", "/v1/volumes"),
  volume: (ref: string) => call<Volume>("GET", `/v1/volumes/${encodeURIComponent(ref)}`),
  createVolume: (req: {
    name: string;
    mount_path?: string;
    placement_policy: string;
    min_free_bytes: number;
    allow_spillover: boolean;
    disk_ids: string[];
  }) => call<Volume>("POST", "/v1/volumes", req),
  updateVolume: (ref: string, body: Record<string, unknown>) => call<Volume>("PATCH", `/v1/volumes/${encodeURIComponent(ref)}`, body),
  deleteVolume: (ref: string) => call<{ deleted: boolean }>("DELETE", `/v1/volumes/${encodeURIComponent(ref)}`),
  setDisks: (ref: string, disk_ids: string[], expected_revision: number) =>
    call<Volume>("PUT", `/v1/volumes/${encodeURIComponent(ref)}/disks`, { disk_ids, expected_revision }),
  files: (ref: string, path: string) => call<CatalogObject[]>("GET", `/v1/volumes/${encodeURIComponent(ref)}/files?path=${encodeURIComponent(path)}`),
  locate: (ref: string, path: string) => call<CatalogObject[]>("GET", `/v1/volumes/${encodeURIComponent(ref)}/locate?path=${encodeURIComponent(path)}`),
  mkdir: (ref: string, path: string, ordinal = 0) => call<CatalogObject>("POST", `/v1/volumes/${encodeURIComponent(ref)}/mkdir`, { path, ordinal }),
  rename: (ref: string, from: string, to: string) => call("POST", `/v1/volumes/${encodeURIComponent(ref)}/rename`, { from, to }),
  remove: (ref: string, path: string, recursive: boolean, permanent: boolean) =>
    call<{ files: number; bytes: number }>("POST", `/v1/volumes/${encodeURIComponent(ref)}/delete`, { path, recursive, permanent }),
  trash: (ref: string) => call<Tomb[]>("GET", `/v1/volumes/${encodeURIComponent(ref)}/trash`),
  restore: (id: string) => call<CatalogObject>("POST", `/v1/trash/${id}/restore`),
  scan: (ref: string, opt: { full?: boolean; disk?: string; fix_partials?: boolean }) =>
    call<ScanReport>("POST", `/v1/volumes/${encodeURIComponent(ref)}/scan`, opt),
  conflicts: (ref: string, state = "open") => call<Conflict[]>("GET", `/v1/volumes/${encodeURIComponent(ref)}/conflicts?state=${state}`),
  resolveConflict: (id: string, keep: string) => call<Conflict>("POST", `/v1/conflicts/${id}/resolve`, { keep }),
  resolveDuplicates: (ref: string) => call<{ resolved: number }>("POST", `/v1/volumes/${encodeURIComponent(ref)}/conflicts/resolve-duplicates`, {}),
  plan: (req: CopyRequest) => call<Plan>("POST", "/v1/plan", req),
  start: (req: CopyRequest) => call<Job>("POST", "/v1/jobs", req),
  jobs: (q = "") => call<Job[]>("GET", "/v1/jobs" + q),
  job: (id: string) => call<Job>("GET", `/v1/jobs/${id}`),
  jobAction: (id: string, act: "pause" | "resume" | "cancel" | "retry") => call<Job>("POST", `/v1/jobs/${id}/${act}`),
  jobItems: (id: string, state = "") => call<JobItem[]>("GET", `/v1/jobs/${id}/items${state ? `?state=${state}` : ""}`),
  browse: (path = "") => call<Browse>("GET", `/v1/fs/browse?path=${encodeURIComponent(path)}`),
  activity: (limit = 100) => call<EventItem[]>("GET", `/v1/activity?limit=${limit}`),
  audit: (limit = 100) => call<AuditEntry[]>("GET", `/v1/audit?limit=${limit}`),
  auditVerify: () => call<{ intact: boolean; first_broken_seq: number }>("GET", "/v1/audit/verify"),
};

/** URL of the diagnostics zip (token as query: a download link cannot send headers). */
export function diagnosticsURL(maskPaths: boolean): string {
  const t = token();
  return `${controllerURL()}/v1/diagnostics?mask=${maskPaths ? 1 : 0}${t ? `&token=${encodeURIComponent(t)}` : ""}`;
}

/** URL of a job's failed items as CSV (token as query for plain links). */
export function jobErrorsCSV(id: string): string {
  const t = token();
  return `${controllerURL()}/v1/jobs/${id}/errors?format=csv${t ? `&token=${encodeURIComponent(t)}` : ""}`;
}

/** Subscribe to all job progress (SSE /v1/events/stream). Returns unsubscribe. */
export function streamProgress(onEvent: (p: Progress) => void): () => void {
  const t = token();
  const es = new EventSource(controllerURL() + "/v1/events/stream" + (t ? `?token=${encodeURIComponent(t)}` : ""));
  es.onmessage = (m) => {
    try {
      onEvent(JSON.parse(m.data) as Progress);
    } catch {
      /* ignore malformed */
    }
  };
  return () => es.close();
}
