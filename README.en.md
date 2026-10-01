# Continuum

Ordinal-based, cross-platform disk pooling and disk management — *Storage without boundaries.*

Continuum binds several disks (internal, USB, folders) into one volume **in an order you choose**. It fills disk 1 first and spills over to the next ordinal when full. Files are never split, so any single disk can be unplugged and opened in Finder/Explorer as-is.

> **Scaffold info**: `wise-dev-std` plugin, profile `custom` (base `go-gin`).
> Requirements: [.docs/Continuum_PRD.pdf](.docs/Continuum_PRD.pdf) · UI design: [.docs/Continuum_UI_Design.pdf](.docs/Continuum_UI_Design.pdf) · Stack decisions: [STACK.md](STACK.md)

## Status (P1 MVP + P2 + P3 + P4 + P5 + P6 — PRD §18.2)

| # | Epic | Status | Tests |
|---|---|---|---|
| E1 | Catalog & registry (portable SQLite/PostgreSQL schema, hash-chained audit) | DONE | [impl/1](test/impl/1/result.md) |
| E2 | Disk discovery & attach (per-OS identity, 10 preflight checks, disk marker) | DONE | [impl/2](test/impl/2/result.md) |
| E3 | Volume management (ordinals, revision locking, summary formula, export/import) | DONE | [impl/3](test/impl/3/result.md) |
| E4 | Placement engine & plan (5 policies, forced/start ordinal, **FR-P08 24/6 GB reproduction**) | DONE | [impl/4](test/impl/4/result.md) |
| E5 | Transfer engine (atomic copy, BLAKE3, resume, pause, cancel, trash, move, rename) | DONE | [impl/5](test/impl/5/result.md) |
| E6 | Controller REST API + SSE | DONE | [impl/6](test/impl/6/result.md) |
| E7 | CLI `continuum` / `ctm` | DONE | [impl/7](test/impl/7/result.md) |
| E8 | Web UI (Create Volume 3-column DnD, …) | DONE | [impl/8](test/impl/8/result.md) |
| E9 | Desktop shell (Tauri) + Chrome extension T1 | DONE | [impl/9](test/impl/9/result.md) |
| E10 | CI & release basics (FR-P08 gate, goreleaser) | DONE | [impl/10](test/impl/10/result.md) |
| — | P1 full regression | PASS | [regression](test/impl/regression/result.md) |
| E11 | SCAN / direct-edit reconciliation (orphans auto-added or flagged for review, external deletes → tombstones, changes and corruption detected, stale partials, periodic scan) | DONE | [impl/11](test/impl/11/result.md) |
| E12 | Conflict Center (duplicate vs conflict, keep lowest/newest/both/disk, bulk-resolve duplicates) | DONE | [impl/12](test/impl/12/result.md) |
| E13 | Disk detail (SMART, benchmark, usage treemap, duplicate finder, open in Finder/Explorer) | DONE | [impl/13](test/impl/13/result.md) |
| E14 | Drain & rebalance (copy → verify → delete, disk leaves the volume when drained, plan preview) | DONE | [impl/14](test/impl/14/result.md) |
| E15 | SMB access point (Samba + mergerfs container, volume and per-disk shares, 3 access modes) | DONE | [impl/15](test/impl/15/result.md) |
| E16 | Operations (`/metrics`, diagnostics bundle, webhook notifications, notification center) | DONE | [impl/16](test/impl/16/result.md) |
| E17 | UI & i18n finish (complete ko/en, job detail, folder picker) | DONE | [impl/17](test/impl/17/result.md) |
| — | P2 full regression (`make test`, race, `make e2e`: 8 web specs + 1 extension) | PASS | [regression-p2](test/impl/regression-p2/result.md) |
| E18 | Agent enrollment & mTLS (private CA, one-time token pinning the CA, certificate rotation and revocation) | DONE | [impl/18](test/impl/18/result.md) |
| E19 | Agent stream & remote disks (connect-go bidi, heartbeat/inventory, 3 missed → OFFLINE, reconnect) | DONE | [impl/19](test/impl/19/result.md) |
| E20 | Cross-node transfers (remote copy/move/drain/scan/verify, PAUSED on node loss → auto-resume) | DONE | [impl/20](test/impl/20/result.md) |
| E21 | OS service install (launchd · systemd · Windows SCM) | DONE | [impl/21](test/impl/21/result.md) |
| E22 | Authentication & 5-role RBAC (PBKDF2, sessions, permission matrix, lockout, audit actor) | DONE | [impl/22](test/impl/22/result.md) |
| E23 | Central server mode (full suite on PostgreSQL, job leases, compose agents) | DONE | [impl/23](test/impl/23/result.md) |
| E24 | Extension T3 native bridge (native messaging, automatic downgrade) | DONE | [impl/24](test/impl/24/result.md) |
| E25 | Secure wipe (overwrite + remove, server-side name confirmation) | DONE | [impl/25](test/impl/25/result.md) |
| — | P3 full regression (`make test`, race, PostgreSQL, `make e2e`: 11 web + 3 extension) | PASS | [regression-p3](test/impl/regression-p3/result.md) |

