# impl/37 결과 — E37 웹 UI 내장 이미지 · **PASS** (시도 1회, 정적·시뮬레이션)

| # | 판정 | 근거 |
|---|---|---|
| 1 | PASS | `logs/web-stage.log` — manifest 만 복사한 디렉터리에서 `pnpm install --frozen-lockfile --filter @continuum/web...`(로컬 store 오프라인) + build 성공; `logs/image-sim.log` — 새 named volume 이 이미지 `/data`(65532) 로 초기화, DB 파일 65532 소유 |
| 2 | PASS | `logs/release-lint.log` 250 검사 0 문제 |
| 3 | PASS | `logs/image-sim.log` — `/`·`/volumes` text/html 200, JS 자산 200, `/v1/info` JSON (read-only rootfs, cap-drop ALL, uid 65532) |
| 4 | PASS | `helm lint --strict` |

미실측: 실제 `docker build`(node·golang·distroless 베이스 이미지 pull 필요 — 네트워크 설치 규칙으로 생략). 시뮬레이션은 로컬 debian 이미지에 같은 경로·사용자로 구성. CI 첫 빌드에서 확인 필요.
