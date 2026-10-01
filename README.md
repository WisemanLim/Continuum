# Continuum

순번 기반 크로스플랫폼 디스크 풀링·디스크 관리 플랫폼 — *Storage without boundaries. 디스크의 경계 없이 이어지는 스토리지.*

여러 디스크(내장·USB·폴더)를 **사용자가 정한 순번**으로 하나의 볼륨에 엮고, 1번부터 채워 넘치면 다음 순번으로 흘려 보냅니다(spillover). 파일은 쪼개지 않고 디스크마다 온전히 저장되므로, 디스크를 떼어 Finder/Explorer 로 열어도 그대로 읽힙니다.

> **스캐폴딩 정보**: `wise-dev-std` 플러그인 / 프로파일 `custom` (base `go-gin`).
> 요구사항: [.docs/Continuum_PRD.pdf](.docs/Continuum_PRD.pdf) · 화면설계: [.docs/Continuum_UI_Design.pdf](.docs/Continuum_UI_Design.pdf) · 스택 결정: [STACK.md](STACK.md)

## 구현 현황 (P1 MVP + P2 + P3 + P4 + P5 + P6 — PRD §18.2)

| # | Epic | 상태 | 시험 |
|---|---|---|---|
| E1 | 카탈로그·레지스트리 (SQLite/PostgreSQL 공용 스키마, 해시 체인 감사) | DONE | [impl/1](test/impl/1/result.md) |
| E2 | 디스크 탐색·Attach (OS별 식별, 사전 검사 10항목, 디스크 마커) | DONE | [impl/2](test/impl/2/result.md) |
| E3 | 볼륨 관리 (순번·revision 잠금·요약 계산식·Export/Import) | DONE | [impl/3](test/impl/3/result.md) |
| E4 | 배치 엔진·Plan (5 정책, 강제/시작 순번, **FR-P08 24/6 GB 재현**) | DONE | [impl/4](test/impl/4/result.md) |
| E5 | 전송 엔진 (원자적 복사·BLAKE3·재개·일시정지·취소·휴지통·이동·이름변경) | DONE | [impl/5](test/impl/5/result.md) |
| E6 | Controller REST API + SSE | DONE | [impl/6](test/impl/6/result.md) |
| E7 | CLI `continuum` / `ctm` | DONE | [impl/7](test/impl/7/result.md) |
| E8 | Web UI (Create Volume 3열 DnD 등) | DONE | [impl/8](test/impl/8/result.md) |
| E9 | 데스크톱 셸(Tauri) + Chrome 확장 T1 | DONE | [impl/9](test/impl/9/result.md) |
| E10 | CI·릴리즈 기초 (FR-P08 게이트, goreleaser) | DONE | [impl/10](test/impl/10/result.md) |
| — | P1 전체 회귀 | PASS | [regression](test/impl/regression/result.md) |
| E11 | SCAN·직접 편집 보정 (고아 자동 추가/검토, 외부 삭제 tombstone, 변경·손상 탐지, partial 정리, 주기 스캔) | DONE | [impl/11](test/impl/11/result.md) |
| E12 | Conflict Center (duplicate/conflict 분류, keep lowest·newest·both·디스크, 중복 일괄 해결) | DONE | [impl/12](test/impl/12/result.md) |
| E13 | 디스크 상세 (SMART, 벤치마크, 사용량 트리맵, 중복 파일 탐색, Finder/Explorer 열기) | DONE | [impl/13](test/impl/13/result.md) |
| E14 | Drain·Rebalance (복사→검증→삭제, 완료 시 볼륨에서 제거, 계획 미리보기) | DONE | [impl/14](test/impl/14/result.md) |
| E15 | SMB 접근점 (Samba + mergerfs 컨테이너, 볼륨·디스크별 share, 접근 모드 3종) | DONE | [impl/15](test/impl/15/result.md) |
| E16 | 운영·관측 (`/metrics`, 진단 번들, 웹훅 알림, 알림 센터) | DONE | [impl/16](test/impl/16/result.md) |
| E17 | UI·i18n 마감 (ko/en 완전화, 작업 상세, 폴더 선택기) | DONE | [impl/17](test/impl/17/result.md) |
| — | P2 전체 회귀 (`make test`·race·`make e2e` web 8 + 확장 1) | PASS | [regression-p2](test/impl/regression-p2/result.md) |
| E18 | Agent 등록·mTLS (자체 CA, 1회용 토큰 + CA 지문 고정, 인증서 회전·폐기) | DONE | [impl/18](test/impl/18/result.md) |
| E19 | Agent 스트림·원격 디스크 (connect-go 양방향, heartbeat·inventory, 3회 누락 → OFFLINE, 재연결) | DONE | [impl/19](test/impl/19/result.md) |
| E20 | Cross-node 전송 (원격 복사·이동·Drain·SCAN·검증, 단절 시 PAUSED → 복귀 자동 재개) | DONE | [impl/20](test/impl/20/result.md) |
| E21 | OS 서비스 설치 (launchd · systemd · Windows SCM) | DONE | [impl/21](test/impl/21/result.md) |
| E22 | 인증·RBAC 5역할 (PBKDF2, 세션, 권한 매트릭스, 잠금, 감사 행위자) | DONE | [impl/22](test/impl/22/result.md) |
| E23 | 중앙 서버 모드 (PostgreSQL 전체 스위트, 작업 lease, compose Agent) | DONE | [impl/23](test/impl/23/result.md) |
| E24 | 확장 T3 네이티브 브리지 (native messaging, 자동 강등) | DONE | [impl/24](test/impl/24/result.md) |
| E25 | 보안 삭제 Wipe (덮어쓰기 + 삭제, 서버 측 이름 확인) | DONE | [impl/25](test/impl/25/result.md) |
| — | P3 전체 회귀 (`make test`·race·PostgreSQL·`make e2e` web 11 + 확장 3) | PASS | [regression-p3](test/impl/regression-p3/result.md) |
| E26 | 네이티브 VFS · Linux FUSE (go-fuse: 읽기 최저 순번, 쓰기 배치 정책·spillover, 휴지통, 읽기 전용) | DONE | [impl/26](test/impl/26/result.md) |
| E27 | 확장 T2 브라우저 로컬 풀 (File System Access API, IndexedDB 핸들, SHA-256 검증) | DONE | [impl/27](test/impl/27/result.md) |
| E28 | P2 잔여 (fsnotify 감시자, macOS SMB 자동 재연결, Linux 로컬 mergerfs, SMART 온도 추이, 작업 시간창) | DONE | [impl/28](test/impl/28/result.md) |
| E29 | 보안 잔여 (TOTP MFA, 2인 승인, OIDC SSO, OS 키체인 비밀 저장) | DONE | [impl/29](test/impl/29/result.md) |
| E30 | 운영 잔여 (이메일 알림, Grafana 대시보드, Helm chart, OpenTelemetry 트레이스) | DONE | [impl/30](test/impl/30/result.md) |
| — | P4 전체 회귀 | PASS | [regression-p4](test/impl/regression-p4/result.md) |
| E31 | 네이티브 VFS · macOS (macFUSE 경로 + 실험적 FUSE-T 경로) | **BLOCKED** (macFUSE 확장 미승인 · FUSE-T 는 macOS 커널 패닉 → 기본 차단) | [impl/31](test/impl/31/result.md) |
| E32 | Agent ↔ Agent 직접 전송 (mTLS pull + CA 서명 티켓, 오프셋 재개, BLAKE3 trailer, 중계 폴백) | DONE | [impl/32](test/impl/32/result.md) |
| E33 | LDAP / Active Directory 로그인 (그룹 → 역할, StartTLS·LDAPS·사설 CA) | DONE | [impl/33](test/impl/33/result.md) |
| E34 | 공급망·릴리즈 (재현 빌드, cosign·공증·Authenticode·GPG, SBOM·출처 증명, 스토어·기업 배포 정책) | DONE (정적 검증) | [impl/34](test/impl/34/result.md) |
| — | P5 전체 회귀 | PASS | [regression-p5](test/impl/regression-p5/result.md) |
| E35 | 직접 전송 파일 내부 진행률 (Agent `Progress` 스트림 → 작업 진행률·SSE) | DONE | [impl/35](test/impl/35/result.md) |
| E36 | Agent 측 OTel 트레이스 (명령 traceparent, Controller ↔ Agent 한 트레이스) | DONE | [impl/36](test/impl/36/result.md) |
| E37 | Controller 이미지에 웹 UI 내장 (nonroot, `/web`, 릴리즈 이미지) | DONE (정적·시뮬레이션) | [impl/37](test/impl/37/result.md) |
| — | P6 전체 회귀 | PASS | [regression-p6](test/impl/regression-p6/result.md) |