| E26 | Native VFS · Linux FUSE (go-fuse: reads from the lowest ordinal, writes follow the placement policy with spillover, trash, read-only mounts) | DONE | [impl/26](test/impl/26/result.md) |
| E27 | Extension T2 browser-local pool (File System Access API, IndexedDB handles, SHA-256 verification) | DONE | [impl/27](test/impl/27/result.md) |
| E28 | P2 leftovers (fsnotify watcher, macOS SMB auto-reconnect, Linux local mergerfs, SMART temperature history, job time windows) | DONE | [impl/28](test/impl/28/result.md) |
| E29 | Security leftovers (TOTP MFA, two-person approval, OIDC SSO, OS keychain secrets) | DONE | [impl/29](test/impl/29/result.md) |
| E30 | Ops leftovers (email notifications, Grafana dashboard, Helm chart, OpenTelemetry traces) | DONE | [impl/30](test/impl/30/result.md) |
| — | P4 full regression | PASS | [regression-p4](test/impl/regression-p4/result.md) |

| E31 | Native VFS · macOS (macFUSE path + experimental FUSE-T path) | **BLOCKED** (macFUSE extension not approved · FUSE-T panics the macOS kernel → disabled by default) | [impl/31](test/impl/31/result.md) |
| E32 | Direct agent ↔ agent transfers (mTLS pull + CA-signed ticket, offset resume, BLAKE3 trailer, relay fallback) | DONE | [impl/32](test/impl/32/result.md) |
| E33 | LDAP / Active Directory sign-in (groups → roles, StartTLS, LDAPS, private CA) | DONE | [impl/33](test/impl/33/result.md) |
| E34 | Supply chain & release (reproducible builds, cosign, notarization, Authenticode, GPG, SBOM, provenance, store and enterprise policies) | DONE (static checks) | [impl/34](test/impl/34/result.md) |
| — | P5 full regression | PASS | [regression-p5](test/impl/regression-p5/result.md) |
| E35 | In-file progress for direct transfers (agent `Progress` stream → job progress / SSE) | DONE | [impl/35](test/impl/35/result.md) |
| E36 | Agent-side OTel traces (traceparent on every command, one trace across controller and agent) | DONE | [impl/36](test/impl/36/result.md) |
| E37 | Web UI inside the controller image (non-root, `/web`, release image) | DONE (static + simulation) | [impl/37](test/impl/37/result.md) |
| — | P6 full regression | PASS | [regression-p6](test/impl/regression-p6/result.md) |

Not implemented: Windows WinFsp VFS · native macOS VFS (E31 BLOCKED; macOS uses the SMB access point) · actual signing, notarization and store upload (needs certificates and accounts) · OTLP gRPC.

## Components

| Component | Location | Notes |
|---|---|---|
| Controller (+ embedded agent, M1) | `cmd/controller`, `internal/{api,core,transfer,placement,disk,store,audit}` | REST/SSE API, registry & catalog, placement, transfer, SCAN and drain/rebalance engines |
| Agent | `cmd/agent`, `internal/agent` | remote node daemon: enrollment, mTLS stream, disk commands, OS service, Chrome native host |
| Agent hub | `internal/hub`, `internal/pki`, `internal/agentproto`, `internal/nodefs` | enrollment tokens, CA, connect-go stream, blob relay, per-node filesystem (local/remote) |
| Auth & RBAC | `internal/auth` | users, sessions, 5-role permission matrix, TOTP MFA, two-person approval, OIDC SSO, LDAP/AD |
| Secrets | `internal/secrets` | macOS Keychain · Linux Secret Service · Windows Credential Locker, 0600 file fallback |
| Native VFS | `internal/vfs` | Linux FUSE / macOS macFUSE mount (a volume as one filesystem), runs inside the controller process |
| Direct transfers | `internal/agent/peer.go`, `internal/hub` (`Pull`), `internal/pki/ticket.go` | agent ↔ agent mTLS pull, controller CA-signed tickets, resume offsets |
| Supply chain | `.goreleaser.yaml`, `.github/workflows/release.yml`, `scripts/{repro-check.sh,release-lint.py}`, `tools/notices`, `deploy/{release,extension-policy}` | reproducible builds, signing, SBOM, provenance, store / enterprise rollout |
| FS watcher | `internal/watch` | fsnotify: direct edits on local disks reach the catalog within seconds |
| Tracing | `internal/tracing` | OpenTelemetry OTLP/HTTP JSON export (no SDK dependency) |
| SMB access point | `internal/access`, `deploy/samba/Dockerfile` | the controller configures and runs a Samba + mergerfs container (`continuum-smb`) |
| Operations | `internal/ops`, `deploy/grafana`, `deploy/helm/continuum` | Prometheus `/metrics`, diagnostics bundle, webhook and email notifications, Grafana dashboard, Helm chart |
| CLI | `cmd/continuum` (`ctm` = symlink) | calls the same REST API |
| Migrations | `cmd/migrate`, `internal/store/migrations/*.sql` | embedded, shared by SQLite and PostgreSQL |
| Web UI | `apps/web` | Vite + React + TS, dnd-kit, light/dark design tokens |
| Desktop | `apps/desktop` | Tauri v2 shell + controller sidecar |
| Chrome extension | `apps/extension` | wxt MV3: side panel (T1) + full console (reuses the web UI) + browser-local pool (T2) + native host (T3) |

