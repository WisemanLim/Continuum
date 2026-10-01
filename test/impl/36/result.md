# impl/36 결과 — E36 Agent 트레이스 · **PASS** (시도 2회)

| # | 판정 | 근거 |
|---|---|---|
| 1~3 | PASS | `logs/go-test.log` — stat 2회(성공·실패): client 2·server 2, 부모·trace 일치, 실패 쪽 양측 error |
| 3·4 | PASS | `logs/live-trace.log` — `POST /v1/disks`(controller SERVER) → `agent probe/mkdirall/writefile`(controller CLIENT) → 같은 이름 agent SERVER span, 모두 trace `27a58d8d50a7…`; traceparent 헤더를 준 요청은 그 trace 를 이어받음 |
| 5 | PASS | hub·agent 전체 시험 |

시도 이력: 1 — 시험의 부모 검사 조건이 부정확 → root span id 직접 비교로 수정, 2 — PASS. Live 스크립트는 상태 디렉터리 이름 충돌만 수정.
안전: 사용자 공간 프로세스·loopback 만 사용.
