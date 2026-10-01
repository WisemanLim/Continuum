# impl/14 결과 — E14 Drain·Rebalance · **PASS** (시도 2회)

| # | 판정 | 근거 |
|---|---|---|
| 1, 2, 4 | PASS | `logs/go-test.log` (-race) |
| 1, 4, 6, 7 | PASS | `logs/live-cli.log` (attempt 2) |
| 3, 5 | PASS(코드) | 전용 테스트 없음 — 기존 conflict/busy 경로 재사용 |
| 8 | PASS | `logs/e2e.log`, `logs/01-rebalance-plan.png`, `02-drain-plan.png`, `03-transfers.png` |

시도 이력
1. 부분 FAIL — 계획의 "remaining after" 가 0 B (재계산용 요약에 잔여량 누락). 실제 여유 − 유입 + 유출로 계산. 라이브 스크립트가 `-q` 로 seed 복사 완료를 기다리지 않아 첫 plan 이 0 files (정상 동작: 진행 중 파일 제외).
2. PASS.

가정: 시간창 지정(FR-P10 "시간창")은 미구현 — 대역폭 제한(`--bwlimit`)만. 스케줄러 도입 시(P3) 추가.
