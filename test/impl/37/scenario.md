# impl/37 — E37 Controller 이미지에 웹 UI 내장 (E30 잔여, FR-G01 배포)

| # | 수용 기준 | 방법 |
|---|---|---|
| 1 | 저장소 Dockerfile `controller` 타깃: node 22 stage 에서 pnpm 11.10 고정·`--frozen-lockfile` 로 web 빌드 → `/web`, `CONTINUUM_WEB_DIR=/web`, nonroot(65532), `/data` 를 65532 소유로 생성(compose named volume 이 쓰기 가능) | web stage 절차 오프라인 재현, 이미지 구성 시뮬레이션 |
| 2 | 릴리즈 이미지(goreleaser `dockers_v2`): 워크플로가 web 을 먼저 빌드, `extra_files` 로 `web/` 전달, `Dockerfile.controller` 가 `/web` 복사 | release-lint |
| 3 | 컨테이너가 같은 출처로 UI·API 제공: `/` index.html, SPA 경로 200, 자산 200, `/v1/info` | 이미지 시뮬레이션 |
| 4 | Helm: 이미지 그대로 UI 제공(NOTES 안내), lint 통과 | helm lint |
