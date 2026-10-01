# impl/20 — E20 Cross-node 전송 (P3: cross-node 스트림·대역폭 제한, FR-N05 자동 재개)

| # | 수용 기준 | 방법 |
|---|---|---|
| 1 | 전송 엔진이 디스크별 노드 FS(`nodefs.FS`: Local / hub 원격)로 동작 — 로컬↔로컬은 기존 재개형 copyAtomic 유지 | 기존 transfer 테스트 전부 PASS |
| 2 | COPY Controller → 원격 디스크: 스트리밍 중 원본 BLAKE3, Agent 가 partial 을 fsync·재읽기 해시 → 일치 시에만 rename | `TestCrossNodeTransfers`, live |
| 3 | MOVE 원격 → 로컬, 원격 → 원격(Agent→Controller→Agent 중계): copy → verify → 원본 삭제 | TestCrossNodeTransfers, live attempt 2 |
| 4 | DRAIN 원격 디스크 → 다른 노드 디스크, 완료 시 볼륨에서 제거·DISABLED | TestCrossNodeTransfers, live attempt 2 |
| 5 | SCAN·VERIFY·충돌 해시·중복 탐색·SMART·벤치마크가 데이터가 있는 노드에서 실행 | TestCrossNodeTransfers(scan/verify), 코드(smart/bench) |
| 6 | 전송 중 노드 단절 → 작업 PAUSED("disk … offline"), 노드 복귀 → 자동 재개 → DONE (FR-N05) | TestCrossNodeTransfers, live (network disconnect/connect) |
| 7 | 대역폭 제한(`--bwlimit`)이 원격 스트림에도 적용 | live (2 MB/s 로 단절 시점 확보) |
