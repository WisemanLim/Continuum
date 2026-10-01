# impl/12 — E12 Conflict Center (FR-K04·G15·A07)

| # | 수용 기준 | 방법 |
|---|---|---|
| 1 | 같은 논리 경로가 2개+ 디스크에 있으면 탐지. 동일(크기+BLAKE3) = duplicate, 다르면 conflict | `TestConflictDetectAndResolve` |
| 2 | 재스캔 시 conflicts 행 중복 생성 없음, 사라진 충돌은 자동 resolved | 동일 |
| 3 | 읽기 우선순위: 최저 ordinal 사본이 visible, 나머지는 state duplicate/conflict | 동일 |
| 4 | resolve keep lowest/newest/<disk>: 나머지는 휴지통(복원 가능), both: "name (disk).ext" 로 이름 변경 | 동일 + live CLI |
| 5 | 중복 일괄 해결(최저 ordinal 유지) | 동일 |
| 6 | API: `GET /v1/volumes/{v}/conflicts`, `POST /v1/conflicts/{id}/resolve {keep}`, `POST /v1/volumes/{v}/conflicts/resolve-duplicates`; 잘못된 keep → 400 | `TestScanAndConflictsAPI` |
| 7 | CLI `continuum conflict list|resolve|resolve-duplicates` | `logs/live-cli.log` |
| 8 | UI: 볼륨 상세 Conflicts 탭(Scan now / Full scan / Resolve duplicates / Keep lowest·newest·both), Overview 충돌 타일 실제 값 | `e2e/conflicts.e2e.ts` (실제 Chrome) + 스크린샷 |