미구현: Windows WinFsp VFS · macOS 네이티브 VFS(E31 BLOCKED, macOS 는 SMB 접근점) · 실제 서명·공증·스토어 업로드(인증서·계정 필요) · OTLP gRPC.

## 구성

| 구성요소 | 위치 | 설명 |
|---|---|---|
| Controller (+내장 Agent, M1) | `cmd/controller`, `internal/{api,core,transfer,placement,disk,store,audit}` | REST/SSE API, 레지스트리·카탈로그, 배치·전송·SCAN·Drain/Rebalance 엔진 |
| Agent | `cmd/agent`, `internal/agent` | 원격 노드 데몬: 등록·mTLS 스트림·디스크 명령 실행·OS 서비스·Chrome native host |
| Agent 허브 | `internal/hub`, `internal/pki`, `internal/agentproto`, `internal/nodefs` | 등록 토큰·CA·connect-go 스트림·blob 중계·노드별 파일시스템(로컬/원격) |
| 인증·RBAC | `internal/auth` | 사용자·세션·5역할 권한 매트릭스, TOTP MFA, 2인 승인, OIDC SSO, LDAP/AD |
| 비밀 저장 | `internal/secrets` | macOS Keychain · Linux Secret Service · Windows Credential Locker, 없으면 0600 파일 |
| 네이티브 VFS | `internal/vfs` | Linux FUSE / macOS macFUSE 마운트(볼륨 = 하나의 파일시스템), Controller 프로세스 안에서 동작 |
| 직접 전송 | `internal/agent/peer.go`, `internal/hub` (`Pull`), `internal/pki/ticket.go` | Agent ↔ Agent mTLS pull, Controller CA 서명 티켓, 재개 오프셋 |
| 공급망 | `.goreleaser.yaml`, `.github/workflows/release.yml`, `scripts/{repro-check.sh,release-lint.py}`, `tools/notices`, `deploy/{release,extension-policy}` | 재현 빌드·서명·SBOM·출처 증명·스토어/기업 배포 |
| FS 감시 | `internal/watch` | fsnotify 로 로컬 디스크 직접 편집을 수 초 안에 카탈로그 반영 |
| 트레이싱 | `internal/tracing` | OpenTelemetry OTLP/HTTP JSON 내보내기 (SDK 의존성 없음) |
| SMB 접근점 | `internal/access`, `deploy/samba/Dockerfile` | Controller 가 Samba + mergerfs 컨테이너(`continuum-smb`)를 설정·기동 |
| 운영 | `internal/ops`, `deploy/grafana`, `deploy/helm/continuum` | Prometheus `/metrics`, 진단 번들, 웹훅·이메일 알림, Grafana 대시보드, Helm chart |
| CLI | `cmd/continuum` (`ctm` = 심볼릭 링크) | 같은 REST API 호출 |
| 마이그레이션 | `cmd/migrate`, `internal/store/migrations/*.sql` | embed, SQLite·PostgreSQL 공용 |
| Web UI | `apps/web` | Vite + React + TS, dnd-kit, 디자인 토큰(라이트/다크) |
| 데스크톱 | `apps/desktop` | Tauri v2 셸 + Controller 사이드카 |
| Chrome 확장 | `apps/extension` | wxt MV3: 사이드 패널(T1) + 전체 콘솔(web UI 재사용) + 브라우저 로컬 풀(T2) + native host(T3) |