## Tech stack

| Area | Tool |
|------|------|
| Controller · CLI · Agent | Go 1.26 (no cgo), `net/http` REST + SSE, cobra, connect-go (JSON codec, agent stream) |
| DB | SQLite (modernc, WAL, local/M1) · PostgreSQL 17 (pgx, dev/staging/prod) |
| Hash · IDs | BLAKE3 (zeebo/blake3) · ULID |
| Disk discovery | gopsutil + diskutil (macOS) / lsblk (Linux) / Get-Volume (Windows) |
| Web | React 19 · Vite 7 · TypeScript 5.9 · @dnd-kit · Vitest · Playwright |
| Desktop | Tauri 2.12 + tauri-plugin-shell (sidecar) |
| Extension | wxt 0.21 (MV3) |
| Packages | go modules · pnpm 11 workspaces · cargo |
| Local processes | goreman (`Procfile.dev`) |
| SMB access point | Samba 4.17 + mergerfs 2.33 (debian bookworm container, Docker) |
| VFS · watcher | hanwen/go-fuse v2.11 (Linux FUSE) · fsnotify v1.10 |
| Secrets | wraps OS tools: `security` (macOS) · `secret-tool` (Linux) · PowerShell PasswordVault (Windows) |
| Auth extensions | TOTP (RFC 6238, standard library) · OIDC Authorization Code + PKCE (RS256 JWKS verified in-house) · go-ldap v3.4 (LDAP/AD) |
| Release | goreleaser v2 (reproducible builds, cosign keyless, notarytool, jsign/Azure Trusted Signing, syft SBOM, ghcr images) · attest-build-provenance · tauri-action · wxt submit · dependabot · govulncheck |
| Observability | Prometheus text · OTLP/HTTP JSON traces · Grafana dashboard JSON |
| Deployment | Helm 3 chart (`deploy/helm/continuum`) |
| Release | goreleaser v2 · release-please · GitHub Actions |

## Prerequisites

- Go 1.26+, Node 22+, pnpm (`corepack enable`), GNU Make, Docker Desktop (dev stack and SMB access point — without Docker only the access point is unavailable)
- goreman: `go install github.com/mattn/goreman@latest` (put `$(go env GOPATH)/bin` on PATH)
- Desktop builds: Rust stable (`rustup`, put `~/.cargo/bin` on PATH)
- E2E: Google Chrome (web) · `npx playwright install chromium` (extension — branded Chrome blocks `--load-extension`)

## Quick start

```bash
make preflight            # check tools
make local-build          # go build + pnpm install (+ continuum-samba:local image when Docker is available)
make db-migrate           # migrate up: applied [0001_init 0002_settings 0003_p2] (sqlite)
make local-all            # controller (127.0.0.1:8080) + web dev server (:5173) in background
open http://localhost:5173
make local-stop
```

CLI (same controller):

```bash
go build -o bin/continuum ./cmd/continuum && ln -sf continuum bin/ctm
bin/continuum disk attach /Volumes/ExternalSSD --role portable
bin/continuum volume create research-pool --disks disk-01,win-d,mac-ext --policy topdir --min-free 100GiB
bin/continuum plan copy ~/Downloads/research-data research-pool:/research/2026-q3
bin/continuum copy ~/Downloads/research-data research-pool:/research/2026-q3 --yes
bin/continuum scan research-pool                       # catalog files added directly in Finder etc.
bin/continuum volume drain research-pool mac-ext -y   # empty a disk and remove it from the volume
bin/continuum access enable                            # smb://127.0.0.1:4445/research-pool
```

Remote nodes (agents over mTLS — the controller listens on `CONTINUUM_AGENT_ADDR`):

```bash
bin/continuum node token --label nas-1                 # one-time token + the command to run
# on the new node:
continuum-agent run --controller https://controller:8443 --token CTM1.… --root /srv/disk1   # enrolls on first start only
continuum-agent service install --root /srv/disk1      # launchd / systemd / Windows service
bin/continuum disk attach /srv/disk1 --node <node_id> --volume research-pool
```

Users and roles (logins are required from the moment the first user exists):

```bash
bin/continuum user add admin --role system-admin && bin/continuum login admin
bin/continuum user add ops --role operator
```

Desktop and extension:

```bash
pnpm --filter @continuum/desktop dev                  # builds the sidecar + opens the Tauri dev window
pnpm --filter @continuum/desktop build --bundles app  # Continuum.app (includes the controller sidecar)
pnpm --filter @continuum/extension build              # .output/chrome-mv3 → chrome://extensions "Load unpacked"
```

## Make targets

