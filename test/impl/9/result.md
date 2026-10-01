# impl/9 결과 — E9 데스크톱·확장 · **PASS** (시도 3회)

| # | 판정 | 근거 |
|---|---|---|
| 1 | PASS | `target/release/bundle/macos/Continuum.app` (33.7 MiB) |
| 2 | PASS | `logs/sidecar-lifecycle.log`: `{"env":"desktop","status":"ok"}` |
| 3 | PASS | 같은 로그: `controller exited (no orphan)`, `port 8080 free` |
| 4~7 | PASS | `logs/extension-e2e.log`, 스크린숏 `logs/ext-sidepanel.png`, `logs/ext-console.png` |

시도 이력
1. FAIL — **앱을 강제 종료하면 사이드카 Controller 가 고아로 남음**(창 파괴 이벤트에만 의존). → `RunEvent::Exit` 에서 종료 + Controller 가 부모 소멸(재부모화) 감지 시 정상 종료(`CONTINUUM_EXIT_WITH_PARENT=1`).
2. FAIL — 확장 tsconfig(WXT) 가 더 엄격(`noUncheckedIndexedAccess`, `noImplicitOverride`) → 공유 web 코드를 엄격 모드로 정리, Overview 타일 타입 명시.
3. FAIL(환경) — 정식 Chrome 137+ 는 `--load-extension` 을 막음 → 시험은 Playwright Chromium 사용(사용자는 chrome://extensions 개발자 모드 "압축해제된 확장 로드"로 설치).
4. PASS.

한계: 데스크톱 창 시각 캡처는 화면 녹화 권한이 없어 불가(배경화면만 캡처됨) — 창에 로드되는 UI 는 E8 에서 같은 빌드로 E2E 검증. `kill` 은 SIGKILL 이라 실행 중 작업은 다음 기동 때 `Recover` 가 재개(NFR-A01). 서명·공증·dmg/msi/deb 번들은 P3(FR-R03).
