# STACK — Continuum 스택 결정 기록

근거: `.docs/Continuum_PRD.pdf` §12 (2026-09-19 실측), `.docs/Continuum_UI_Design.pdf`, `/wise-dev-std:recommend`·`scaffold custom`·`implement` 결정.

## 선택

| 항목 | 선택 |
|---|---|
| 프로파일 | `custom` (base: `go-gin`) |
| 백엔드 | Go 1.26 — Controller(+내장 Agent, M1) · CLI · migrate 단일 모듈, `CGO_ENABLED=0` |
| API | **REST/JSON + SSE** (`net/http`) — implement 시작 시 사용자 결정 |
| Agent 전송 (P3) | **connect-go** 양방향 스트림 + JSON 코덱(protoc 없음), mTLS(자체 CA, TLS 1.3), 파일 바이트는 별도 HTTP blob 스트림 — implement P3 사용자 결정 |
| 인증 (P3) | 사용자·세션(PBKDF2-SHA256, 표준 라이브러리) + RBAC 5역할 |
| DB | SQLite(modernc, WAL, local/M1) · PostgreSQL 17(pgx, dev/staging/prod). 스키마는 공용 SQL 1벌 |
| 프론트 | Vite + React 19 + TypeScript, @dnd-kit, 순수 CSS 디자인 토큰(PRD §8.5) |
| 데스크톱 | Tauri 2.12 + tauri-plugin-shell (Go Controller 사이드카) |
| 확장 | wxt 0.21 MV3 (사이드 패널 T1 + web UI 재사용 전체 콘솔) |
| 패키지 | go modules · pnpm 11 workspaces · cargo |
| 로컬 매니저 | goreman (`Procfile.dev`: controller, web) |
| 시험 | go test(+race) · Vitest · Playwright(실제 Chrome/Chromium) |
| 릴리즈 | goreleaser v2 · release-please · GitHub Actions |
| SMB 접근점 (P2) | Samba 4.17 + mergerfs 2.33 컨테이너 (`deploy/samba`) — implement P2 사용자 결정 |
| 네이티브 VFS (P4) | **hanwen/go-fuse v2** (Linux FUSE, fs API) — implement P4 사용자 결정. Windows 빌드는 stub |
| FS 감시 (P4) | **fsnotify** (inotify / FSEvents·kqueue / ReadDirectoryChangesW), 3초 debounce 후 디스크별 증분 SCAN — implement P4 사용자 결정 |
| 비밀 저장 (P4) | **OS 명령 래핑**: `security`(macOS Keychain) · `secret-tool`(Linux Secret Service) · PowerShell PasswordVault(Windows), 없으면 0600 JSON — implement P4 사용자 결정 |
| MFA·SSO (P4) | TOTP RFC 6238(표준 라이브러리) · OIDC Authorization Code + PKCE, RS256 JWKS 검증 직접 구현 |
| 트레이스 (P4) | OTLP/HTTP JSON 직접 구현(표준 `OTEL_*` env), W3C traceparent |
| 배포 (P4) | Helm 3 chart `deploy/helm/continuum` · Grafana 대시보드 `deploy/grafana` |
| 직접 전송 (P5) | Agent ↔ Agent mTLS HTTPS pull + Controller CA(ECDSA) 서명 티켓, HTTP trailer BLAKE3, partial 길이 재개 — PRD 의 gRPC TransferService 대체 |
| LDAP/AD (P5) | **go-ldap/ldap/v3** — implement P5 사용자 결정 |
| macOS VFS (P5) | go-fuse + macFUSE(`mount_macfuse`) — 확장 승인 전 BLOCKED. 실험: cgofuse(`third_party/cgofuse` 패치, FUSE-T 우선) `-tags fuset` — macOS 커널 패닉(NFS) 으로 기본 차단 |
| 진행률·트레이스 (P6) | Agent `Progress` 메시지(250 ms) · 명령 `traceparent` (W3C) — 기존 connect 스트림 위, 새 의존성 없음 |
| 이미지 (P6) | Controller 이미지에 web UI(`/web`), distroless nonroot, node 22 + pnpm 11.10 빌드 stage |
| 공급망 (P5) | goreleaser v2 · cosign keyless · notarytool · jsign(Azure Trusted Signing) · GPG · syft · attest-build-provenance · dependabot · govulncheck — 설정 + 정적 검증(사용자 결정), 실 서명은 CI 비밀 |
| 도메인 | 없음 (COMPLIANCE.md 미생성) |