| Command | Description |
|------|------|
| `make preflight` | requires Go 1.26+ and Node 22+; checks pnpm, docker, goreman, cargo |
| `make test` | `go test ./...` + `pnpm -r --if-present test` (vitest) |
| `make e2e` | build web and extension (+ T3 test build), then Playwright E2E (real controller, agent and native host) |
| `make release-check` | supply-chain checks: reproducible build (two builds, SHA-256 compare) · THIRD_PARTY_NOTICES · release config lint |
| `make deploy` | Controller Helm deploy (`deploy/helm/continuum`, needs `database.existingSecret`) |
| `make help` | list targets |
| `make local-build` | `go mod download && go build ./...` + `pnpm install` + (if Docker is available) `docker build -t continuum-samba:local deploy/samba` |
| `make local-all` | goreman in background (`-env .env.local`); with `LOCAL_INFRA=postgres` also starts docker postgres |
| `make local-logs` / `local-stop` / `local-restart` / `local-ps` | logs / stop / restart / status |
| `make <env>-all` | [dev\|staging\|prod] postgres + controller containers (`--profile agents` adds the compose agent) |
| `make <env>-build` | [dev\|staging] rebuild images and start (no prod) |
| `make <env>-logs` / `-stop` / `-restart` / `-ps` | container logs (`SVC=`) / teardown / restart / status |
| `make db-migrate [ENV=]` | apply embedded migrations (idempotent) |
| `make db-seed [ENV=]` | not implemented (override with `SEED=`) |
| `make db-reset [ENV=]` | local = delete SQLite · others = recreate schema · refused on prod |
| `make db-fresh [ENV=]` | reset + seed |

## Environment variables

| File | Purpose |
|------|------|
| `.env.example` | committed template (placeholders) — `cp .env.example .env.<env>` |
| `.env.local` | M1 single PC, SQLite |
| `.env.dev` | M2 shared dev, compose postgres (placeholder password) |
| `.env.staging` | prod-like verification (secrets injected) |
| `.env.prod` | keys only — values injected by CI/Vault |

| Variable | Description |
|------|------|
| `ENV` | local/dev/staging/prod |
| `BIND_HOST` / `API_PORT` / `POSTGRES_PORT` | compose bind IP and ports (default `127.0.0.1` / `8080` / `5432`) |
| `CONTROLLER_ADDR` | controller listen address (local `127.0.0.1:8080`, container `:8080`) |
| `CONTROLLER_URL` | URL used by agents and CLI |
| `DATABASE_URL` | `sqlite:///./local.db` or `postgresql://…` (SQLite on local disk only, NFR-A04) |
| `POSTGRES_USER` / `POSTGRES_PASSWORD` / `POSTGRES_DB` | compose postgres |
| `CONTINUUM_TOKEN` | if set, `/v1/*` requires `Authorization: Bearer` (FR-S02 local token) |
| `CONTINUUM_ALLOWED_ORIGINS` | allowed CORS origins (default `localhost:5173`, `tauri://localhost`, `chrome-extension://*`); same origin is always allowed |
| `CONTINUUM_WEB_DIR` | if set, the controller serves `apps/web/dist` at `/` |
| `CONTINUUM_AGENT_ADDR` | agent mTLS endpoint (default `127.0.0.1:8443`, `off` = single machine) |
| `CONTINUUM_AGENT_URL` / `CONTINUUM_AGENT_SANS` | public URL put into token commands / extra server certificate SANs |
| `CONTINUUM_PKI_DIR` | CA certificate and key (0600) (default `<UserConfigDir>/Continuum/pki-<controller>`) |
| `CONTINUUM_AGENT_HEARTBEAT_SEC` | agent heartbeat period (default 10; 3 missed → OFFLINE) |
| `AGENT_PORT` | host port compose publishes for agents (default 8443) |
| `CONTINUUM_AGENT_DIR` / `CONTINUUM_CONTROLLER_AGENT_URL` / `CONTINUUM_ENROLL_TOKEN` | [agent] state directory / controller agent URL / first-start token |
| `CONTINUUM_SESSION_FILE` / `CONTINUUM_PASSWORD` | [CLI] `continuum login` session file (0600) / non-interactive password |
| `CONTINUUM_TEST_PG` | [tests] when set, Go tests create a database per test on PostgreSQL |
| `CONTINUUM_WATCH` | `off` disables the fsnotify watcher (on by default; network filesystems and remote disks keep the periodic scan) |
| `CONTINUUM_SECRETS` / `CONTINUUM_SECRETS_DIR` | secret store `keychain` (default when the OS tool exists) \| `file` / file store location (default `<UserConfigDir>/Continuum/secrets-<controller>`) |
| `OTEL_EXPORTER_OTLP_ENDPOINT` / `OTEL_EXPORTER_OTLP_TRACES_ENDPOINT` / `OTEL_EXPORTER_OTLP_HEADERS` / `OTEL_SERVICE_NAME` / `OTEL_TRACES_SAMPLER_ARG` | OpenTelemetry traces (off without an endpoint; HTTP, port 4318). Read by the controller and the agent (agent default service.name `continuum-agent`); traceparent travels with every command |
| `CONTINUUM_VFS_FUSET` / `CONTINUUM_TEST_MACOS_MOUNT` | [risky] opt-in for the experimental macOS FUSE-T backend / macOS mount test — known to panic the macOS kernel; disposable machines only |
| `CONTINUUM_DIRECT` | `off` disables direct agent ↔ agent transfers (everything relays through the controller) |
| `CONTINUUM_AGENT_PEER_ADDR` / `CONTINUUM_AGENT_PEER_URL` | [agent] mTLS endpoint for direct transfers (default `:8444`, `off` disables) / URL other agents use (default `https://<hostname>:<port>`) |
| `CONTINUUM_TOTP` / `CONTINUUM_APPROVAL` | [CLI] default for `login --code` / `--approval` |
| `CONTINUUM_TEST_KEYCHAIN` | [tests] `1` runs the real OS keychain test (temporary service name, cleaned up) |
| `CONTINUUM_ACCESS_DIR` | where the SMB access point config (smb.conf, start.sh, secret 0600) is rendered (default `<UserConfigDir>/Continuum/access-<controller>`) |
| `CONTINUUM_CONTROLLER` / `CONTINUUM_FORMAT` | CLI default controller URL / output format |
| `VITE_CONTROLLER_URL` | controller called by the web dev server |