## 기술 스택

| 구분 | 도구 |
|------|------|
| Controller · CLI · Agent | Go 1.26 (cgo 없음), `net/http` REST + SSE, cobra, connect-go(JSON 코덱, Agent 스트림) |
| DB | SQLite(modernc, WAL, local/M1) · PostgreSQL 17(pgx, dev/staging/prod) |
| 해시·ID | BLAKE3(zeebo/blake3) · ULID |
| 디스크 탐색 | gopsutil + diskutil(macOS) / lsblk(Linux) / Get-Volume(Windows) |
| Web | React 19 · Vite 7 · TypeScript 5.9 · @dnd-kit · Vitest · Playwright |
| 데스크톱 | Tauri 2.12 + tauri-plugin-shell (사이드카) |
| 확장 | wxt 0.21 (MV3) |
| 패키지 | go modules · pnpm 11 workspaces · cargo |
| 로컬 프로세스 | goreman (`Procfile.dev`) |
| SMB 접근점 | Samba 4.17 + mergerfs 2.33 (debian bookworm 컨테이너, Docker) |
| VFS · 감시 | hanwen/go-fuse v2.11 (Linux FUSE) · fsnotify v1.10 |
| 비밀 저장 | OS 명령 래핑: `security`(macOS) · `secret-tool`(Linux) · PowerShell PasswordVault(Windows) |
| 인증 확장 | TOTP(RFC 6238, 표준 라이브러리) · OIDC Authorization Code + PKCE (RS256 JWKS 직접 검증) · go-ldap v3.4 (LDAP/AD) |
| 릴리즈 | goreleaser v2(재현 빌드, cosign keyless, notarytool, jsign/Azure Trusted Signing, syft SBOM, ghcr 이미지) · attest-build-provenance · tauri-action · wxt submit · dependabot · govulncheck |
| 관측 | Prometheus 텍스트 · OTLP/HTTP JSON 트레이스 · Grafana 대시보드 JSON |
| 배포 | Helm 3 chart (`deploy/helm/continuum`) |
| 릴리즈 | goreleaser v2 · release-please · GitHub Actions |

## 사전 요구사항

- Go 1.26+, Node 22+, pnpm (`corepack enable`), GNU Make, Docker Desktop(dev 스택·SMB 접근점 — 없으면 접근점만 비활성)
- goreman: `go install github.com/mattn/goreman@latest` (`$(go env GOPATH)/bin` 을 PATH 에)
- 데스크톱 빌드: Rust stable (`rustup`, `~/.cargo/bin` 을 PATH 에)
- E2E: Google Chrome(웹) · `npx playwright install chromium`(확장 — 정식 Chrome 은 `--load-extension` 차단)

## 빠른 시작

```bash
make preflight            # 도구 점검
make local-build          # go build + pnpm install (+ docker 있으면 continuum-samba:local 이미지)
make db-migrate           # migrate up: applied [0001_init 0002_settings 0003_p2] (sqlite)
make local-all            # controller(127.0.0.1:8080) + web dev server(:5173) 백그라운드
open http://localhost:5173
make local-stop
```

CLI (같은 Controller 사용):

```bash
go build -o bin/continuum ./cmd/continuum && ln -sf continuum bin/ctm
bin/continuum disk attach /Volumes/ExternalSSD --role portable
bin/continuum volume create research-pool --disks disk-01,win-d,mac-ext --policy topdir --min-free 100GiB
bin/continuum plan copy ~/Downloads/research-data research-pool:/research/2026-q3
bin/continuum copy ~/Downloads/research-data research-pool:/research/2026-q3 --yes
bin/continuum scan research-pool                       # Finder 등으로 직접 넣은 파일을 카탈로그에 반영
bin/continuum volume drain research-pool mac-ext -y   # 디스크 비우고 볼륨에서 제거
bin/continuum access enable                            # smb://127.0.0.1:4445/research-pool
```

원격 노드(Agent, mTLS — Controller 의 `CONTINUUM_AGENT_ADDR` 에서 대기):

```bash
bin/continuum node token --label nas-1                 # 1회용 토큰 + 실행할 명령 출력
# 새 노드에서:
continuum-agent run --controller https://controller:8443 --token CTM1.… --root /srv/disk1   # 첫 기동만 등록
continuum-agent service install --root /srv/disk1      # launchd / systemd / Windows 서비스
bin/continuum disk attach /srv/disk1 --node <node_id> --volume research-pool
```

사용자·역할(첫 사용자 생성 순간부터 로그인 필요):

```bash
bin/continuum user add admin --role system-admin && bin/continuum login admin
bin/continuum user add ops --role operator
```

데스크톱·확장:

