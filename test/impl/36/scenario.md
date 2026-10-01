# impl/36 — E36 Agent 측 OpenTelemetry 트레이스 (FR-O01, E30 잔여)

| # | 수용 기준 | 방법 |
|---|---|---|
| 1 | Controller → Agent 명령마다 hub CLIENT span(`agent <op>`, node·op 속성, 실패 시 error status), `Command.traceparent` 로 전달 | TestCommandTracePropagation |
| 2 | Agent 가 같은 trace 를 이어 SERVER span 생성(부모 = hub span), 오류 결과는 error status | TestCommandTracePropagation |
| 3 | 호출자 span(HTTP 요청 등)의 자식으로 연결 → 한 요청이 Controller·Agent 를 가로지르는 한 트레이스 | TestCommandTracePropagation, live |
| 4 | Agent 바이너리가 표준 `OTEL_*` env 로 내보내기(기본 service.name `continuum-agent`), 미설정 시 no-op | live |
| 5 | 구버전 호환(traceparent 선택 필드) | 기존 hub/agent 시험 |
