# regression-p4 — P4 전체 회귀 (E26~E30 이후) · **PASS** (시도 3회)

| 항목 | 판정 | 로그 |
|---|---|---|
| `make test` (Go 전체 + web vitest 7) | PASS | `logs/make-test.log` |
| `go test -race ./...` | PASS (2회차) | `logs/race.log`, 1회차 `logs/race-attempt1.log` |
| PostgreSQL 17 전체 스위트 (`CONTINUUM_TEST_PG`, 임시 컨테이너 15432 → 삭제) | PASS | `logs/postgres.log` |
| `make e2e` web 13 + 확장 4 | PASS (3회차) | `logs/e2e.log`, `logs/e2e-attempt1.log`, `logs/e2e-attempt2.log` |
| Linux 컨테이너(FUSE): vfs·watch·api·tracing 테스트 바이너리 | PASS | `logs/linux-container.log` |
| 교차 빌드 linux/darwin/windows × amd64/arm64 (`./cmd/...`, vfs·secrets vet) | PASS | `logs/cross-build.log` |
| `cargo check` (Tauri 셸) | PASS | `logs/cargo-check.log` |
| `go mod tidy` — go-fuse·fsnotify 직접 의존성으로 정리 | 완료 | — |

시도 이력
1. FAIL — race: TestWatcherReflectsDirectEdits 가 카탈로그 반영 직후(스캔 카운터 증가 전) 카운트를 읽음 → 시험이 카운터도 대기(제품 결함 아님, `-race -count=5` PASS). e2e: 설정 화면에 SSO 카드가 알림 카드 앞에 생겨 `Save` 버튼 순서 변경 → SSO 카드를 알림 뒤로 이동.
2. FAIL — e2e 확장 빌드: 확장 tsconfig(`noUncheckedIndexedAccess`)에서 App.tsx 의 `m[1]` 타입 오류 → `m?.[1]` 가드.
3. PASS.