```bash
pnpm --filter @continuum/desktop dev                  # 사이드카 빌드 + Tauri 개발 창
pnpm --filter @continuum/desktop build --bundles app  # Continuum.app (Controller 사이드카 포함)
pnpm --filter @continuum/extension build              # .output/chrome-mv3 → chrome://extensions 에서 "압축해제된 확장 로드"
```

## Make 사용법

| 명령 | 설명 |
|------|------|
| `make preflight` | Go 1.26+·Node 22+ 필수, pnpm·docker·goreman·cargo 점검 |
| `make test` | `go test ./...` + `pnpm -r --if-present test` (vitest) |
| `make e2e` | web·확장(+T3 시험 빌드) 빌드 후 Playwright E2E (실제 Controller·Agent·native host) |
| `make release-check` | 공급망 정적 검증: 재현 빌드(2회 빌드 SHA-256 비교) · THIRD_PARTY_NOTICES 생성 · 릴리즈 설정 lint |
| `make deploy` | Controller Helm 배포 (`deploy/helm/continuum`, `database.existingSecret` 필요) |
| `make help` | 타겟 목록 |
| `make local-build` | `go mod download && go build ./...` + `pnpm install` + (docker 가능 시) `docker build -t continuum-samba:local deploy/samba` |
| `make local-all` | goreman 백그라운드(`-env .env.local`). `LOCAL_INFRA=postgres` 면 docker postgres 도 기동 |
| `make local-logs` / `local-stop` / `local-restart` / `local-ps` | 로그 / 종료 / 재기동 / 상태 |
| `make <env>-all` | [dev\|staging\|prod] postgres + controller 컨테이너 (`--profile agents` 로 compose Agent 추가) |
| `make <env>-build` | [dev\|staging] 이미지 재빌드 후 기동 (prod 미제공) |
| `make <env>-logs` / `-stop` / `-restart` / `-ps` | 컨테이너 로그(`SVC=`) / 정리 / 재기동 / 상태 |
| `make db-migrate [ENV=]` | 내장 마이그레이션 적용 (멱등) |
| `make db-seed [ENV=]` | 미구현 (`SEED=` 로 교체) |
| `make db-reset [ENV=]` | local=SQLite 삭제 · 그 외=schema 재생성 · prod 거부 |
| `make db-fresh [ENV=]` | reset + seed |

## 환경 변수

| 파일 | 용도 |
|------|------|
| `.env.example` | 커밋용 템플릿 (placeholder) — `cp .env.example .env.<env>` |
| `.env.local` | M1 단일 PC, SQLite |
| `.env.dev` | M2 공유 개발, compose postgres (placeholder 비밀번호) |
| `.env.staging` | prod 동등 검증 (시크릿 주입) |
| `.env.prod` | 키 목록만 — CI/Vault 주입 |

| 변수 | 설명 |
|------|------|
| `ENV` | local/dev/staging/prod |
| `BIND_HOST` / `API_PORT` / `POSTGRES_PORT` | compose 포트 바인드 IP·포트 (기본 `127.0.0.1` / `8080` / `5432`) |
| `CONTROLLER_ADDR` | Controller listen 주소 (local `127.0.0.1:8080`, 컨테이너 `:8080`) |
| `CONTROLLER_URL` | Agent·CLI 가 붙을 URL |
| `DATABASE_URL` | `sqlite:///./local.db` 또는 `postgresql://…` (SQLite 는 로컬 디스크만, NFR-A04) |
| `POSTGRES_USER` / `POSTGRES_PASSWORD` / `POSTGRES_DB` | compose postgres |
| `CONTINUUM_TOKEN` | 설정 시 `/v1/*` 에 `Authorization: Bearer` 필요 (FR-S02 로컬 토큰) |
| `CONTINUUM_ALLOWED_ORIGINS` | CORS 허용 Origin (기본: `localhost:5173`, `tauri://localhost`, `chrome-extension://*`). 같은 origin 은 항상 허용 |
| `CONTINUUM_WEB_DIR` | 지정 시 Controller 가 `apps/web/dist` 를 `/` 로 서빙 |
| `CONTINUUM_AGENT_ADDR` | Agent mTLS 엔드포인트 (기본 `127.0.0.1:8443`, `off` = 단일 머신) |
| `CONTINUUM_AGENT_URL` / `CONTINUUM_AGENT_SANS` | 토큰 명령에 넣을 공개 URL / 서버 인증서 추가 SAN |
| `CONTINUUM_PKI_DIR` | CA 인증서·키(0600) 위치 (기본 `<UserConfigDir>/Continuum/pki-<controller>`) |
| `CONTINUUM_AGENT_HEARTBEAT_SEC` | Agent heartbeat 주기(기본 10, 3회 누락 → OFFLINE) |
| `AGENT_PORT` | compose 가 게시하는 Agent 포트 (기본 8443) |
| `CONTINUUM_AGENT_DIR` / `CONTINUUM_CONTROLLER_AGENT_URL` / `CONTINUUM_ENROLL_TOKEN` | [Agent] 상태 디렉터리 / Controller Agent URL / 첫 기동 토큰 |
| `CONTINUUM_SESSION_FILE` / `CONTINUUM_PASSWORD` | [CLI] `continuum login` 세션 파일(0600) / 비대화형 비밀번호 |
| `CONTINUUM_TEST_PG` | [테스트] 설정 시 Go 테스트가 PostgreSQL 에서 테스트별 DB 생성 |
| `CONTINUUM_WATCH` | `off` = fsnotify 감시자 끔 (기본 켬, 네트워크 FS·원격 디스크는 주기 스캔) |
| `CONTINUUM_SECRETS` / `CONTINUUM_SECRETS_DIR` | 비밀 저장소 `keychain`(기본, OS 도구 있으면) \| `file` / 파일 저장소 위치(기본 `<UserConfigDir>/Continuum/secrets-<controller>`) |
| `OTEL_EXPORTER_OTLP_ENDPOINT` / `OTEL_EXPORTER_OTLP_TRACES_ENDPOINT` / `OTEL_EXPORTER_OTLP_HEADERS` / `OTEL_SERVICE_NAME` / `OTEL_TRACES_SAMPLER_ARG` | OpenTelemetry 트레이스 (endpoint 없으면 끔, 4318 HTTP). Controller·Agent 모두 읽음(Agent 기본 service.name `continuum-agent`), 명령마다 traceparent 전파 |
| `CONTINUUM_VFS_FUSET` / `CONTINUUM_TEST_MACOS_MOUNT` | [위험] macOS FUSE-T 실험 백엔드·macOS 마운트 시험 opt-in — macOS 커널 패닉 사례가 있어 일회용 머신에서만 |
| `CONTINUUM_DIRECT` | `off` = Agent ↔ Agent 직접 전송 끔(모두 Controller 중계) |
| `CONTINUUM_AGENT_PEER_ADDR` / `CONTINUUM_AGENT_PEER_URL` | [Agent] 직접 전송 mTLS 엔드포인트(기본 `:8444`, `off` 비활성) / 다른 Agent 가 접속할 URL(기본 `https://<hostname>:<port>`) |
| `CONTINUUM_TOTP` / `CONTINUUM_APPROVAL` | [CLI] `login --code` 기본값 / `--approval` 기본값 |
| `CONTINUUM_TEST_KEYCHAIN` | [테스트] `1` 이면 실제 OS 키체인 시험(임시 서비스명, 정리됨) |
| `CONTINUUM_ACCESS_DIR` | SMB 접근점 설정(smb.conf·start.sh·secret 0600) 생성 위치 (기본 `<UserConfigDir>/Continuum/access-<controller>`) |
| `CONTINUUM_CONTROLLER` / `CONTINUUM_FORMAT` | CLI 기본 Controller URL / 출력 형식 |
| `VITE_CONTROLLER_URL` | web dev server 가 호출할 Controller |

