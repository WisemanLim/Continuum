# regression-p6 — P6 전체 회귀 (E35~E37 이후) · **PASS** (시도 1회)

| 항목 | 판정 | 로그 |
|---|---|---|
| `make test` | PASS | `logs/make-test.log` |
| `go test -race ./...` | PASS | `logs/race.log` |
| PostgreSQL 17 전체 스위트 (임시 컨테이너 → 삭제) | PASS | `logs/postgres.log` |
| `make e2e` web 13 + 확장 4 | PASS | `logs/e2e.log` |
| Linux 컨테이너: TestVFS · TestWatcher · TestDirect×2 · TestCommandTracePropagation · TestPeerEndpointSecurity | PASS | `logs/linux-container.log` |
| 교차 빌드 6 대상 + darwin `-tags fuset`(빌드만, 마운트 안 함) | PASS | `logs/cross-build.log` |
| `cargo check` | PASS | `logs/cargo-check.log` |
| release-lint (250 검사) | PASS | `logs/release-lint.log` |

안전: 이 Mac 에서 마운트·커널 확장·시스템 설정 변경 없음. macOS VFS 시험은 기본 skip. 회귀 동안 새 재부팅·패닉 없음(마지막 재부팅 13:55, 패닉 로그 4건 그대로).
