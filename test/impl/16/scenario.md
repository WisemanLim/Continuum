# impl/16 — E16 운영·관측 (FR-O01·O04·D26·G16)

| # | 수용 기준 | 방법 |
|---|---|---|
| 1 | `/metrics` Prometheus text: 볼륨 용량, 디스크 용량/상태/up, 작업 상태별 수·큐 깊이·처리 bytes/files·실패 files, 충돌 수, 접근점 up/clients, build_info. 토큰 설정 시 토큰 필요 | `TestOpsEndpoints`, live |
| 2 | 진단 번들 zip (`GET /v1/diagnostics?mask=1`): info·nodes·volumes·disks·jobs·events·audit·settings·access. 비밀번호/토큰/웹훅 URL 은 항상 제거, 기본 경로·호스트 마스킹(해시 토큰) | `TestOpsEndpoints`, live |
| 3 | 알림 이벤트: disk.offline, disk.capacity(85/95% 교차 시), disk.smart_warning, disk.drained, job.failed, job.paused_need_space, conflict.detected, access.failed | 코드 + 테스트 |
| 4 | 웹훅(Slack/Discord/generic), 5s 폴링, 과거 이벤트 재전송 없음, 1회만 전송, 테스트 전송 | `TestNotifierTick`, `TestOpsEndpoints`, E2E |
| 5 | 설정 API `GET/PUT /v1/settings` (허용 키만, 형식 검증), CLI `continuum settings get|set|test-notify`, `continuum diag bundle` | live CLI |
| 6 | UI: 헤더 알림 센터(미읽음 수, 토스트, 모두 읽음, 데스크톱 알림 opt-in), 설정 › Notifications / Diagnostics | `e2e/ops.e2e.ts` |