> `.env.*` 는 `.gitignore` 대상(`.env.example` 만 커밋). 실제 시크릿·서명키 커밋 금지.

## REST API (PRD §13.4 형태, JSON + SSE)

| 영역 | 엔드포인트 |
|---|---|
| 상태 | `GET /healthz` · `GET /readyz` · `GET /v1/info` · `GET /v1/overview` · `GET /v1/nodes` |
| 인증·사용자 | `GET /v1/auth/status` (`oidc` 포함) · `POST /v1/auth/login {username,password,code}\|logout` · `GET /v1/auth/me` · `PUT /v1/auth/password` · `GET/POST /v1/users` · `PATCH/DELETE /v1/users/{id}` |
| MFA·SSO | `POST /v1/auth/mfa/setup\|enable\|disable {code}` · `GET /v1/auth/oidc/login?return=` → IdP → `GET /v1/auth/oidc/callback` → UI `#oidc_token=` |
| 2인 승인 | `GET /v1/approvals[?state=]` · `GET /v1/approvals/{id}` · `POST /v1/approvals/{id}/approve\|reject` — 대상 작업 재시도 시 `X-Approval-Id` 헤더 |
| VFS | `GET /v1/vfs` · `POST /v1/vfs/mount {volume,mountpoint,read_only}` · `POST /v1/vfs/unmount {mountpoint}` · `GET /v1/volumes/{v}/local-mount?mountpoint=` (mergerfs 명령·fstab·systemd) |
| 노드(Agent) | `POST /v1/nodes/tokens` · `POST /v1/nodes/{id}/revoke` · `GET /v1/nodes/{id}/inventory` · `POST /v1/disks {node_id}` · `POST /v1/disks/preflight {node_id}` · `POST /v1/disks/{id}/wipe {confirm,passes}` |
| Agent ↔ Agent (`:8444`, mTLS) | `GET /peer/v1/file?path=&offset=` + `X-Continuum-Ticket` (Controller CA 서명) → 본문 + trailer `X-Continuum-Blake3` |
| Agent 엔드포인트 (`:8443`, mTLS) | `POST /agent/v1/enroll` (토큰) · `POST /agent/v1/renew` · `/continuum.agent.v1.AgentService/Connect` (connect-go 양방향) · `GET/PUT /agent/v1/blobs/{id}` |
| 디스크 | `GET /v1/disks[?volume=&unassigned=1]` · `GET /v1/disks/discover` · `POST /v1/disks/preflight` · `POST /v1/disks` (attach) · `GET/PATCH/DELETE /v1/disks/{id}` · `POST /v1/disks/{id}/detach\|enable` · `POST /v1/disks/refresh` |
| 볼륨 | `GET/POST /v1/volumes` · `GET/PATCH/DELETE /v1/volumes/{v}` · `PUT /v1/volumes/{v}/disks` (순번, `expected_revision`) · `GET /v1/volumes/{v}/export` · `POST /v1/volumes/import` |
| 카탈로그 | `GET /v1/volumes/{v}/files?path=&ordinal=` · `GET …/locate?path=` · `POST …/mkdir\|rename\|delete\|verify` · `GET …/trash` · `POST …/trash/purge` · `POST /v1/trash/{id}/restore` |
| 작업 | `POST /v1/plan` · `POST /v1/jobs` (202, `Idempotency-Key`) · `GET /v1/jobs[?state=]` · `GET /v1/jobs/{id}[/items]` · `GET /v1/jobs/{id}/errors?format=csv` · `POST /v1/jobs/{id}/pause\|resume\|cancel\|retry` · `GET /v1/jobs/{id}/watch` (SSE) · `GET /v1/events/stream` (SSE) |
| 활동 | `GET /v1/activity` · `GET /v1/audit` · `GET /v1/audit/verify` |
| SCAN·충돌 | `POST /v1/volumes/{v}/scan {full,disk,fix_partials}` · `GET /v1/volumes/{v}/conflicts[?state=]` · `POST /v1/conflicts/{id}/resolve {keep: lowest\|newest\|both\|<disk>}` · `POST /v1/volumes/{v}/conflicts/resolve-duplicates` |
| 디스크 상세 | `GET /v1/disks/{id}/smart[?refresh=1]` · `GET /v1/disks/{id}/smart/history` · `GET /v1/disks/{id}/usage` · `GET /v1/disks/{id}/dupes` · `GET/POST /v1/disks/{id}/bench` · `POST /v1/disks/{id}/reveal` · `GET /v1/volumes/{v}/usage\|dupes` |
| Drain·Rebalance | `POST /v1/plan` · `POST /v1/jobs` (`type: DRAIN\|REBALANCE`) · `POST /v1/disks/{id}/drain` |
| SMB 접근점 | `GET /v1/access` · `GET /v1/access/credentials` · `POST /v1/access/enable {bind,port}\|disable\|reload` |
| 운영 | `GET /metrics` (Prometheus) · `GET /v1/diagnostics?mask=1` (zip) · `GET /v1/notifications` · `POST /v1/notifications/test` (webhook·이메일) · `GET/PUT /v1/settings` · `GET /v1/fs/browse?path=` (폴더 선택기) |

