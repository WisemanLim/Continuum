# impl/6 — E6 Controller REST API + SSE (FR-C03·T09·T21·O03·S02·S04, PRD §13.4)

| # | 케이스 | 방법 | 기대 |
|---|---|---|---|
| 1 | 전체 흐름 | `TestAPIFlow` + 실바이너리 curl | attach → create volume → plan → start(202) → SSE watch 마지막 상태 DONE → files·locate·overview·audit verify |
| 2 | 낙관적 잠금 | PUT stale revision | 409 `revision_mismatch` + current_revision |
| 3 | 공간 부족 | 큰 min_free 로 start | 409 `insufficient_space` |
| 4 | 404 | 없는 볼륨 | 404 `not_found` |
| 5 | 토큰 | `CONTINUUM_TOKEN` 설정 시 | 토큰 없으면 403, /healthz 는 공개 |
| 6 | CORS | 허용 외 Origin | 403 + CORS 헤더 없음, `chrome-extension://*` 허용 |
| 7 | CSRF | form-urlencoded POST | 415 (JSON 만 허용) |
