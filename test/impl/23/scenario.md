# impl/23 — E23 중앙 서버 모드 (FR-N08)

| # | 수용 기준 | 방법 |
|---|---|---|
| 1 | 같은 Go 테스트 스위트가 PostgreSQL 17 에서도 통과(공용 SQL 1벌) — `CONTINUUM_TEST_PG` 로 테스트마다 새 DB | `logs/pg-go-test.log` |
| 2 | 작업 lease: 작업은 한 Controller 워커만 실행(claimed_by·lease_until, 실행 중 갱신, 종료 시 해제). 살아 있는 lease 는 다른 워커가 손대지 않고, 만료되면 인수 | `TestJobLeaseTakeover` (SQLite·PG) |
| 3 | 워커가 SIGKILL 로 죽어도 lease 만료 후 Background 틱이 작업을 인수해 완료 | live (docker kill → t+35s 재개 → DONE, verify 4/4) |
| 4 | compose: postgres + controller(:8080 API, :8443 mTLS, `/data` 볼륨에 CA) + `--profile agents` Agent (첫 기동만 토큰, 이후 상태 볼륨) | `docker-compose.yml` config 검증, live (`test/impl/23/compose.yml`) |
| 5 | Controller 재시작 후에도 노드·디스크·카탈로그·작업 유지(PostgreSQL), Agent 재연결 | live |
| 6 | Agent 2대에 balanced 정책 분산 복사 | live (a-d1 3 · b-d1 3) |
