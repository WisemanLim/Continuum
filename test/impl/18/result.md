# impl/18 결과 — E18 Agent 등록·mTLS · **PASS** (시도 1회)

| # | 판정 | 근거 |
|---|---|---|
| 1~6 | PASS | `logs/go-test.log` (TestAgentLifecycle, TestCertRotation, TestReconnectAndRevoke), `logs/live-docker.log` (Docker linux/arm64 Agent 실등록, 토큰 재사용 거부) |

설계 메모: 제어는 connect-go 양방향 스트림(JSON 코덱 — protoc 생성 단계 없음, `TestBidiJSONOverH2`), 파일 바이트는 별도 mTLS HTTP 스트림(blob)으로 Controller 경유 중계 — Agent 가 NAT 뒤에 있어도 동작.
가정: CA 키는 Controller 호스트 파일(0600, `CONTINUUM_PKI_DIR`). OS 키체인 저장(FR-S06)·step-ca 연동은 미구현.