설정 키(`PUT /v1/settings`): `notify.webhook_url` · `notify.format` · `notify.smtp_host\|smtp_user\|smtp_password\|smtp_tls\|email_from\|email_to` · `security.approval_actions` (`disk.wipe,disk.drain,volume.delete,user.delete,node.revoke` 중) · `oidc.issuer\|client_id\|client_secret\|redirect_url\|default_role\|username_claim` · `ldap.url\|starttls\|bind_dn\|bind_password\|user_base\|user_filter\|group_attr\|role_map\|default_role\|ca_file`. 비밀(`notify.webhook_url`·`notify.smtp_password`·`oidc.client_secret`·`ldap.bind_password`·SMB 비밀번호)은 DB 가 아닌 비밀 저장소에 저장, `smtp_password`·`client_secret`·`bind_password` 는 응답에서 `********`.

오류 형식: `{"error":{"code":"revision_mismatch","message":"…","info":{…}}}` — HTTP 400 invalid · 401 unauthenticated(MFA 필요 시 `info.mfa_required`) · 403 permission/approval_required(`info.approval_id`) · 404 not_found · 409 insufficient_space/offline/conflict/revision_mismatch · 422 verify_failed.
보안: localhost 바인드, 허용 Origin 외 403, 변경 요청은 `application/json` 만(폼 POST CSRF 차단, 415).

실제 응답 예 (`test/impl/6/logs/live-curl.log`):

```text
POST /v1/plan  → {'files': 3, 'dirs': 1, 'bytes': 600000, 'sufficient': True,
                  'placements': [{'ordinal': 1, 'name': 'd1', 'files': 3, 'bytes': 600000, …}], 'excluded': []}
PUT stale revision → {"error":{"code":"revision_mismatch","message":"volume was changed by another session (current revision 1) — reload","info":{"current_revision":1}}}
Origin evil.example → 403 · form POST → 415
```

SCAN · 접근점 실제 출력 (`test/impl/11/logs/live-cli.log`, `test/impl/15/logs/live.log`):

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

명령: `version · node · disk (discover|list|inspect|preflight|attach|detach|enable|role|remove|smart|usage|dupes|bench|open) · volume (create|list|inspect|edit|delete|export|import|order|move|drain|rebalance) · plan (copy|move|drain|rebalance) · copy · move · mkdir · mv · rm · trash (list|restore|purge) · ls · locate · verify · scan · conflict (list|resolve|resolve-duplicates) · access (status|credentials|enable|disable|reload) · settings (get|set|test-notify) · diag bundle · node (list|token|revoke|inventory) · login [--code] · logout · whoami · user (list|add|set-role|disable|enable|passwd|delete|mfa setup|mfa enable|mfa disable) · approval (list|approve|reject) · vfs (mount|unmount|list) · access (local-mount|autoconnect) · disk wipe · job (list|watch|pause|resume|cancel|retry|errors) · activity (tail|verify) · completion`.

Agent: `continuum-agent enroll | run [--root …] [--controller --token] [--peer-listen :8444 --peer-url https://host:8444] | status | service (print|install|uninstall|status) | native-host (install|uninstall)`.

전역 플래그 `--approval <id>`: 2인 승인 대상 작업(wipe·drain·volume delete·user delete·node revoke)을 승인 후 재실행. 복사·이동·Drain·Rebalance 는 `--window 22:00-06:00` 지원.

2인 승인·MFA 실제 출력 (`test/impl/29/logs/cli-security.log`):

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

종료 코드(FR-C06): 0 성공 · 1 일반/not_found · 2 사용법(비대화형에서 `--yes` 누락 포함) · 3 공간 부족 · 4 검증 불일치 · 5 권한·로그인 필요 · 6 오프라인/Controller 불통 · 7 충돌 · 8 revision 불일치 · 130 중단.
진행 표시: TTY 진행 막대, 파이프(비TTY)는 1초 간격 JSON 줄. `disk discover --local` 은 Controller 없이 동작.

plan 출력 예 (`test/impl/7/logs/live-cli.log`):

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

