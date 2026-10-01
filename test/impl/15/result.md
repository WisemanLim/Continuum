# impl/15 결과 — E15 SMB 접근점 · **PASS** (시도 5회)

| # | 판정 | 근거 |
|---|---|---|
| 1 | PASS | Samba 4.17.12, mergerfs 2.33.5 |
| 2, 3 | PASS | `logs/go-test.log` (TestRender) |
| 4, 5 | PASS | `logs/live.log` attempt 4 (write/xattr/rename/delete/cp -R 128 entries, landing d1, SCAN added 65·removed 99), attempt 5 (Asia/ 폴더 정리) |
| 3, 6, 8 | PASS | `logs/live.log` attempt 5 (read-centric 쓰기 거부), `logs/e2e.log`, `01-access-tab.png`(자격 증명 마스킹), `02-volume-card.png` |
| 7 | PASS | live reload/disable, 컨테이너 잔존 0 |
| 9 | PASS(코드) | |

시도 이력
1. FAIL — 쓰기 Permission denied. Docker Desktop(virtiofs)은 bind mount 를 root 소유로 노출 → `force user` 가 쓸 수 없음. start.sh 가 data root 소유 uid/gid 로 SMB 사용자 생성(`useradd -o`).
2. FAIL — 쓰기 OK, xattr 미저장(AppleDouble `._` 폴백)·rename `Operation not supported`. 원인: virtiofs 마운트가 `no_xattr` → streams_xattr / DOS 속성 저장 실패.
3. FAIL — xattr 미지원 감지 시 streams_depot 로 전환 → 동작하나 inode 불일치로 `lost-<n>` 폴더 대량 생성(로그 정리). 부적합.
4. PASS — xattr 미지원이면 `vfs objects = catia`, `ea support = no`, `store dos attributes = no`, mergerfs `xattr=nosys`. macOS 는 exFAT 과 같이 `._` AppleDouble 사용(SCAN 이 이미 무시). 발견: 외부 삭제 폴더 dir 객체 잔존 → SCAN 정리 추가(논리 전용 폴더는 `state=virtual` 로 보존).
5. PASS — CLI·모드 전환·disable 정리.

한계/가정
- Windows `\\host\share` 는 포트 445 고정 → LAN 에서 쓰려면 `continuum access enable --bind 0.0.0.0 --port 445` (호스트 445 가 비어 있어야 함). 이 Mac 에서는 Windows/Linux 클라이언트 실측 안 함(명령 안내만).
- macOS 자동 재연결 launchd 스크립트(FR-A02)·Linux 로컬 mergerfs 전용 모드(FR-A04 일부) 미구현.
- SMB 쓰기의 배치는 파일 단위 ff(첫 여유 branch) — tree-aware 정책·start/forced ordinal 은 앱/CLI 경로만 (FR-A05 문서화).
- 컨테이너 이름 `continuum-smb` 고정 — 한 호스트에 Controller 1개 가정.
