# impl/9 — E9 데스크톱 셸(Tauri) · Chrome 확장 T1 (FR-G17·N07·R02 일부, FR-X01·X04·X05)

| # | 케이스 | 방법 | 기대 |
|---|---|---|---|
| 1 | Tauri 빌드 | `pnpm --filter @continuum/desktop build --bundles app` | `Continuum.app` 생성, Go Controller 가 `externalBin` 사이드카로 번들 |
| 2 | 사이드카 자동 기동 | 앱 실행 → `curl :8080/healthz` | `env=desktop`, 앱 데이터 디렉터리 SQLite |
| 3 | 사이드카 정리 | 앱 `kill -9` | Controller 가 고아로 남지 않고 스스로 종료, 8080 해제 |
| 4 | 확장 빌드 | `pnpm --filter @continuum/extension build` | `tsc --noEmit` + wxt MV3 (sidepanel, console, background) |
| 5 | 사이드 패널 T1 | Playwright Chromium 에 확장 로드 | Controller URL 저장 → Connected, 타일 표시, T2/T3 "Not available in browser mode" 배지 |
| 6 | 전체 콘솔 | `console.html#/nodes` | 데스크톱과 같은 React UI, "모든 Agent 온라인" |
| 7 | 최소 권한 | manifest | `permissions = ["storage","sidePanel"]`, host 권한은 optional |