> `.env.*` files are git-ignored (only `.env.example` is committed). Never commit real secrets or signing keys.

## REST API (PRD §13.4 style, JSON + SSE)

| Area | Endpoints |
|---|---|
| Status | `GET /healthz` · `GET /readyz` · `GET /v1/info` · `GET /v1/overview` · `GET /v1/nodes` |
| Auth & users | `GET /v1/auth/status` (incl. `oidc`) · `POST /v1/auth/login {username,password,code}\|logout` · `GET /v1/auth/me` · `PUT /v1/auth/password` · `GET/POST /v1/users` · `PATCH/DELETE /v1/users/{id}` |
| MFA · SSO | `POST /v1/auth/mfa/setup\|enable\|disable {code}` · `GET /v1/auth/oidc/login?return=` → IdP → `GET /v1/auth/oidc/callback` → UI `#oidc_token=` |
| Two-person approval | `GET /v1/approvals[?state=]` · `GET /v1/approvals/{id}` · `POST /v1/approvals/{id}/approve\|reject` — retry the action with the `X-Approval-Id` header |
| VFS | `GET /v1/vfs` · `POST /v1/vfs/mount {volume,mountpoint,read_only}` · `POST /v1/vfs/unmount {mountpoint}` · `GET /v1/volumes/{v}/local-mount?mountpoint=` (mergerfs command, fstab, systemd) |
| Nodes (agents) | `POST /v1/nodes/tokens` · `POST /v1/nodes/{id}/revoke` · `GET /v1/nodes/{id}/inventory` · `POST /v1/disks {node_id}` · `POST /v1/disks/preflight {node_id}` · `POST /v1/disks/{id}/wipe {confirm,passes}` |
| Agent ↔ agent (`:8444`, mTLS) | `GET /peer/v1/file?path=&offset=` + `X-Continuum-Ticket` (signed by the controller CA) → body + trailer `X-Continuum-Blake3` |
| Agent endpoint (`:8443`, mTLS) | `POST /agent/v1/enroll` (token) · `POST /agent/v1/renew` · `/continuum.agent.v1.AgentService/Connect` (connect-go bidi) · `GET/PUT /agent/v1/blobs/{id}` |
| Disks | `GET /v1/disks[?volume=&unassigned=1]` · `GET /v1/disks/discover` · `POST /v1/disks/preflight` · `POST /v1/disks` (attach) · `GET/PATCH/DELETE /v1/disks/{id}` · `POST /v1/disks/{id}/detach\|enable` · `POST /v1/disks/refresh` |
| Volumes | `GET/POST /v1/volumes` · `GET/PATCH/DELETE /v1/volumes/{v}` · `PUT /v1/volumes/{v}/disks` (order, `expected_revision`) · `GET /v1/volumes/{v}/export` · `POST /v1/volumes/import` |
| Catalog | `GET /v1/volumes/{v}/files?path=&ordinal=` · `GET …/locate?path=` · `POST …/mkdir\|rename\|delete\|verify` · `GET …/trash` · `POST …/trash/purge` · `POST /v1/trash/{id}/restore` |
| Jobs | `POST /v1/plan` · `POST /v1/jobs` (202, `Idempotency-Key`) · `GET /v1/jobs[?state=]` · `GET /v1/jobs/{id}[/items]` · `GET /v1/jobs/{id}/errors?format=csv` · `POST /v1/jobs/{id}/pause\|resume\|cancel\|retry` · `GET /v1/jobs/{id}/watch` (SSE) · `GET /v1/events/stream` (SSE) |
| Activity | `GET /v1/activity` · `GET /v1/audit` · `GET /v1/audit/verify` |
| SCAN & conflicts | `POST /v1/volumes/{v}/scan {full,disk,fix_partials}` · `GET /v1/volumes/{v}/conflicts[?state=]` · `POST /v1/conflicts/{id}/resolve {keep: lowest\|newest\|both\|<disk>}` · `POST /v1/volumes/{v}/conflicts/resolve-duplicates` |
| Disk detail | `GET /v1/disks/{id}/smart[?refresh=1]` · `GET /v1/disks/{id}/smart/history` · `GET /v1/disks/{id}/usage` · `GET /v1/disks/{id}/dupes` · `GET/POST /v1/disks/{id}/bench` · `POST /v1/disks/{id}/reveal` · `GET /v1/volumes/{v}/usage\|dupes` |
| Drain & rebalance | `POST /v1/plan` · `POST /v1/jobs` (`type: DRAIN\|REBALANCE`) · `POST /v1/disks/{id}/drain` |
| SMB access point | `GET /v1/access` · `GET /v1/access/credentials` · `POST /v1/access/enable {bind,port}\|disable\|reload` |
| Operations | `GET /metrics` (Prometheus) · `GET /v1/diagnostics?mask=1` (zip) · `GET /v1/notifications` · `POST /v1/notifications/test` (webhook and email) · `GET/PUT /v1/settings` · `GET /v1/fs/browse?path=` (folder picker) |

