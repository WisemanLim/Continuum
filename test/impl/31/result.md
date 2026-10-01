# impl/31 결과 — E31 macOS VFS · **BLOCKED** (macFUSE 확장 미승인 · FUSE-T 경로는 macOS 커널 패닉) — 시도 7회

| # | 판정 | 근거 |
|---|---|---|
| 1 | **BLOCKED** | `logs/macfuse-probe.log` — `mount_macfuse: the file system is not available (1)` (exit 69). macFUSE 5.2.0 kext 가 로드되지 않음, FSKit backend 도 같은 응답. 승인은 시스템 설정 + 재시작이 필요한 사용자 조치 |
| 2 | 코드 반영 (실측 불가) | `internal/vfs/vfs.go` darwin 옵션 |
| 3 | PASS | `logs/attempt2.log` — 15.3 초에 안내 오류로 반환, 테스트 skip(이전 `logs/attempt1-full.log`: 40 초 타임아웃까지 go-fuse `getConnection` 대기) |
| 4 | PASS | darwin·linux·windows 빌드, skip 사유 출력 |
| 5 | PASS | `logs/linux-regression.log` |

시도 이력
1. HANG — go-fuse 가 `mount_macfuse` 실패 후에도 fd 소켓을 대기(부모가 remote 끝을 쥐고 있어 EOF 없음). 진단: `logs/attempt1-full.log` 스택.
2. 무한 대기 방지(15초 제한 + 조치 안내) 반영. 실마운트는 확장 미승인으로 불가 → BLOCKED.

해결에 필요한 결정/조치
- (사용자) macFUSE 시스템 확장 승인: 시스템 설정 → 개인정보 보호 및 보안 → macFUSE 허용 → 재시작 (Apple Silicon 은 복구 모드에서 "Reduced Security" 필요할 수 있음). 또는 macFUSE ≥ 5.4 로 업데이트 후 일반 → 로그인 항목 및 확장 → 파일 시스템 확장에서 macFUSE(FSKit) 켜기.
- 또는 FUSE-T(설치됨, kext 불필요) 지원: cgofuse(cgo) 필요 → `CGO_ENABLED=0` 단일 바이너리 원칙과 상충, macOS 전용 cgo 빌드 허용 여부 결정 필요.

## 사용자 결정 이후 (FUSE-T 경로, cgo `-tags fuset`)

3. FAIL — FUSE-T 가 `Init` 을 NFS 마운트 부착 전에 호출해 쓰기가 원 디렉터리로 감 → fsid 변화 확인 후 반환.
4. FAIL — macOS NFS 클라이언트가 xattr 미지원 시 `._*` AppleDouble 생성 → xattr 을 실제 디스크 파일로 pass-through.
5. FAIL — 여전히 `._*` → FUSE-T 옵션 `namedattr,noappledouble`.
6. **기능 PASS** (`logs/attempt6-fuset.log`): 배치·spillover·rename·휴지통·symlink·xattr 보존·읽기 전용 모두 통과.
   **그러나** 이 시도들과 라이브 시험 중 macOS 가 4회 재부팅: 커널 패닉 `nfs_vinvalbuf2: ubc_msync failed!, error 22` (nfs_bio.c:4260, `logs/kernel-panics.log`). FUSE-T 의 NFS 루프백을 macOS NFS 클라이언트가 처리하다 발생 — 사용자 환경을 손상시킬 수 있어 **사용 금지**.
7. 안전 조치(`logs/attempt7-fuset-gated.log`): FUSE-T 백엔드는 `CONTINUUM_VFS_FUSET=i-accept-kernel-panic-risk` 없이는 마운트 거부, macOS 마운트 시험은 `CONTINUUM_TEST_MACOS_MOUNT=1` 없이는 skip. 기본 빌드(`CGO_ENABLED=0`)에는 FUSE-T 코드가 없음. 이후 이 Mac 에서 마운트 시험은 수행하지 않음.

남은 결정
- macOS 네이티브 VFS 는 macFUSE kext 승인(재시작) 또는 macFUSE ≥ 5.4 FSKit 백엔드 이후 **일회용 시험 머신**에서 재검증. 그 전까지 macOS 는 SMB 접근점 사용.
- FUSE-T 패닉 원인 분석(FUSE-T 이슈 보고, 커널 버전별 재현)은 별도 장비에서.
