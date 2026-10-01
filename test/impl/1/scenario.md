# impl/1 — E1 카탈로그·레지스트리 (FR-K01·K06·K07, PRD §13.1)

| # | 케이스 | 방법 | 기대 |
|---|---|---|---|
| 1 | SQLite 마이그레이션 | `make db-migrate` ×2 | 1회차 `applied [0001_init] (sqlite)`, 2회차 `already current` |
| 2 | PostgreSQL 이식성 | compose postgres + `DATABASE_URL=postgresql://… go run ./cmd/migrate up` ×2 | 동일 결과, 테이블 10개 |
| 3 | 스키마 9표 조회 | `TestOpenAndMigrateSQLite` | nodes·volumes·disks·objects·jobs·job_items·tombstones·events·audit 조회 성공 |
| 4 | 감사 해시 체인 | `TestHashChain` | 정상 체인 intact, seq 2 변조 시 seq 2 탐지 |
| 5 | placeholder 변환 | `TestPlaceholderRewrite` | `?` → `$1,$2` |
| 6 | DATABASE_URL 형식 | `TestSQLitePathForms` | `sqlite:///./x.db`, `sqlite://./x.db`, `sqlite::memory:` |
