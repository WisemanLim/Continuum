# impl/14 — E14 Drain·Rebalance (FR-D10·P10)

| # | 수용 기준 | 방법 |
|---|---|---|
| 1 | DRAIN: 시작 즉시 디스크 DRAINING(신규 쓰기 차단) → 카탈로그 조회 → 남은 디스크로 배치(`--start-ordinal` 반영) → copy→verify→delete → 빈 디스크 확인 → 볼륨에서 제거·DISABLED, 순번 재정렬 | `TestDrainRemovesDisk`, live CLI |
| 2 | 공간 부족 시 거부(insufficient_space), 디스크 ONLINE 복귀. 취소·실패 시 ONLINE 복귀. 재개/재시도/복구 시 다시 DRAINING | `TestDrainRefusedAndCancel` |
| 3 | 충돌(다른 디스크에 같은 경로) 시 기본 on_conflict=rename — 사본 유실 없음 | 코드(기본값) |
| 4 | REBALANCE: 범위(볼륨/폴더)를 비었다고 가정하고 현재 순번·정책으로 재계산, 위치가 다른 파일만 이동, 낮은 순번 대상 먼저. 재실행은 no-op | `TestRebalance`, live CLI |
| 5 | 진행 중 작업이 쓰는 파일은 제외 (PRD §11.8) | `busyPaths` 재사용 |
| 6 | 계획 미리보기: `POST /v1/plan {type: DRAIN|REBALANCE}`, 남은 공간 = 실제 여유 − 유입 + 유출 | live CLI |
| 7 | API `POST /v1/jobs {type}`, `POST /v1/disks/{id}/drain`; CLI `continuum volume drain|rebalance`, `continuum plan drain|rebalance` | live CLI |
| 8 | UI: 볼륨 Disks 보드 Drain…/Rebalance…, 디스크 상세 Drain… (계획 → 시작) | `e2e/relocate.e2e.ts` |
