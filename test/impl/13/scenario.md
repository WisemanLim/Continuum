# impl/13 — E13 디스크 상세 (FR-D27·D13·D14·D15·D16)

| # | 수용 기준 | 방법 |
|---|---|---|
| 1 | SMART: smartctl -a -j(ATA/NVMe) 파싱, 없으면 diskutil(macOS)/Get-Disk(Windows) 상태만, USB 미지원은 "Not Supported"+안내. 결과 disks.smart_json 캐시, `refresh` 시 재조회, 실패 예측 시 disk.smart_warning 이벤트 | `TestParseSmartctl`, `TestReadSmartNeverFails`, `TestSmartCacheAndBench`, live CLI |
| 2 | 폴더 기반 디스크도 SMART 는 그 폴더를 담은 OS 볼륨으로 조회 | live CLI (`Verified (diskutil)`) |
| 3 | 벤치마크: data root 임시 파일 순차 쓰기(fsync)/읽기, 4K 랜덤 읽기, 캐시 우회(macOS F_NOCACHE, Linux fadvise), 파일 삭제. ONLINE + 여유 2× 조건, 결과 카드 이력 | `TestRunBench`, `TestSmartCacheAndBench`, E2E |
| 4 | 사용량: 카탈로그 기반 폴더 트리(깊이 3)·상위 N·확장자 분포, 디스크/볼륨 범위 | `TestUsageAndDupes`, E2E, live CLI |
| 5 | 중복: size → head/tail 64KiB 부분 해시 → 전체 BLAKE3, 같은 논리 경로(충돌)는 제외, 디스크 범위 필터. 삭제는 사용자 확정 + 휴지통 | `TestUsageAndDupes`, E2E(Move to trash) |
| 6 | Finder/Explorer 열기 (`POST /v1/disks/{id}/reveal`, 로컬 노드 디스크만) | 코드 + UI 버튼 |
| 7 | API: `GET /v1/disks/{id}/smart|usage|dupes|bench`, `POST /v1/disks/{id}/bench|reveal`, `GET /v1/volumes/{v}/usage|dupes` | E2E·live |
| 8 | CLI: `continuum disk smart|usage|dupes|bench|open` | `logs/live-cli.log` |
| 9 | UI: `#/disks/<id>` (Overview·SMART / Benchmark / Usage 트리맵 / Duplicates), 노드·볼륨 카드에서 링크 | `e2e/disk-detail.e2e.ts` + 스크린샷 |
