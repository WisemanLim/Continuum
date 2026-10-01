# impl/13 결과 — E13 디스크 상세 · **PASS** (시도 3회)

| # | 판정 | 근거 |
|---|---|---|
| 1~5 | PASS | `logs/go-test.log`, `logs/live-cli.log` |
| 6 | PASS(부분) | API/UI 구현. 실제 Finder 창 열림은 자동 검증 안 함(GUI) |
| 7~8 | PASS | `logs/live-cli.log` |
| 9 | PASS | `logs/e2e.log`, `logs/01-overview-smart.png` ~ `04-duplicates.png` |

시도 이력
1. FAIL — E2E `.raw` 셀 단언이 부분 일치로 여러 셀 매칭. 단언 `exact` + Largest files 표 레이아웃(td 에 truncate) 수정.
2. FAIL — SMART 를 폴더 경로로 조회해 `none`. `MountOf` 로 OS 볼륨 해석 후 조회. diskutil `IORegistryEntryName` 이 모델명이 아니라 제거.
3. PASS. 부수 발견·수정: CLI `parseSize("1")` = 1000 (단위 없는 숫자에 k 적용되던 P1 버그) → 바이트로. 테스트 케이스 추가.

한계: 이 Mac 에 smartmontools 없음 → 속성·온도는 파서 단위 테스트(샘플 JSON)로만 검증. 온도 추이 그래프는 단일 스냅샷 캐시만 있어 미구현(P3).
