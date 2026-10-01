# impl/10 — E10 CI·릴리즈 기초 (FR-R05·R06, FR-R02 일부, FR-S08 일부)

| # | 케이스 | 방법 | 기대 |
|---|---|---|---|
| 1 | CI 정의 | `.github/workflows/ci.yml` YAML 파싱 | go(3 OS, **FR-P08 게이트** 단계) · race · web(unit + Playwright E2E + 확장 E2E) · desktop(macOS cargo check) |
| 2 | FR-P08 게이트 로컬 실행 | `go test ./internal/placement ./internal/transfer -run FRP08` | 두 테스트 PASS (24/6 GiB, 실복사 24/6 MiB) |
| 3 | goreleaser 설정 | `goreleaser check` | 1 configuration file(s) validated |
| 4 | 스냅샷 크로스 빌드 | `goreleaser release --snapshot --clean --skip=sbom,publish` | darwin/linux/windows × amd64/arm64 archive, deb/rpm, checksums.txt |
| 5 | 버전 주입 | 산출물 `continuum version` | `continuum 0.0.0-SNAPSHOT-<sha>` (ldflags) |
| 6 | 버전·CHANGELOG | `release-please-config.json` JSON 유효 | Go 릴리스 타입, tauri.conf.json·Cargo.toml 버전 동기화 |