## 테스트

| 층 | 명령 | 내용 |
|---|---|---|
| Go 단위·통합 | `make test` / `go test -race ./...` / `CONTINUUM_TEST_PG=… go test ./internal/...` | 17 패키지(SQLite·PostgreSQL 모두). Agent ↔ Agent 직접 전송(일시정지·오프셋 재개·중계 폴백·티켓 위조 6종 거부), LDAP(가짜 디렉터리: 그룹→역할·주입·StartTLS·LDAPS 사설 CA·잠금), VFS(Linux FUSE, 컨테이너), fsnotify 감시, TOTP RFC 벡터·MFA·2인 승인·OIDC(가짜 IdP, PKCE·서명·aud·nonce 위조 거부), 비밀 저장소, 이메일(가짜 SMTP), Grafana 메트릭 일치, OTLP 내보내기, Agent 등록·회전·폐기·오프라인·재연결, cross-node 복사·이동·Drain·자동 재개, RBAC 매트릭스, 작업 lease 인수, Wipe, FR-P08 재현(24/6 GiB, 실복사 24/6 MiB), 장애 주입(디스크 분리·취소·재시작 복구), 감사 체인 변조 탐지, API 보안 가드, CLI 종료 코드, SCAN/충돌/Drain/Rebalance, smb.conf 렌더링, 메트릭·진단·웹훅 |
| Web 단위 | `pnpm --filter @continuum/web test` | FR-V10 계산식, 순번 재부여, 포맷 |
| 브라우저 E2E | `make e2e` | 실제 Chrome/Chromium + 실제 Controller·Agent, web 13 스펙(+ MFA 등록·코드 로그인, 2인 승인) / 확장 4 스펙(T1, T2 로컬 풀, T3 native host, T3 강등). 이전 구성 web 11 스펙: Agent 등록·원격 Attach·폐기, Wipe, 로그인·RBAC. 이전 구성 web 8 스펙: Create Volume DnD·복사, Conflict Center, 디스크 상세, Rebalance·Drain, SMB 접근점(Docker 이미지 없으면 skip), 알림·웹훅·진단, 작업 상세·폴더 선택기·ko/en / 확장 1 스펙 |
| 개발환경 | [test/dev-env/result.md](test/dev-env/result.md) | 12 케이스 |

차수별 판정·시도 이력은 `test/impl/<N>/result.md`.

## 트러블슈팅 (실제로 겪은 문제)

| 증상 | 원인·해결 |
|---|---|
| `pnpm install` 이 `ERR_PNPM_IGNORED_BUILDS: esbuild` 로 실패 | pnpm 11 은 빌드 스크립트를 기본 차단 → `pnpm-workspace.yaml` 의 `allowBuilds: { esbuild: true }` (반영됨) |
| `password authentication failed for user "app"` | 예전 `pgdata` 볼륨이 다른 비밀번호로 초기화된 채 남음 → `make dev-stop` 후 `docker compose down -v` |
| 확장 E2E 가 서비스 워커를 못 찾음 | 정식 Chrome 137+ 는 `--load-extension` 차단 → `npx playwright install chromium` |
| `make preflight` 에 cargo WARN | rustup 은 PATH 를 수정하지 않도록 설치됨 → `source $HOME/.cargo/env` |
| 작은 파일 대량 복사가 느림(macOS ≈8 ms/파일) | 원자적 복사의 fsync(파일+디렉터리, Go 는 darwin 에서 F_FULLFSYNC). 무결성 우선 설계 |
| 볼륨 경고 "share free space" | 같은 물리 볼륨의 폴더 여러 개를 디스크로 붙이면 여유 공간이 중복 집계됨 — 시험용 외엔 디스크별 마운트 루트 사용 |
| 타 Controller 마커로 attach 실패(검사 6) | 다른 Controller 가 쓰던 디스크 → `volume import`(adopt) 사용 |
| `disk attach` 가 수 초 걸림 | 잠든 외장 HDD 에 `diskutil info` 가 스핀업 대기 → OS 메타 30s 캐시, 네트워크 FS 는 diskutil 생략 (반영됨) |
| SMB 쓰기 `Permission denied` (Docker Desktop) | virtiofs 가 bind mount 를 root 소유로 보임 → 접근점이 data root 소유 uid 로 SMB 사용자 생성 (반영됨) |
| SMB rename `Operation not supported`, Finder 태그 미저장 | Docker Desktop virtiofs 는 `no_xattr` → xattr 미지원 감지 시 fruit/streams 끄고 AppleDouble(`._*`) 사용 (반영됨, SCAN 은 `._*` 무시). streams_depot 은 `lost-*` 폴더를 대량 생성해 사용 안 함 |
| 재시작한 Agent 컨테이너가 바로 종료 | 컨테이너 명령이 매번 `enroll` 을 실행(토큰 1회용) → `continuum-agent run --controller … --token …` 사용: 미등록일 때만 등록 (반영됨) |
| Controller 를 SIGKILL 한 뒤 작업이 멈춤 | 이전 워커의 lease(≤30s)가 남아 있음 → Background 30초 틱이 만료 lease 작업을 인수 (반영됨) |
| 헤더가 "1/2 Agent 온라인" | 폐기한 노드를 전체 수에 포함하던 문제 → REVOKED 제외 (반영됨) |
| VFS 로 복사한 파일 크기가 카탈로그에 0 | FUSE RELEASE 는 close 이후 비동기 → FLUSH 시점에 카탈로그 반영 (반영됨) |
| `cp -a` 가 VFS 에서 "No data available" | ACL xattr 조회 → Setxattr ENOTSUP / Getxattr ENODATA 로 응답 (반영됨) |
| mergerfs 합집합에 `volume.json` 이 보임 | mergerfs 로 숨길 수 없음 → 내부 파일을 숨기는 네이티브 VFS(`continuum vfs mount`) 권장 |
| **macOS 가 재부팅됨 (커널 패닉 `nfs_vinvalbuf2: ubc_msync failed!, error 22`)** | FUSE-T(NFS 루프백) 마운트가 macOS NFS 클라이언트 패닉을 일으킴(macOS 26.6.2 에서 4회 재현) → FUSE-T 백엔드는 `CONTINUUM_VFS_FUSET=i-accept-kernel-panic-risk` 없이는 거부, 시험은 `CONTINUUM_TEST_MACOS_MOUNT=1` 없이는 skip. macOS 는 SMB 접근점 사용 |
| macOS `vfs mount` 가 멈춤 | go-fuse 는 `mount_macfuse` 가 fd 전에 실패하면 무한 대기 → 15초 후 안내 오류 반환(반영됨). 원인 `mount_macfuse: the file system is not available` = macFUSE 시스템 확장 미승인 → 시스템 설정에서 허용 후 재시작 |
| 직접 전송이 중계로 바뀜 (`relaying through the controller` 로그) | 원본 Agent 에 peer 엔드포인트가 없음(구버전·`--peer-listen off`) 또는 접속 불가 → `--peer-url` 을 다른 Agent 가 닿는 주소로 |
| LDAP 로그인 409 "StartTLS" | `ldap://` 는 기본 StartTLS 필수 → 디렉터리에서 TLS 활성화, `ldaps://`, 또는 신뢰된 로컬 디렉터리에 한해 `ldap.starttls=false`. 사설 CA 는 `ldap.ca_file` |
| `helm lint` 가 DB Secret 누락을 통과 | lint 는 `fail` 을 평가하지 않음 → `helm template` 은 `database.existingSecret is required` 로 실패 (의도) |
| 2인 승인 대상 작업이 매번 403 | 승인 ID 는 요청자·작업·대상에 묶인 1회용 → 승인 후 같은 사용자가 `--approval <id>` (UI 는 자동 첨부) |
| Windows `\\host\share` 연결 불가 | Windows 는 포트 445 고정 → `continuum access enable --bind 0.0.0.0 --port 445` (호스트 445 비어 있어야 함) |