Setting keys (`PUT /v1/settings`): `notify.webhook_url` · `notify.format` · `notify.smtp_host\|smtp_user\|smtp_password\|smtp_tls\|email_from\|email_to` · `security.approval_actions` (any of `disk.wipe,disk.drain,volume.delete,user.delete,node.revoke`) · `oidc.issuer\|client_id\|client_secret\|redirect_url\|default_role\|username_claim` · `ldap.url\|starttls\|bind_dn\|bind_password\|user_base\|user_filter\|group_attr\|role_map\|default_role\|ca_file`. Secrets (`notify.webhook_url`, `notify.smtp_password`, `oidc.client_secret`, `ldap.bind_password`, SMB password) live in the secret store, not the database; `smtp_password`, `client_secret` and `bind_password` read back as `********`.

Errors: `{"error":{"code":"revision_mismatch","message":"…","info":{…}}}` — HTTP 400 invalid · 401 unauthenticated (`info.mfa_required` when a code is needed) · 403 permission/approval_required (`info.approval_id`) · 404 not_found · 409 insufficient_space/offline/conflict/revision_mismatch · 422 verify_failed.
Security: localhost bind, non-allowed origins get 403, mutations accept `application/json` only (blocks form-POST CSRF, 415).

Real responses (`test/impl/6/logs/live-curl.log`):

```text
POST /v1/plan  → {'files': 3, 'dirs': 1, 'bytes': 600000, 'sufficient': True,
                  'placements': [{'ordinal': 1, 'name': 'd1', 'files': 3, 'bytes': 600000, …}], 'excluded': []}
PUT stale revision → {"error":{"code":"revision_mismatch","message":"volume was changed by another session (current revision 1) — reload","info":{"current_revision":1}}}
Origin evil.example → 403 · form POST → 415
```

SCAN and access point output (`test/impl/11/logs/live-cli.log`, `test/impl/15/logs/live.log`):

```text
$ continuum scan lab
incremental scan of lab: 2 disks, 3 files, 1 ms
added=1 removed=1 modified=1 corrupt=0 partials=1 (removed 0) conflicts=0
  ADDED     /data/새 폴더/memo.txt                            d1  added to catalog
  REMOVED   /data/sub/f1.bin                               d1  deleted outside Continuum
$ continuum access status
access point  running  127.0.0.1:4445  clients=0
lab           volume   rw              smb://127.0.0.1:4445/lab    1:d1=RW 2:d2=RW
```

## CLI

Commands: `version · node · disk (discover|list|inspect|preflight|attach|detach|enable|role|remove|smart|usage|dupes|bench|open) · volume (create|list|inspect|edit|delete|export|import|order|move|drain|rebalance) · plan (copy|move|drain|rebalance) · copy · move · mkdir · mv · rm · trash (list|restore|purge) · ls · locate · verify · scan · conflict (list|resolve|resolve-duplicates) · access (status|credentials|enable|disable|reload) · settings (get|set|test-notify) · diag bundle · node (list|token|revoke|inventory) · login [--code] · logout · whoami · user (list|add|set-role|disable|enable|passwd|delete|mfa setup|mfa enable|mfa disable) · approval (list|approve|reject) · vfs (mount|unmount|list) · access (local-mount|autoconnect) · disk wipe · job (list|watch|pause|resume|cancel|retry|errors) · activity (tail|verify) · completion`.

Agent: `continuum-agent enroll | run [--root …] [--controller --token] [--peer-listen :8444 --peer-url https://host:8444] | status | service (print|install|uninstall|status) | native-host (install|uninstall)`.

Global flag `--approval <id>`: re-run an action under two-person approval (wipe, drain, volume delete, user delete, node revoke) once approved. Copy, move, drain and rebalance accept `--window 22:00-06:00`.

Two-person approval and MFA, real output (`test/impl/29/logs/cli-security.log`):

```text
$ ctm volume delete pool --yes          # store1 (storage-admin)
error: volume.delete vol_01m3… needs a second person's approval (request apr_01m3…) — retry with it once approved
[exit 5]
$ ctm approval approve apr_01m3…        # sec1 (security-admin)
apr_01m3…	volume.delete vol_01m3…	approved
$ ctm volume delete pool --yes --approval apr_01m3…
deleted volume pool (files remain on the disks)
$ ctm login admin --code 765161
logged in as admin (system-admin) until 2026-10-01T15:06:59Z
```

