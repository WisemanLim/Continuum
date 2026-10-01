# impl/6 결과 — E6 Controller API · **PASS** (시도 2회)

| # | 판정 | 근거 |
|---|---|---|
| 1~7 | PASS | `go test ./internal/api` (TestAPIFlow·TestSecurityGuards), 실바이너리 `logs/live-curl.log` |

시도 이력
1. FAIL(시험 스크립트) — zsh 는 `$J` 변수를 단어 분리하지 않아 `-H` 헤더가 깨짐. 코드 결함 아님.
2. PASS — 헤더 직접 지정.

발견·조치: 같은 물리 볼륨 위 폴더 2개를 디스크로 붙이면 여유 공간이 중복 집계됨 → 볼륨 경고("share free space") 추가. 물리 볼륨 단위 합산은 P2.
