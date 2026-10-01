# 전체 회귀 (P3 완료 게이트) · **PASS** (시도 3회)

| 명령 | 결과 | 로그 |
|---|---|---|
| `make test` (go test ./... + vitest) | PASS — Go 15 패키지(테스트 있는 13), vitest 4 | `logs/make-test.log` |
| `go test -race ./...` | PASS | `logs/race.log` |
| `CONTINUUM_TEST_PG=… go test ./internal/...` (PostgreSQL 17) | PASS — 13 패키지 | `logs/postgres.log` |
| `make e2e` (실제 Chrome·Chromium + 실제 Controller·Agent·native host) | PASS — web 11, 확장 3 | `logs/make-e2e.log` |
| `GOOS=linux|windows|darwin go build ./...` · `cargo check` (desktop) | PASS | — |

시도 이력
1. FAIL — create-volume.e2e: 헤더 "모든 Agent 온라인" 기대, 실제 "1/2" — 앞선 agents.e2e 가 폐기(REVOKED)한 노드를 전체 수에 포함. 제품 수정: `/v1/info` 가 REVOKED 노드를 제외(+ 노드 2개 이상이면 mode=M2).
2. FAIL(간헐) — agents.e2e: Attach 마법사에 Agent 의 `--root` 폴더가 안 보임. 원인: Agent 가 inventory 를 느린 볼륨 탐색(diskutil, 잠든 HDD) 이후에 전송. 수정: roots 만 담은 inventory 즉시 전송 후 탐색 결과 재전송, 마법사는 원격 노드일 때 3초마다 갱신.
3. PASS.