## 디렉터리 구조

```
continuum/
├── cmd/{controller,continuum,migrate,agent}/
├── internal/
│   ├── api/        # REST + SSE, CORS/토큰/JSON 가드
│   ├── core/       # 노드·디스크·볼륨·카탈로그 서비스, 오류 코드
│   ├── transfer/   # 작업 엔진, 원자적 복사, 이동·이름변경·삭제·휴지통·검증
│   ├── placement/  # 순수 배치 엔진 (FR-P01~P11)
│   ├── disk/       # OS 탐색, 사전 검사 10항목, 마커
│   ├── store/      # DB 열기·마이그레이션(embed)·ULID
│   ├── audit/      # 해시 체인 감사 + 이벤트
│   ├── agent/      # Agent 런타임·OS 서비스·native host
│   ├── hub/        # Agent 허브 (등록·mTLS 스트림·blob 중계·원격 FS)
│   ├── pki/ agentproto/ nodefs/ auth/ secrets/
│   ├── vfs/        # Linux FUSE 네이티브 마운트 (Windows 는 stub)
│   ├── watch/      # fsnotify 감시자
│   ├── tracing/    # OTLP/HTTP JSON 트레이스
│   ├── access/     # SMB 접근점 (Samba + mergerfs 컨테이너 관리, smb.conf 렌더링)
│   ├── ops/        # /metrics, 진단 번들, 웹훅·이메일 알림
│   ├── cli/        # continuum CLI
│   └── version/
├── apps/{web,desktop,extension}/
├── deploy/samba/Dockerfile                     # continuum-samba:local
├── deploy/grafana/continuum-dashboard.json     # Grafana import
├── deploy/helm/continuum/                      # Helm chart (make deploy)
├── deploy/release/ · deploy/extension-policy/  # 릴리즈 이미지 · 확장 기업 배포 정책
├── tools/notices/ · scripts/                    # 라이선스 고지 생성 · 재현 빌드 / 릴리즈 lint
├── docs/optional-dependencies.md · THIRD_PARTY_NOTICES.md
├── test/{dev-env,impl/1..34,impl/regression,impl/regression-p2..p5}/
├── .github/workflows/ci.yml · .goreleaser.yaml · release-please-config.json
├── docker-compose.yml · Dockerfile · Makefile · Procfile.dev
└── go.mod · package.json · pnpm-workspace.yaml · STACK.md
```

## 다음 단계

- **E31 BLOCKED**: macOS VFS — FUSE-T 경로는 기능 시험 PASS 였으나 macOS 커널 패닉으로 기본 차단. macFUSE 시스템 확장 승인(또는 macFUSE ≥ 5.4 FSKit) 후 **일회용 시험 머신**에서 `CONTINUUM_TEST_MACOS_MOUNT=1 go test ./internal/vfs` 재검증
- VFS: Windows WinFsp (cgofuse, cgo)
- 인증: 실 IdP·AD/OpenLDAP 연동 실측 (프로토콜은 가짜 IdP·가짜 LDAP 으로 검증)
- 릴리즈: 첫 태그 전에 `goreleaser check`, 서명 인증서·스토어 계정 등록(`release` environment), 프로젝트 LICENSE 결정
- 관측: OTLP gRPC(4317)
- 배포: 실 클러스터 배포·실제 `docker build`(CI 첫 실행) 검증
