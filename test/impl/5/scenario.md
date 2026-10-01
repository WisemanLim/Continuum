# impl/5 — E5 전송 엔진 (FR-T01~T15·T17·T22, FR-P07·P09·P11, NFR-I01~I03·A01·A03, PRD §11)

실파일 E2E (`internal/transfer/transfer_test.go`). 디스크 = 임시 폴더, 용량 = 실제 사용량이 반영되는 가짜 statfs.

| # | 케이스 | 테스트 | 기대 |
|---|---|---|---|
| 1 | **FR-P08 실복사**(1/1024 축소) | `TestCopyFRP08EndToEnd` | 25/15 MiB, 예약 1 MiB, 30 MiB(폴더 10·파일 3,000) → 물리적으로 ≈24/6 MiB, 카탈로그 3,000 건 전부 BLAKE3 검증, 예약 0 복귀, partial 잔존 0, VERIFY 3,000 OK, locate 최저 순번, 감사 체인 intact, idempotency_key 동일 job |
| 2 | 일시정지·재개 | `TestPauseResume` | PAUSED 시 partial·예약 유지, 재개 후 오프셋 이어받기, 해시 일치 |
| 3 | 취소 | `TestCancel` | partial 삭제·예약 해제·원본 불변 |
| 4 | 재시작 복구 | `TestCrashRecovery` | RUNNING 중단 job 이 새 엔진 Recover 로 재개·완료, 예약 재계산 |
| 5 | **장애 주입: 디스크 분리** | `TestDiskRemovedPausesJob` | 복사 중 분리 → PAUSED(사유), 원본 불변, 분리 상태 신규 복사 `offline`, 재연결 후 재개·해시 일치 |
| 6 | 공간 부족 → 디스크 추가 | `TestNeedSpaceThenAddDisk` | 기본 fail(`insufficient_space`), `wait` 시 PAUSED_NEED_SPACE, 3번 추가 후 재개 → 3번에 배치 |
| 7 | 충돌 정책 | `TestConflictPolicies` | skip / rename(`a (2).txt`) / newer(교체·구버전 skip) |
| 8 | 이동·이름변경·폴더·삭제·휴지통 | `TestMoveRenameMkdirDeleteTrash` | 같은 디스크 move=rename, 강제 순번 move=copy→verify→delete, 폴더 rename, 지연/즉시 mkdir, 휴지통→복원→재삭제→purge, 경로 탈출 거부 |
| 9 | 변조 탐지 | `TestVerifyDetectsTamper` | 디스크 직접 변조 → VERIFY mismatched |
| 10 | 동시성 | `go test -race` | data race 없음 |
