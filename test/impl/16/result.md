# impl/16 결과 — E16 운영·관측 · **PASS** (시도 3회)

| # | 판정 | 근거 |
|---|---|---|
| 1, 2 | PASS | `logs/go-test.log`, `logs/live-cli.log` (마스킹 번들에 원경로 0건, 웹훅 URL 0건) |
| 3, 4 | PASS | `logs/go-test.log` (TestNotifierTick), E2E 웹훅 수신 |
| 5 | PASS | `logs/live-cli.log` (잘못된 format → rc 2, 도달 불가 웹훅 → rc 6) |
| 6 | PASS | `logs/e2e.log`, `01-notification-center.png`, `02-settings.png` |

시도 이력
1. FAIL — 테스트 알림 POST 가 201 반환(기본 규칙) → 200 으로. 진단 JSON 이 `<` 를 `<` 로 이스케이프 → SetEscapeHTML(false).
2. FAIL — Notifier 가 같은 ms 안의 이벤트를 놓침: `store.NewID` 가 ms 내 비단조 ULID → `ulid.Make()`(프로세스 내 단조)로 변경. 모든 ID 정렬이 생성 순서와 일치하게 됨.
3. PASS. 부수: zip 항목 시각(1980) → 생성 시각.

미구현(가정): 이메일 알림(SMTP 자격 증명 필요, P3), Grafana 대시보드 JSON, OTel 트레이스(FR-O02).
