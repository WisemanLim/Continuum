# impl/21 — E21 OS 서비스 설치 (FR-N04)

| # | 수용 기준 | 방법 |
|---|---|---|
| 1 | `continuum-agent service print|install|uninstall|status [--system] [--root …]` | live macOS |
| 2 | macOS launchd plist (user: `~/Library/LaunchAgents`, system: `/Library/LaunchDaemons`), RunAtLoad·KeepAlive·로그 경로, `plutil -lint` 통과, `launchctl bootstrap/bootout` | TestServiceDefinitions, live macOS (실제 bootstrap) |
| 3 | 프로세스가 죽으면 서비스 관리자가 재시작 | live macOS (kill → 새 pid, 노드 ONLINE 유지) |
| 4 | Linux systemd unit (user: `~/.config/systemd/user`, system: `/etc/systemd/system` + 하드닝), `systemctl [--user] enable --now` | TestServiceDefinitions, live linux 컨테이너 `service print --system` |
| 5 | Windows: Service Control Manager 핸들러(Stop/Shutdown → 정상 종료), `sc.exe create/failure/start` | 코드 + windows 크로스 빌드 |
| 6 | 미등록 상태에서 install 거부, uninstall 은 상태·인증서 보존 | live macOS |
