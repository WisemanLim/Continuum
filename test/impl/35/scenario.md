# impl/35 — E35 직접 전송 파일 내부 진행률 (FR-T09, E32 잔여)

| # | 수용 기준 | 방법 |
|---|---|---|
| 1 | Agent ↔ Agent pull 중 대상 Agent 가 250 ms 마다 `Progress{id, bytes}`(파일 내 절대 길이)를 스트림으로 보고 | 코드, TestDirectAgentToAgent |
| 2 | Hub 가 명령 id 로 콜백을 찾아 엔진 진행률(바이트·속도·ETA → SSE/CLI/UI)에 반영, 파일이 끝나기 전에 job `bytes_done` 증가 | TestDirectAgentToAgent: RUNNING 중 bytes_done > 1 MiB (작은 파일 3개 합으로는 불가) |
| 3 | 재개 시 이어받은 오프셋부터 보고, 중복 집계 없음(단조 증가 delta) | 코드 (advance) |
| 4 | 구버전 호환: Progress·traceparent 필드는 선택 — 모르는 쪽은 무시 | JSON 선택 필드 |
| 5 | 시험 검증력: Agent 보고를 끄면 시험이 실패 | 변이 시험 |
