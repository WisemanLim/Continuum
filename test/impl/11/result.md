# impl/11 결과 — E11 SCAN·직접 편집 보정 · **PASS** (시도 2회)

| # | 판정 | 근거 |
|---|---|---|
| 1~6, 8 | PASS | `logs/go-test.log` (TestScanReconcilesDirectEdits), `logs/live-cli.log` |
| 7 | PASS | TestScanSkipsOfflineDisk |
| 9 | PASS | `internal/api` TestScanAndConflictsAPI, `logs/live-cli.log` |
| 10 | PASS | last_scan_at 단언. 주기 트리거는 Background 틱 재사용 |

시도 이력
1. 스크립트 오류 — live 스크립트가 data root 를 `<disk>/Continuum` 으로 가정(실제 `<disk>/.continuum`), `--wait` 플래그 없음. 스크립트 수정.
2. PASS.

부수 수정: `disk attach` 가 매번 모든 마운트에 `diskutil info` 실행 → 잠든 외장 HDD(/Volumes/FILESHARING) 7~8s 대기. OS 메타 30s 캐시 + 네트워크 FS(smbfs/nfs/afpfs/webdav) 는 diskutil 생략 (`internal/disk/osmeta.go`).

가정: fsnotify 감시자는 새 의존성 추가가 필요해 도입 안 함 → PRD 의 "폴링 fallback" 으로 주기 스캔만 구현.
