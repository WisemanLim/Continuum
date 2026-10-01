# impl/32 — E32 Agent ↔ Agent 직접 전송 (PRD D36, §7.4 규칙 ①~④, §7.6 TransferService)

| # | 수용 기준 | 방법 |
|---|---|---|
| 1 | 원격 → 원격 복사는 Controller 가 바이트를 중계하지 않음: 대상 Agent 가 원본 Agent 의 peer 엔드포인트(mTLS, 양쪽 모두 Controller CA 인증서)에서 pull. 같은 노드면 그 Agent 가 로컬 복사(규칙 ①) | TestDirectAgentToAgent, live (`continuum_direct_transfers_total`, relay fallback 0) |
| 2 | 세션 티켓: Controller CA 키로 서명(src·dst·path·만료 15분), 원본 Agent 가 오프라인 검증 — 티켓 없음/다른 경로/다른 수신자/만료/외부 CA 서명 → 403, 외부 CA 클라이언트 인증서 → TLS 실패 | TestPeerEndpointSecurity |
| 3 | 재개 오프셋: 대상 partial(`…continuum-partial-<job>.w`) 길이부터 이어받기, 일시정지 시 Agent 의 pull 을 취소(OpCancel)하고 partial 보존 → 재개 시 이어받음 | TestDirectAgentToAgent (6 MiB, 2 MiB/s, 일시정지·재개, resumed=1), TestPeerEndpointSecurity |
| 4 | 검증: 원본이 전송하며 계산한 전체 파일 BLAKE3 를 HTTP trailer 로 전달, 대상이 디스크에서 다시 읽어 비교 → 불일치 시 partial 삭제·verify_failed. 카탈로그 checksum 기록 | TestPeerEndpointSecurity (손상 partial), TestDirectAgentToAgent |
| 5 | 대역폭 제한(`--bwlimit`) 이 Agent 쪽에 적용 | TestDirectAgentToAgent (일시정지 가능한 속도) |
| 6 | 폴백: peer 엔드포인트 없음·구버전 Agent·연결 불가 → Controller 중계로 자동 전환, `CONTINUUM_DIRECT=off` 로 강제 중계 | TestDirectFallsBackToRelay |
| 7 | Agent 설정: `continuum-agent run --peer-listen :8444 --peer-url …` (env `CONTINUUM_AGENT_PEER_ADDR`·`CONTINUUM_AGENT_PEER_URL`, `off` 비활성), Hello 의 `peer_url`·capability `peer|pull|cancel` | live |
| 8 | 관측: `/metrics` `continuum_direct_transfers_total`, `continuum_direct_resumed_total` | live |