## go-gin 기본값·PRD 권고와 다른 점 (이유)

| 기본/권고 | 채택 | 이유 |
|---|---|---|
| Gin | stdlib `net/http` (Go 1.22+ 패턴 라우팅) | 의존성 최소, 라우팅 요구가 단순 |
| connect-go + buf (PRD §7.1·§13.3) | 클라이언트 API = REST/JSON + SSE, Agent 스트림 = connect-go(JSON 코덱) | 사용자 결정(P1·P3). buf/protoc 코드 생성 없이 Go 구조체 + 커스텀 코덱 |
| river (PostgreSQL 잡큐) | DB 상태기계 + 작업 lease(claimed_by·lease_until) | 사용자 결정(P3). SQLite·PostgreSQL 공용, 새 의존성 없음 |
| GORM / sqlc + golang-migrate | `database/sql` + 내장 마이그레이션(embed) | SQLite·PostgreSQL 공용 SQL 1벌, `?`→`$n` 변환만 필요 |
| Redis | 없음 | 캐시·브로커 요구 없음. 잡큐는 DB 상태기계(M1), river 는 P3 |
| Tailwind + shadcn/ui | 순수 CSS 토큰 | PRD §8.5 토큰을 그대로 CSS 변수로. 빌드 설정 단순 |
| TanStack Query/Table, Zustand, i18next | 자체 `useLoad` 훅 · 테이블 · 소형 i18n | P1 화면 수에서 충분. 필요 시 P2 도입 |
| distroless/base | distroless/static + `CGO_ENABLED=0` | cgo 없는 정적 바이너리 |
| Tauri plugin-dialog (네이티브 폴더 대화상자) | Controller 폴더 탐색 API(`/v1/fs/browse`) | 새 npm·cargo 의존성 없이 브라우저·데스크톱·확장 공통, Controller 가 읽을 수 있는 경로만 표시 |
| fsnotify 워처 (FR-K05) | P1~P3: 주기 SCAN → P4: fsnotify + 주기 SCAN 유지(네트워크 FS·원격 디스크) | P4 사용자 결정으로 도입 |
| go-oidc / golang.org/x/oauth2 | OIDC 직접 구현 (discovery·PKCE·JWKS RS256·iss/aud/exp/nonce) | 의존성 최소. RS256 만 지원(대부분 IdP 기본) |
| go.opentelemetry.io SDK + OTLP exporter | OTLP/HTTP JSON 직접 구현 | 수십 개 모듈 의존성 회피, 표준 env·수집기 호환. gRPC(4317) 미지원 |
| go-keyring / 99designs/keyring | OS 명령 래핑 | cgo 없음 유지, 사용자 결정 |
| gRPC TransferService.Push/Pull (PRD §7.6) | mTLS HTTPS GET + trailer | protoc/gRPC 의존성 없이 기존 Agent blob 방식과 동일, 재개는 `offset` 쿼리 |
| cgofuse (PRD 1순위 3-OS 바인딩) | go-fuse (Linux·macOS) | `CGO_ENABLED=0` 단일 바이너리 유지. Windows WinFsp·FUSE-T 는 cgo 필요 → 잔여 |

## 버전 (실측 설치값)

Go 1.26.4 · Node 22.23 · pnpm 11.10 · Rust 1.98 · React 19.3 · Vite 7.3 · TypeScript 5.9 · Vitest 3.2 · Playwright 1.63 · wxt 0.21.4 · Tauri 2.12 · modernc sqlite 1.60 · pgx 5.11 · cobra 1.10 · gopsutil 4.26 · zeebo/blake3 0.2.4 · connect-go 1.21 · golang.org/x/sys 0.48 (Windows SCM) · hanwen/go-fuse 2.11 · fsnotify 1.10.1 · Helm 3 (lint/template 검증) · go-ldap 3.4.14 · go-asn1-ber 1.5.8 · golang.org/x/crypto 0.54 · golang.org/x/text 0.40.
