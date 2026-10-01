# impl/32 결과 — E32 Agent ↔ Agent 직접 전송 · **PASS** (시도 2회)

| # | 판정 | 근거 |
|---|---|---|
| 1 | PASS | `logs/go-test.log`, `logs/live-direct.log` — drain a1(Agent A) → b1(Agent B) 4 파일, `continuum_direct_transfers_total 4`, relay fallback 0, 8 MiB `cmp` 동일 |
| 2 | PASS | TestPeerEndpointSecurity — 6가지 위조 거부 |
| 3 | PASS | TestDirectAgentToAgent — 일시정지 시 partial 1 MiB 초과 남김, 재개 후 `resumed=1`, 남은 partial 0 |
| 4 | PASS | 손상 partial → verify failed + 삭제 |
| 5 | PASS | 2 MiB/s 제한으로 6 MiB 파일 중간 일시정지 가능 |
| 6 | PASS | TestDirectFallsBackToRelay (원본 Agent peer 없음 → 중계, direct=0) |
| 7 | PASS | live: 두 Agent `--peer-listen 127.0.0.1:1847x` |
| 8 | PASS | live `/metrics` |
| race | PASS | `logs/go-test-race.log` (hub·agent·transfer) |

시도 이력
1. FAIL(시험 코드) — 시험 Agent 의 context 를 정리하지 않아 실패 후 httptest 서버 Close 가 스트림을 기다리며 정지, 광고 URL 을 호스트명으로 만들어 포트 추출이 취약. 수정: `t.Cleanup(cancel)`, `PeerListen` 이 `:0` 광고 URL 의 포트를 실제 포트로 채움(제품 코드 개선).
2. PASS.

설계 메모
- PRD 의 gRPC `TransferService.Push/Pull` 대신 mTLS HTTPS GET(+trailer) — P3 Agent 스트림과 같은 "JSON 코덱 + HTTP blob" 방식, protoc 불필요. 대상 Agent 가 pull(규칙 ②③④ 공통), Controller 로컬 노드 ↔ Agent 는 기존 경로(Controller 가 곧 해당 노드의 Agent).
- 진행률은 파일 단위(Agent pull 이 끝날 때 반영). 파일 내부 진행률 스트리밍은 잔여.
- 티켓은 15분 동안 같은 경로에 재사용 가능(재개용). 폐기된 노드는 새 티켓을 받지 못함.
