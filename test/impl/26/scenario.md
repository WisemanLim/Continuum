# impl/26 — E26 네이티브 VFS · Linux FUSE (FR-A06, A07)

| # | 수용 기준 | 방법 |
|---|---|---|
| 1 | 볼륨을 단일 파일시스템으로 마운트(`continuum vfs mount <vol> <dir> [--read-only]`, `POST /v1/vfs/mount`), 해제·목록, Controller 종료 시 자동 해제 | live container |
| 2 | 읽기·목록: 카탈로그 기준, 같은 경로가 여러 디스크에 있으면 최저 순번 사본(FR-A07) | TestVFS |
| 3 | 새 파일·심볼릭 링크는 볼륨 배치 정책으로 디스크 선택(직전 statfs 반영) → 예약 미만이면 다음 순번으로 넘김 | TestVFS, live attempt 5 |
| 4 | 쓰기 후 close 시점에 카탈로그 반영(크기·mtime, 기존 체크섬 무효화) — append·truncate 포함 | TestVFS (FLUSH 시점 commit) |
| 5 | mkdir(논리 폴더)·rename(모든 디스크, 대상 덮어쓰기 = 편집기 저장 패턴)·unlink(휴지통)·비어 있지 않은 rmdir 거부 | TestVFS, live |
| 6 | xattr/ACL 은 ENOTSUP 로 응답해 `cp -a` 등이 조용히 대체 | TestVFS, live (`cp -a rc=0`) |
| 7 | 읽기 전용 마운트는 쓰기 거부(EROFS), `df` 는 볼륨 용량 | TestVFS, live |
| 8 | 마운트 경유 변경 후 full SCAN 변화 0 (카탈로그 일관) | live |
| 9 | 권한: vfs 마운트/해제는 storage 권한 | RBAC 규칙 |
