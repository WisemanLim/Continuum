# impl/27 결과 — E27 확장 T2 · **PASS** (시도 1회)

| # | 판정 | 근거 |
|---|---|---|
| 1~5 | PASS | `logs/e2e.log` (확장 4 spec, vitest 7), `01-t2-pool.png` |

가정·한계: 네이티브 폴더 선택 창은 자동화할 수 없어 E2E 는 `?opfs=1` 시험 모드(OPFS 디렉터리 핸들 — 같은 FileSystemDirectoryHandle API)로 실행. 브라우저는 폴더의 여유 공간을 알 수 없어 사용자가 할당량을 지정. 해시는 WebCrypto SHA-256(BLAKE3 없음). 파일 크기 제한은 브라우저 메모리(재읽기 검증 시 arrayBuffer).
