# impl/23 결과 — E23 중앙 서버 모드 · **PASS** (시도 3회)

| # | 판정 | 근거 |
|---|---|---|
| 1 | PASS | `logs/pg-go-test.log` — 13 패키지 PostgreSQL 17.10 (Dialect=postgres, 마이그레이션 0001~0006 확인) |
| 2 | PASS | `TestJobLeaseTakeover` |
| 3, 5, 6 | PASS | `logs/live-compose.log` |
| 4 | PASS | `docker compose --profile app --profile agents config` |

시도 이력
1. FAIL(시험) — zsh 가 `$DC` 변수(공백 포함 명령)를 분리하지 않아 compose 미기동 → 셸 함수로 수정.
2. PASS(기본 흐름) — 설계 공백 발견: SIGKILL 직후 재시작한 Controller 는 이전 lease(≤30s)가 살아 있어 시작 시 Recover 가 건너뛰고 다시 보지 않음.
3. PASS — Background 30초 틱에서 Recover(만료 lease 인수) 실행.

시험 구성: 로컬에 있는 이미지만 사용(postgres:17, debian:bookworm-slim + 미리 빌드한 linux/arm64 바이너리 bind mount). 실제 배포용 `Dockerfile`(golang 빌드 → distroless, controller/agent 타깃)은 이미지 pull 이 필요해 이 머신에서 빌드하지 않음.
가정: Agent 세션은 한 Controller 에만 붙음 — 다중 Controller 는 작업 중복 방지(lease)까지이고, 활성-활성 Agent 라우팅은 범위 밖. Helm 차트 미작성.
