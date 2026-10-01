# impl/12 결과 — E12 Conflict Center · **PASS** (시도 1회)

| # | 판정 | 근거 |
|---|---|---|
| 1~5 | PASS | `logs/go-test.log` |
| 6 | PASS | `logs/go-test.log` (TestScanAndConflictsAPI) |
| 7 | PASS | `logs/live-cli.log` |
| 8 | PASS | `apps/web/e2e/conflicts.e2e.ts` 1 passed (8.5s), `logs/01-conflicts.png`, `logs/02-resolved.png` |

가정: 해결로 치운 사본은 일반 삭제와 같은 휴지통(`reason=unlink`, `deleted_by=conflict-resolve`) — 복원/비우기 경로 재사용.
