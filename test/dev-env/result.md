# dev-env 검증 결과 — 2026-09-30

환경: macOS (Darwin 25.6, arm64) · Go 1.26.4 · Node 22.23.1 · pnpm 11.10.0 · Docker 29.3.0 · goreman 0.3.19 · Rust 미설치
원본 로그: `logs/dev-env-run.log`, `logs/dev-stack.log` (커밋 제외)

| # | 케이스 | 결과 | 비고 |
|---|---|---|---|
| 1 | `make preflight` | PASS | WARN: Rust(cargo) 미설치 |
| 2 | `make local-build` | PASS | go build + pnpm install |
| 3 | `make local-all` | PASS | SQLite 모드, goreman controller·agent·web |
| 4 | `/healthz` | PASS | `{"env":"local","status":"ok","version":"0.0.0-dev"}` |
| 5 | Web UI :5173 | PASS | HTTP 200 |
| 6 | `local-ps` / `local-stop` | PASS | 3 프로세스 표시 → 종료 확인 |
| 7 | `make db-migrate` | PASS(스텁) | `not implemented yet` — implement 에서 연결 |
| 8 | `make test` | PASS | `TestHealthz` ok, vitest 테스트 없음 허용 |
| 9 | CLI `version` | PASS | `continuum 0.0.0-dev` |
| 10 | `dev-build` → `dev-ps` → `dev-stop` | PASS | postgres healthy, controller env=dev |
| 11 | 데스크톱 Tauri | 보류 | Rust 미설치. 아이콘은 `assets/icon.svg` 로 생성 완료 |
| 12 | 확장 wxt | PASS(빌드) | `tsc --noEmit && wxt build` 성공. Chrome 로드 수동 확인 필요 |

## 발견·수정

| 문제 | 수정 |
|---|---|
| pnpm 11 `ERR_PNPM_IGNORED_BUILDS: esbuild` 로 install 비정상 종료 | `pnpm-workspace.yaml` 에 `allowBuilds: { esbuild: true }` |
| 확장 `TS2304: Cannot find name 'chrome'` | WXT 전역 `browser.sidePanel` 로 변경, build 에 `tsc --noEmit` 추가 |
| Tauri 아이콘 없음 | `apps/desktop/assets/icon.svg`(∞ 로고, #3F51E5) → `tauri icon` 으로 `src-tauri/icons/*` 생성 |

## 해석된 의존성 버전

react/react-dom 19.3.0 · vite 7.3.6 · @vitejs/plugin-react 5.2.0 · vitest 3.2.7 · typescript 5.9.3 · wxt 0.21.4 · @wxt-dev/module-react 1.2.2 · @tauri-apps/api·cli 2.12.0
