# impl/21 결과 — E21 OS 서비스 설치 · **PASS** (시도 2회)

| # | 판정 | 근거 |
|---|---|---|
| 1~3, 6 | PASS | `logs/live-macos.log` |
| 4 | PASS(렌더링) | `logs/live-linux.log`, `logs/go-test.log` — systemd 가 있는 Linux 에서 `enable --now` 실측은 안 함 (컨테이너에 systemd 없음) |
| 5 | PASS(빌드) | Windows 실기 없음 |

시도 이력
1. 부분 FAIL — `uninstall` 직후 launchd 가 아직 job 을 보유("still loaded"): `launchctl bootout` 이 비동기. uninstall 이 상태 조회가 실패할 때까지 최대 10초 대기하도록 수정.
2. PASS — install → running → uninstall → unloaded, 정의 파일 0.

시험은 plist 를 스크래치 폴더(`--unit-dir`)에 두고 실제 `launchctl bootstrap gui/$UID` 로 기동 — 사용자 `~/Library/LaunchAgents` 는 건드리지 않음.
