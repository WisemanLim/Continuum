# impl/19 — E19 Agent 스트림·원격 디스크 (FR-N02·N03·N05·N06·D12)

| # | 수용 기준 | 방법 |
|---|---|---|
| 1 | Connect 스트림: hello → welcome(watch 목록·heartbeat 주기) → heartbeat(감시 경로 statfs) · inventory(마운트 + `--root` 폴더) · command/result | live, `TestAgentLifecycle` |
| 2 | 원격 Attach: Agent 에서 사전 검사 10항목·식별자 → 등록 → 마커를 Agent 가 기록, 용량은 heartbeat 로 갱신 | live (`/disks/d1` volume.json), TestAgentLifecycle |
| 3 | 로컬+원격 혼합 볼륨 | live (`mixed`: mac-1 + lin-d1) |
| 4 | heartbeat 3회 누락 → NODE_OFFLINE → 소속 디스크 OFFLINE, 원격 명령은 ErrNodeOffline | live (network disconnect), TestAgentLifecycle |
| 5 | 재연결: 지수 백오프(1s→60s), 같은 node_id 로 복귀, 디스크 ONLINE, 오프라인으로 PAUSED 된 작업 자동 재개 훅 | live (network connect), TestReconnectAndRevoke |
| 6 | 버전 협상: hello.protocol < MinProtocol 이면 FailedPrecondition, 응답은 min(agent, controller) | 코드 |
