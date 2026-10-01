# impl/26 결과 — E26 네이티브 VFS (Linux FUSE) · **PASS** (시도 5회)

| # | 판정 | 근거 |
|---|---|---|
| 1~8 | PASS | `logs/go-test-linux.log` (linux/arm64 테스트 바이너리를 `--device /dev/fuse --cap-add SYS_ADMIN` 컨테이너에서 실행), `logs/live-container.log` |
| 9 | PASS(코드) | `auth` 규칙 `/v1/vfs.*` → storage |

시도 이력
1. FAIL — 쓴 파일의 카탈로그 크기 0: FUSE RELEASE 는 close(2) 이후 비동기 → close 안에서 동기 호출되는 FLUSH 에서 commit.
2. FAIL(시험) — 라이브 컨테이너가 `/data` 미존재로 SQLite 열기 실패 → DB 경로 `/tmp`.
3. FAIL — `cp -r` 이 심볼릭 링크 생성 실패(Operation not supported) → Symlink/Readlink 구현(배치 정책으로 디스크 선택).
4. FAIL — `cp -a` 가 ACL xattr 에서 "No data available" 대량 경고 → Setxattr 는 ENOTSUP. 배치 직전 로컬 디스크 statfs 갱신 추가. (넘김 미관찰: 시험에서 디스크별 예약 0 이 볼륨 예약보다 우선)
5. PASS — small 디스크 예약 1M, 2.2 MB 파일 후 다음 파일은 ordinal 2(big) 로.

한계: macOS(FSKit/macFUSE)·Windows(WinFsp) 미구현 — Windows 빌드는 명확한 오류 반환. 다른 노드 디스크의 파일은 목록에만 보이고 읽기 EIO(앱/CLI/SMB 사용). 파일 생성 시점에 디스크가 정해지므로 한 파일이 쓰는 도중 디스크가 차면 ENOSPC(파일은 쪼개지지 않음). xattr 미보존. 체크섬은 쓰기 때 계산하지 않음(verify/full scan 으로).
