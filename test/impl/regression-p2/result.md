# 전체 회귀 (P2 완료 게이트) · **PASS**

| 명령 | 결과 | 로그 |
|---|---|---|
| `make test` (go test ./... + vitest) | PASS — 10 Go 패키지, vitest 4 | `logs/make-test.log` |
| `go test -race ./...` | PASS | (E17 직전 실행, 이후 Go 변경 없음) |
| `make e2e` (실제 Chrome + Chromium 확장, 실제 Controller) | PASS — web 8 스펙, 확장 1 | `logs/make-e2e.log` |
| `GOOS=linux|windows go build ./...` | PASS | — |
