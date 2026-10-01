# impl/28 결과 — E28 P2 잔여 · **PASS** (시도 3회)

| # | 판정 | 근거 |
|---|---|---|
| 1 | PASS | `logs/go-test.log`, `logs/live-local-mount.log` attempt 3 |
| 2 | PASS(렌더·문법) | `logs/autoconnect.log` — 실제 키체인 기록·launchctl 적재는 사용자 환경 변경이라 시험에서 제외(`--dir - --no-load`) |
| 3 | PASS | `logs/live-local-mount.log` |
| 4 | PASS | TestSmartHistory — 이 Mac 은 smartctl 이 없어 온도 값 실측 없음(그래프는 데이터가 2점 이상일 때 표시) |
| 5 | PASS | TestTimeWindow |

시도 이력
1. FAIL(시험) — `local-mount --format json` 에 `--print` 를 빼 CLI 가 Mac 에서 마운트 시도.
2. FAIL — 감시자가 시작 후 연결된 디스크를 30초 동기화 주기까지 감시하지 않아 `new.txt` 미반영 → 동기화 5초. 부수 발견: mergerfs 합집합에는 마커 `volume.json` 이 보임(mergerfs 로 숨길 수 없음) → 문서화, 내부 파일을 숨기는 네이티브 VFS(E26) 권장.
3. PASS — mergerfs 경유 쓰기 2건이 수동 스캔 없이 카탈로그 반영.

E2E 는 결정적 SCAN 보고를 위해 `CONTINUUM_WATCH=off`.
