# dev-env 검증 시나리오 — continuum (custom / base go-gin)

목표: 표준 개발환경이 스캐폴드 상태로 동작함을 1회 검증. 결과는 `result.md`, 원본 로그는 `logs/`.

| # | 케이스 | 명령 | 기대 결과 |
|---|---|---|---|
| 1 | 도구 점검 | `make preflight` | `=== Preflight OK ===` (Go 1.24+, Node 22+ 필수. pnpm/docker/goreman/cargo 는 WARN 가능) |
| 2 | 의존성·빌드 | `make local-build` | `go build ./...` 성공, `pnpm install` 성공 |
| 3 | 로컬 기동 | `make local-all` | SQLite 모드 메시지, goreman 백그라운드 기동 (controller·agent·web) |
| 4 | 헬스 체크 | `curl -s localhost:8080/healthz` | `{"env":"local","status":"ok","version":"0.0.0-dev"}` |
| 5 | Web UI | 브라우저 `http://localhost:5173` | "Controller: ok (env=local, …)" 표시 |
| 6 | 프로세스 상태·정지 | `make local-ps` → `make local-stop` | 상태 출력 후 전부 종료 |
| 7 | DB 마이그레이션 | `make db-migrate` | 스텁 메시지 `migrate [up]: not implemented yet` (implement 에서 golang-migrate 연결) |
| 8 | 테스트 | `make test` | `TestHealthz` PASS, pnpm 테스트 통과 (테스트 없음 허용) |
| 9 | CLI | `go run ./cmd/continuum version` | `continuum 0.0.0-dev` |
| 10 | dev 스택 (선택) | `make dev-build` → `make dev-ps` → `make dev-stop` | postgres healthy + controller 기동 |
| 11 | 데스크톱 (선택) | `pnpm --filter @continuum/desktop tauri icon <1024px.png>` → `pnpm --filter @continuum/desktop dev` | Tauri 창에 web UI 표시 (Rust 필요) |
| 12 | 확장 (선택) | `pnpm --filter @continuum/extension dev` | Chrome 에 side panel 로드 |
