# impl/7 — E7 CLI `continuum` / `ctm` (FR-C01~C10, PRD §9)

| # | 케이스 | 방법 | 기대 |
|---|---|---|---|
| 1 | disk attach/list | `TestCLIEndToEnd` + 실바이너리 | 사전 검사 10항목 출력, `-q` 는 ID 만, `--unassigned` |
| 2 | volume create/move/order | 같은 테스트 | 3 disks, portable 경고, 이름으로 `--before` 재정렬 |
| 3 | plan 출력 | `plan copy` | PRD §9.3 형식: Source / Placement / Planned placement / `(excluded: role=portable)` / Result |
| 4 | plan 우선 확인 | `copy` (비대화형, `--yes` 없음) | 종료 2 + "--yes" 안내 (FR-C04) |
| 5 | copy·강제 순번 | `copy --yes`, `--target-ordinal 2` | DONE, locate → ordinal 2 |
| 6 | ls·locate·물리 뷰 | `ls`, `ls --physical --ordinal 2`, `locate` | 논리 목록·물리 경로 (FR-C09) |
| 7 | mkdir/mv/rm/trash | 같은 테스트 | 휴지통 5건, `rm --permanent` 은 `--yes` 없으면 거부 |
| 8 | verify·job·activity | 같은 테스트 | mismatched=0, job list DONE, audit chain intact |
| 9 | 출력 형식 | `--format json` | JSON |
| 10 | 종료 코드 | `TestExitCodes` | 사용법 2 · not_found 1 · 공간 부족 3 · 오프라인/없는 순번 6 · 충돌 7 · Controller 불통 6 |
| 11 | 크기 파싱 | `TestParseSize` | 100GiB·1GB·500M·1.5TiB, `C:\data` 는 볼륨 경로 아님 |
| 12 | 오프라인 로컬 명령 | `disk discover --local` | Controller 없이 탐색 (FR-C10) |
