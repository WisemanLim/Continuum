# impl/20 결과 — E20 Cross-node 전송 · **PASS** (시도 2회)

| # | 판정 | 근거 |
|---|---|---|
| 1 | PASS | `go test ./internal/transfer` (기존 로컬 경로 회귀) |
| 2~4, 6 | PASS | `logs/go-test.log` (TestCrossNodeTransfers, -race), `logs/live-docker.log` |
| 5 | PASS(scan/verify) · 코드(smart/bench) | |
| 7 | PASS | live: 2 MB/s 제한으로 단절 시 PAUSED, 재연결 후 `job.auto_resumed` → DONE |

시도 이력
1. 부분 FAIL(시험) — `docker restart ctm-agent-1` 이 컨테이너 명령의 `enroll`(이미 쓴 토큰)을 다시 실행해 종료 → agent-1 OFFLINE 상태라 Agent→Agent 구간 미검증. 제품 보완: `continuum-agent run --controller --token` 은 미등록일 때만 등록(재시작 안전).
2. PASS — Mac→agent-1 복사, agent-1→agent-2 이동(중계), agent-1 drain(→mac-1), verify 14/14, full scan 변화 0.

한계: 원격 전송은 파일 단위 재시작(로컬 복사의 partial 오프셋 재개는 원격 미지원). Agent↔Agent 직접 경로 없음(Controller 중계 — 대역폭은 Controller 링크에 의존). Windows Agent 경로 결합은 Controller 의 filepath 규칙 사용(같은 OS 계열 가정).
