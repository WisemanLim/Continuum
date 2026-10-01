# impl/31 — E31 네이티브 VFS · macOS (FR-A06, R07)

| # | 수용 기준 | 방법 |
|---|---|---|
| 1 | Linux 와 같은 VFS 코드가 macOS 에서 macFUSE(go-fuse `mount_macfuse`)로 마운트: 읽기 최저 순번·배치 정책 쓰기·spillover·rename·휴지통·symlink·xattr ENOTSUP·읽기 전용 (TestVFS 를 linux‖darwin 공용으로) | `go test ./internal/vfs` (macOS 실마운트) |
| 2 | Finder 표시 이름 = 볼륨 이름(`volname`), 디스크에 `._*` AppleDouble 미생성(`noappledouble,noapplexattr`) | 코드, 실마운트 |
| 3 | macFUSE 미설치·확장 미승인이면 무한 대기 없이 15초 안에 원인·조치 안내 오류(go-fuse 는 helper 가 fd 전에 실패하면 소켓을 영원히 대기) | TestVFS(macOS), 오류 메시지 |
| 4 | `vfs.Available()` 로 OS별 사전 판정(Linux `/dev/fuse`, macOS macFUSE, Windows 미지원 → SMB 접근점 안내) | 빌드 3 OS, TestVFS skip 사유 |
| 5 | Linux 회귀 | Linux 컨테이너 TestVFS |