Exit codes (FR-C06): 0 ok · 1 general/not_found · 2 usage (incl. missing `--yes` when non-interactive) · 3 insufficient space · 4 verify mismatch · 5 permission / login required · 6 offline/controller unreachable · 7 conflict · 8 revision mismatch · 130 interrupted.
Progress: progress bar on a TTY, one JSON line per second when piped. `disk discover --local` works without a controller.

Plan output (`test/impl/7/logs/live-cli.log`):

```text
Source
  Files:  300  Directories:  10  Logical size:  3.0 MB
Placement
  Volume:       research-pool  Start ordinal:  1  Policy:  topdir-affinity  Spillover:  next writable  Min free:  1.0 MB
Planned placement
  [1]  disk-01 (WisemanLimui-MacBookPro.local)  3.0 MB  300 files  remaining after: 646.4 GB above reserve
  [3]  mac-ext                                  —       (excluded: role=portable)
Result
  Sufficient capacity:  YES  Estimated duration:  0s  Warnings:  0  Existing paths:  0
```

## Tests

| Layer | Command | Scope |
|---|---|---|
| Go unit/integration | `make test` / `go test -race ./...` / `CONTINUUM_TEST_PG=… go test ./internal/...` | 17 packages (on SQLite and PostgreSQL). Direct agent ↔ agent transfers (pause, offset resume, relay fallback, 6 forged-ticket cases), LDAP (fake directory: groups → roles, injection, StartTLS, LDAPS with a private CA, lockout), VFS (Linux FUSE, in a container), fsnotify watcher, TOTP RFC vectors, MFA, two-person approval, OIDC (fake IdP; forged PKCE, signature, aud, nonce rejected), secret store, email (fake SMTP), Grafana metric coverage, OTLP export, Agent enrollment/rotation/revocation/offline/reconnect, cross-node copy/move/drain/auto-resume, RBAC matrix, job lease takeover, wipe, FR-P08 reproduction (24/6 GiB, real copy 24/6 MiB), fault injection (unplug, cancel, restart recovery), audit tamper detection, API security guards, CLI exit codes, SCAN/conflicts/drain/rebalance, smb.conf rendering, metrics/diagnostics/webhooks |
| Web unit | `pnpm --filter @continuum/web test` | FR-V10 formula, ordinal re-numbering, formatting |
| Browser E2E | `make e2e` | real Chrome/Chromium + real controller and agent, 13 web specs (+ MFA enrolment and code login, two-person approval) / 4 extension specs (T1, T2 local pool, T3 native host, T3 downgrade). The P3 set of 11 web specs: agent enrollment/remote attach/revoke, wipe, login & RBAC. The P2 set of 8 web specs: Create Volume DnD & copy, Conflict Center, disk detail, rebalance & drain, SMB access point (skipped without the Docker image), notifications/webhook/diagnostics, job detail & folder picker & ko/en / 1 extension spec |
| Dev environment | [test/dev-env/result.md](test/dev-env/result.md) | 12 cases |

Per-iteration verdicts and retry history: `test/impl/<N>/result.md`.

## Troubleshooting (issues actually hit)

