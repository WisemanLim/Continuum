# impl/25 — E25 보안 삭제 Wipe (FR-D22 파일 단위, FR-S05)

| # | 수용 기준 | 방법 |
|---|---|---|
| 1 | data root 안 모든 일반 파일을 난수로 N회(1~7) 덮어쓰고 매 회 fsync → 삭제 → 빈 폴더·data root 제거. 심볼릭 링크는 따라가지 않음 | TestWipe (열린 inode 의 내용이 원문을 포함하지 않음) |
| 2 | data root 밖 파일은 건드리지 않음 | TestRemoteWipe |
| 3 | 볼륨에 속한 디스크는 거부(drain 먼저), 확인값이 디스크 이름과 정확히 같아야 함(서버 측 FR-S05), OFFLINE 거부 | TestRemoteWipe, live |
| 4 | 원격 노드 디스크는 Agent 에서 실행 | TestRemoteWipe |
| 5 | 완료 후 카탈로그 객체 삭제·디스크 등록 해제·감사 기록 | live (`disk.wipe … 3 passes`) |
| 6 | CLI `continuum disk wipe <disk> --passes N` (이름 타이핑/--yes), UI 디스크 상세 "Wipe…" (볼륨 밖 디스크만) | live, e2e |
