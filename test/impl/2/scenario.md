# impl/2 — E2 디스크 탐색·Attach (FR-D01~D07·D09·D11·D12·D20, PRD §10.2·10.3)

| # | 케이스 | 테스트 | 기대 |
|---|---|---|---|
| 1 | OS 디스크 탐색 | `TestDiscoverSmoke` | macOS `/` 탐색, APFS Volume UUID·라벨·버스 채움, identity 비어있지 않음 |
| 2 | 사전 검사 10항목 + 마커 | `TestAttachPreflightAndMarker` | 체크 10개, `.continuum/volume.json` 에 disk_id·controller_id |
| 3 | 동일 경로 재attach | 같은 테스트 | 중복 등록 없이 marker 로 재식별 (DP01) |
| 4 | 경로 변경 후 재연결 | `TestReattachAfterMove` | 폴더 이동 시 OFFLINE, 새 경로 attach 시 같은 disk_id 로 ONLINE (FR-D20) |
| 5 | 타 Controller 마커 | `TestPreflightRejectsForeignController` | 검사 6 fail, `adopt` 시 허용 |
| 6 | 예약 유효성 | `TestReserveMustBeBelowHalf` | min_free ≥ total×50% → 검사 5 fail |
| 7 | 즉시 detach | `TestDetachMarksObjectsOffline` | DISABLED, 객체 offline, 객체 있는 디스크 unregister 거부, enable 시 복구 |