| Symptom | Cause · fix |
|---|---|
| `pnpm install` fails with `ERR_PNPM_IGNORED_BUILDS: esbuild` | pnpm 11 blocks build scripts by default → `allowBuilds: { esbuild: true }` in `pnpm-workspace.yaml` (applied) |
| `password authentication failed for user "app"` | an old `pgdata` volume was initialised with another password → `make dev-stop` then `docker compose down -v` |
| Extension E2E cannot find the service worker | branded Chrome 137+ blocks `--load-extension` → `npx playwright install chromium` |
| `make preflight` warns about cargo | rustup was installed without touching PATH → `source $HOME/.cargo/env` |
| Many small files copy slowly (macOS ≈8 ms/file) | fsync of file + directory in the atomic copy (Go uses F_FULLFSYNC on darwin); integrity first by design |
| Volume warning "share free space" | several folders on the same physical volume were attached as disks, so free space is double-counted — use one mount root per disk outside tests |
| Attach fails on check 6 (foreign controller marker) | the disk belongs to another controller → use `volume import` (adopt) |
| `disk attach` takes seconds | `diskutil info` waits for a sleeping external HDD to spin up → OS metadata cached 30 s, diskutil skipped for network filesystems (applied) |
| SMB write `Permission denied` (Docker Desktop) | virtiofs shows bind mounts as root-owned → the access point creates the SMB user with the data root owner's uid (applied) |
| SMB rename `Operation not supported`, Finder tags not saved | Docker Desktop virtiofs is `no_xattr` → when xattrs are missing, fruit/streams are disabled and macOS uses AppleDouble (`._*`) files (applied; SCAN ignores `._*`). streams_depot was rejected: it creates many `lost-*` folders |
| A restarted agent container exits immediately | the container command ran `enroll` every time (tokens are single use) → use `continuum-agent run --controller … --token …`, which enrolls only when not enrolled (applied) |
| Jobs stall after the controller was SIGKILLed | the dead worker's lease (≤30 s) is still live → a 30 s background tick adopts jobs with expired leases (applied) |
| Header shows "1/2 agents online" | revoked nodes were counted → REVOKED nodes are excluded (applied) |
| Files copied through the VFS show size 0 in the catalog | FUSE RELEASE arrives asynchronously after close → the catalog is committed on FLUSH (applied) |
| `cp -a` prints "No data available" on the VFS | ACL xattr lookups → Setxattr answers ENOTSUP, Getxattr ENODATA (applied) |
| The mergerfs union shows `volume.json` | mergerfs cannot hide it → use the native VFS (`continuum vfs mount`), which hides internal files |
| **macOS reboots (kernel panic `nfs_vinvalbuf2: ubc_msync failed!, error 22`)** | FUSE-T (NFS loopback) mounts panicked the macOS NFS client (4 times on macOS 26.6.2) → the FUSE-T backend refuses to mount without `CONTINUUM_VFS_FUSET=i-accept-kernel-panic-risk`, and the test skips without `CONTINUUM_TEST_MACOS_MOUNT=1`. Use the SMB access point on macOS |
| macOS `vfs mount` hangs | go-fuse waits forever when `mount_macfuse` fails before handing over the fd → a guidance error after 15 s (applied). `mount_macfuse: the file system is not available` = macFUSE system extension not approved → allow it in System Settings and restart |
| Direct transfers fall back to relaying (`relaying through the controller` in the log) | the source agent has no peer endpoint (old agent, `--peer-listen off`) or is unreachable → set `--peer-url` to an address other agents can reach |
| LDAP login 409 "StartTLS" | `ldap://` requires StartTLS by default → enable TLS on the directory, use `ldaps://`, or `ldap.starttls=false` for a trusted local directory only. Private CA: `ldap.ca_file` |
| `helm lint` passes without the DB Secret | lint does not evaluate `fail` → `helm template` fails with `database.existingSecret is required` (intended) |
| An action under approval keeps returning 403 | an approval is single-use and bound to requester, action and target → the same user retries with `--approval <id>` (the UI attaches it automatically) |
| Windows cannot connect to `\\host\share` | Windows always uses port 445 → `continuum access enable --bind 0.0.0.0 --port 445` (port 445 must be free on the host) |

## Directory layout

```
continuum/
├── cmd/{controller,continuum,migrate,agent}/
├── internal/
│   ├── api/        # REST + SSE, CORS/token/JSON guard
│   ├── core/       # node, disk, volume and catalog services, error codes
│   ├── transfer/   # job engine, atomic copy, move/rename/delete/trash/verify
│   ├── placement/  # pure placement engine (FR-P01~P11)
│   ├── disk/       # OS discovery, 10 preflight checks, marker
│   ├── store/      # DB open, embedded migrations, ULID
│   ├── audit/      # hash-chained audit + events
│   ├── agent/      # agent runtime, OS service, native host
│   ├── hub/        # agent hub (enrollment, mTLS stream, blob relay, remote FS)
│   ├── pki/ agentproto/ nodefs/ auth/ secrets/
│   ├── vfs/        # Linux FUSE native mount (stub on Windows)
│   ├── watch/      # fsnotify watcher
│   ├── tracing/    # OTLP/HTTP JSON traces
│   ├── access/     # SMB access point (Samba + mergerfs container, smb.conf rendering)
│   ├── ops/        # /metrics, diagnostics bundle, webhook and email notifications
│   ├── cli/        # continuum CLI
│   └── version/
├── apps/{web,desktop,extension}/
├── deploy/samba/Dockerfile                     # continuum-samba:local
├── deploy/grafana/continuum-dashboard.json     # Grafana import
├── deploy/helm/continuum/                      # Helm chart (make deploy)
├── deploy/release/ · deploy/extension-policy/  # release images · extension enterprise policies
├── tools/notices/ · scripts/                    # license notices · reproducible build / release lint
├── docs/optional-dependencies.md · THIRD_PARTY_NOTICES.md
├── test/{dev-env,impl/1..34,impl/regression,impl/regression-p2..p5}/
├── .github/workflows/ci.yml · .goreleaser.yaml · release-please-config.json
├── docker-compose.yml · Dockerfile · Makefile · Procfile.dev
└── go.mod · package.json · pnpm-workspace.yaml · STACK.md
```

## Next steps

- **E31 BLOCKED**: macOS VFS — the FUSE-T path passed the functional test but panics the macOS kernel, so it is disabled by default. Re-verify on a **disposable test machine** after approving macFUSE (or macFUSE ≥ 5.4 FSKit): `CONTINUUM_TEST_MACOS_MOUNT=1 go test ./internal/vfs`
- VFS: Windows WinFsp (cgofuse, cgo)
- Auth: verification against real IdPs and AD/OpenLDAP (the protocols are covered by a fake IdP and a fake directory)
- Release: run `goreleaser check` before the first tag, register signing certificates and store accounts (`release` environment), choose the project LICENSE
- Observability: OTLP gRPC (4317)
- Deployment: verification on a real cluster and the first real `docker build` in CI
