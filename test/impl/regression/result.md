# 전체 회귀 게이트 — **PASS** (시도 2회)

| 항목 | 결과 |
|---|---|
| `make test` (go test ./... + vitest) | 8 Go 패키지 ok, web 4 tests |
| `go test -race ./...` | 8 패키지 ok, race 0 |
| `make e2e` | web 2 passed, extension 1 passed |
| `make preflight` | OK |
| `make -n local-all … db-reset ENV=dev` | 문법 OK |

시도 이력
1. FAIL — (a) **데이터 레이스**: 작업 시작 시 `run()` 이 진행 상황(합계·상태)을 잠금 없이 쓰는 동안 SSE `watch` 의 `Get()→snapshot()` 이 동시 읽기(E5 코드, API·CLI 테스트에서 검출) → 합계를 지역 변수로 계산 후 `rc.mu` 아래에서 게시. (b) `make e2e` 의 확장 빌드 `tsc` 가 Node 용 E2E 파일까지 검사 → 확장 tsconfig 에서 `e2e`·`playwright.config.ts` 제외.
2. PASS (`full-regression.log`, `e2e.log`).
