# impl/4 — E4 배치 엔진·Plan (FR-P01~P11, PRD §11.1)

| # | 케이스 | 테스트 | 기대 |
|---|---|---|---|
| 1 | **FR-P08 재현** | `TestFRP08SpilloverReproduction` | 25/15 GB, 예약 1 GB, 30 GB(폴더 10·파일 3,000) → ≈24 / 6 GB, 합계 보존, 예약 미침범 (fill-in-order·topdir-affinity) |
| 2 | 폴더 원자성 | `TestTopdirKeepsFoldersTogether` | topdir-affinity 시 최상위 폴더가 한 디스크 |
| 3 | 공간 부족 → 디스크 추가 | `TestPoolOutOfSpaceThenAddDisk` | `PoolOutOfSpace`, 3번 추가 후 충분 (FR-P09·P11) |
| 4 | 강제 순번 | `TestForcedOrdinal` | 지정 디스크만, 부족 시 `InsufficientSpaceOnForcedDisk`, 없는 순번 `ForcedTargetUnavailable` (R04) |
| 5 | 시작 순번·strict | `TestStartOrdinalAndStrict` | start_ordinal 2 부터, spillover off 면 첫 디스크만 (R06·FR-V17) |
| 6 | whole-tree-only | `TestWholeTreeOnly` | 단일 디스크 불가 → `NoSingleDiskCanHoldTree` |
| 7 | 배치 제외 | `TestExclusions` | portable·OFFLINE 제외 사유 표시, 강제 지정 시 portable 허용 (DP08) |
| 8 | most-free / balanced | `TestMostFreeAndBalanced` | 여유 최대 / 비율 최대 디스크 |
| 9 | 이름 규칙 | `TestNameWarnings` | NTFS 금지 문자·예약어·대소문자 충돌 경고, 자동 치환 `a_b_.txt` (FR-T06) |
