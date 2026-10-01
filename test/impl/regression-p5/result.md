# regression-p5 — P5 전체 회귀 (E31~E34 이후) · **PASS** (시도 1회)

| 항목 | 판정 | 로그 |
|---|---|---|
| `make test` (Go 전체 + web vitest) | PASS | `logs/make-test.log` |
| `go test -race ./...` | PASS | `logs/race.log` |
| PostgreSQL 17 전체 스위트 (임시 컨테이너 15432 → 삭제) | PASS | `logs/postgres.log` |
| `make e2e` web 13 + 확장 4 | PASS | `logs/e2e.log` |
| Linux 컨테이너(FUSE): TestVFS · TestWatcher · TestDirect×2 · TestPeerEndpointSecurity | PASS | `logs/linux-container.log` |
| 교차 빌드 6 대상 (`./cmd/...`) | PASS | `logs/cross-build.log` |
| `cargo check` | PASS | `logs/cargo-check.log` |
| release-lint | PASS (238 검사) | `logs/release-lint.log` |

## 재확인 (E31 FUSE-T 차단·vfs 백엔드 분리 이후)
- `make test` PASS (`test/impl/31/logs/make-test-after-gate.log`), macOS 마운트 시험은 기본 skip.
- Linux 컨테이너 TestVFS·TestDirect×2 PASS (`test/impl/31/logs/linux-regression-after-gate.log`).
- 교차 빌드 linux/darwin/windows, release-lint 238/0.
