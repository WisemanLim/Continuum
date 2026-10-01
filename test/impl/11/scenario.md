# impl/11 — E11 SCAN·직접 편집 보정 (FR-K03·K05, NFR-I03·I04, PRD §11.6)

| # | 수용 기준 | 방법 |
|---|---|---|
| 1 | 디스크에 직접 추가한 파일(고아)이 카탈로그에 추가(orphan_policy=auto, review 는 state=review) | `TestScanReconcilesDirectEdits`, live CLI |
| 2 | 직접 만든 폴더가 논리 폴더로 보임 | 동일 + `continuum ls` |
| 3 | ONLINE 디스크에서 사라진 파일 → tombstone(external_delete) + 카탈로그 제거 | 동일 |
| 4 | 크기/mtime 변경 → 메타 갱신(modified) | 동일 |
| 5 | full 모드: 크기·mtime 같고 내용 다름 → corrupt | 동일 |
| 6 | 죽은 작업의 partial 은 파일로 취급 안 함, 보고; `--fix-partials` 시 삭제. 진행 중 작업 항목은 건드리지 않음 | 동일 |
| 7 | OFFLINE 디스크는 건너뜀(삭제로 간주 금지) | `TestScanSkipsOfflineDisk` |
| 8 | 재스캔 멱등 | 동일 |
| 9 | API `POST /v1/volumes/{v}/scan {full,disk,fix_partials}`, CLI `continuum scan <vol> [--full] [--disk] [--fix-partials]` | `TestScanAndConflictsAPI`, live CLI |
| 10 | 주기 스캔: `scan_interval_min` 경과 볼륨을 Background 틱(30s)에서 증분 스캔, `last_scan_at` 갱신 | `ScanDue` (코드) + last_scan_at 테스트 |
