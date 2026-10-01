# impl/28 — E28 P2 잔여 (FR-K05·A02·A04·D13·P10)

| # | 수용 기준 | 방법 |
|---|---|---|
| 1 | FS 이벤트 감시(fsnotify: inotify/FSEvents·kqueue/ReadDirectoryChangesW): 로컬 ONLINE 디스크의 모든 폴더 감시(새 폴더 자동 추가), 3초 무변화 후 해당 디스크만 증분 SCAN, partial·휴지통·._ 무시, 실행 중 작업이 쓰는 디스크는 작업 후로 미룸, 네트워크 FS·원격 디스크는 주기 스캔 유지, `CONTINUUM_WATCH=off` | TestWatcherReflectsDirectEdits (macOS·Linux), live (mergerfs 경유 쓰기) |
| 2 | macOS SMB 자동 재연결 LaunchAgent(로그인·5분·네트워크 변경 시 `mount volume`), 자격 증명은 로그인 키체인 internet password | TestLocalMountAndAutoconnect, `plutil -lint` + `osacompile` |
| 3 | Linux 로컬 mergerfs 모드: 이 호스트 디스크의 순번 합집합(1회 명령·fstab·systemd .mount), `continuum access local-mount <vol> [--print]`, `GET /v1/volumes/{v}/local-mount` | TestLocalMountAndAutoconnect, live container |
| 4 | SMART 온도 추이: 갱신마다 스냅샷 이벤트, 1시간 주기 전체 갱신, `GET /v1/disks/{id}/smart/history`, 디스크 상세 스파크라인 | TestSmartHistory, 코드 |
| 5 | 작업 시간창(`--window 22:00-06:00`, 자정 넘김): 창 밖이면 QUEUED 대기, 열리면 시작, 닫히면 PAUSED(partial 보존), 다음 창에서 재개 | TestTimeWindow |
