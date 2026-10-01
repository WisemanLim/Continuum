# test/ — 시험 표준 (wise-dev-std test-runner)

모든 시험 산출물은 이 디렉터리 하나로 통일한다.

| 경로 | 용도 |
|---|---|
| `dev-env/` | 표준 개발환경 검증 (1회). `scenario.md` → 실행 → `result.md` |
| `impl/<Nth>/` | 구현 차수별 시나리오·결과 (`/wise-dev-std:implement` 가 자동 생성) |
| `**/logs/` | 원본 로그 (커밋 제외) |

사이클: **시나리오 작성 → 시험 진행 → 오류 시 수정·재시험 → 결과 작성**.

러너:
- Go: `go test ./...` (in-package `_test.go`, testify/afero/testcontainers 는 implement 시 추가)
- Web/Extension: Vitest (`pnpm -r --if-present test`), E2E 는 Playwright (implement 시 추가)
- 공통 진입점: `make test`

Continuum 필수 시험 (PRD §6·§15.3):
- FR-P08 spillover 재현: 25 GB/15 GB 디스크, 예약 1 GB, 30 GB(폴더 10·파일 3,000) → 약 24/6 GB 배치
- 장애 주입: 디스크 제거·ENOSPC·전원 차단 시뮬레이션 → 원본 100% 보존 (NFR-I01)
- 3 OS Agent E2E, 한글·NFC/NFD 파일명, 접근성(키보드 DnD)
