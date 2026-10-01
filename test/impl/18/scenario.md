# impl/18 — E18 Agent 등록·mTLS (FR-N01·S03·S07·N09)

| # | 수용 기준 | 방법 |
|---|---|---|
| 1 | Controller 자체 CA(ECDSA P-256, 키 0600 파일) + Agent 리스너 TLS 1.3 전용 | `pki`, live |
| 2 | 1회용 토큰 `CTM1.<secret>.<CA sha256>` (해시만 저장, TTL, 재사용 거부), `continuum node token` 이 Agent 명령 출력 | `TestAgentLifecycle`, live |
| 3 | Agent 가 토큰의 CA 지문으로 Controller 인증서를 고정 검증(MITM 차단), 불일치 시 거부 | `TestAgentLifecycle` (잘못된 pin) |
| 4 | 클라이언트 인증서: CN·URI SAN = node_id, 90일. Controller 는 등록된 현재 serial 과 일치할 때만 허용 | `auth` 미들웨어, `TestCertRotation` |
| 5 | 회전(FR-N09): 1/3 남으면 자동 갱신, 새 serial 발급 즉시 이전 인증서 거부 | `TestCertRotation` |
| 6 | 폐기: `continuum node revoke` → 세션 종료, 재연결 불가, 디스크 OFFLINE | `TestReconnectAndRevoke` |
