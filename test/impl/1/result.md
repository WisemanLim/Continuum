# impl/1 결과 — E1 카탈로그·레지스트리 · **PASS** (시도 2회)

| # | 판정 | 근거 |
|---|---|---|
| 1 | PASS | `migrate up: applied [0001_init] (sqlite)` → `already current (sqlite)` |
| 2 | PASS | `applied [0001_init] (postgres)` → `already current`, `\dt` 10 tables (`logs/postgres-migrate.log`) |
| 3~6 | PASS | `logs/go-test.log` |

시도 이력
1. FAIL — `password authentication failed for user "app"`: dev 실행 때 생성된 `pgdata` 볼륨이 `.env.dev` 비밀번호로 초기화된 채 남아 있음 (코드 결함 아님). `docker compose down -v` 후 재시험.
2. PASS.

비고: 마이그레이션은 `internal/store/migrations/*.sql` 에 embed. SQLite·PostgreSQL 공용 SQL(ULID TEXT id, BIGINT 크기). FTS5 검색(FR-K08)은 P2.
