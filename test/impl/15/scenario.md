# impl/15 — E15 SMB 접근점 (FR-A01~A05·A07·A08·A09)

| # | 수용 기준 | 방법 |
|---|---|---|
| 1 | 이미지 `continuum-samba:local` (debian bookworm + samba 4.17 + mergerfs 2.33, `deploy/samba/Dockerfile`) — `make local-build` 에서 빌드 | `docker build` |
| 2 | 볼륨 share = mergerfs(디스크를 ordinal 순서 branch, auto-place ONLINE = RW, 그 외 NC/RO, category.create=ff, minfreespace=볼륨 예약) + 디스크별 share. SMB2_10 이상, catia·fruit·streams_xattr, veto(.trash, volume.json, partial, probe). OFFLINE·다른 노드 디스크 제외, blocked 볼륨 미노출 | `TestRender` |
| 3 | 접근 모드(FR-A09): read-centric = 읽기 전용, full-direct = 읽기/쓰기 + SCAN 5분, blocked = 미노출 | `TestRender`, live attempt 5, E2E |
| 4 | macOS Finder 경로(mount_smbfs)로 읽기·쓰기·xattr·rename·delete·`cp -R`, 한글 경로 | `logs/live.log` attempt 4 |
| 5 | SMB 쓰기는 ordinal 1(landing) 에 기록되고 SCAN 이 카탈로그에 추가(FR-A05); 외부 삭제 폴더는 SCAN 이 카탈로그에서 정리(논리 전용 폴더 보존) | live attempt 4·5, `TestScanPrunesVanishedFolders` |
| 6 | 상태(FR-A08): 실행 여부·클라이언트 수(smbstatus)·share URL, 볼륨 카드 표시 | live, E2E |
| 7 | API `GET /v1/access`, `GET /v1/access/credentials`, `POST /v1/access/enable|disable|reload`; 볼륨/디스크 변경 시 Background 30s 틱이 설정 해시 비교 후 자동 재생성 | live |
| 8 | CLI `continuum access status|credentials|enable|disable|reload` | live attempt 5 |
| 9 | 비밀번호: 최초 enable 시 난수 생성 → settings, 컨테이너에는 0600 파일로만 전달 (env/inspect 노출 없음) | 코드 |
