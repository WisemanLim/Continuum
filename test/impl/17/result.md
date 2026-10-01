# impl/17 결과 — E17 UI·i18n 마감 · **PASS** (시도 4회)

| # | 판정 | 근거 |
|---|---|---|
| 1, 2 | PASS | 키 ~400쌍(en/ko 동일 수), `logs/e2e.log`, `03-job-detail-ko.png` |
| 3, 4 | PASS | `logs/go-test.log` (TestBrowse), `01-folder-picker.png`, `02-job-detail.png` |
| 5 | PASS | impl/12·15·16 E2E |
| 6 | PASS | `../regression-p2/logs/make-e2e.log` |

시도 이력
1. FAIL(tsc) — Nodes.tsx 폴더 선택기 상태 `pick` 이 기존 함수 `pick()` 과 이름 충돌 → `browsing`.
2. FAIL — i18n 적용 후 ko 기본 E2E(create-volume)·확장 콘솔이 영어 셀렉터 사용 → en→ko 사전으로 셀렉터 자동 치환, en 전용 스펙은 `continuum.prefs` 로 en 고정.
3. FAIL — 스펙들이 한 Controller 를 공유해 복사 대화상자 기본 볼륨·볼륨 수 타일이 달라짐 → 대상 볼륨 명시 선택, 개수 단언 완화. 확장 tsconfig(strict index) 에서 web 소스 3곳 타입 오류 → 수정.
4. PASS — web 8 + 확장 1.

부수: 진행 막대가 100% 에서 용량 경고 색(빨강)으로 표시 → 진행 막대는 기본 색. 버전 표기 0.2.0 · P2.

가정: Tauri 네이티브 폴더 대화상자(@tauri-apps/plugin-dialog)는 새 npm·cargo 의존성(네트워크 설치) 필요 → Controller 호스트 폴더 탐색 API 로 대체. 브라우저·데스크톱·확장에서 동일하게 동작하고 Controller 가 실제 읽을 수 있는 경로만 보임.
