# impl/34 결과 — E34 공급망·릴리즈 · **PASS (정적 검증 범위)** — 시도 1회

| # | 판정 | 근거 |
|---|---|---|
| 1 | PASS | `logs/repro-check.log` — darwin/arm64·linux/amd64·windows/arm64 × 3 명령, 별도 캐시 2회 빌드 SHA-256 동일 9/9 |
| 2 | PASS(정적) | `logs/make-release-check.log` release-lint 238 검사 0 문제 |
| 3 | PASS(정적) | 동일 |
| 4 | PASS(정적) | 동일 |
| 5 | PASS | `logs/notices.log` — 34 모듈, 라이선스 불명 0 (Apache-2.0 5 · BSD-3 17 · MIT 11 · CC0 1) |
| 6 | PASS | 문서 작성, archives.files 참조 확인 |
| 7 | PASS(정적) | 정책 파일 파싱·구조 검사, `logs/wxt-zip.log` (140 kB zip, 고정 타임스탬프) |
| 8 | PASS(정적) | release-lint |
| 9 | PASS | `logs/release-lint-mutations.log` — 3 변이 모두 검출, 복원 후 0 |

미실측(사용자 결정 E34 "설정 작성 + 정적 검증")
- goreleaser·cosign·syft·jsign 실행 없음(미설치, 네트워크 설치 금지). 첫 태그 전에 release 워크플로의 `goreleaser check` 단계가 스키마를 검증 — `dockers_v2`·`binary_signs`·`signs[].if` 는 goreleaser v2 최신 문법 기준이며 버전에 따라 조정 필요할 수 있음.
- 실제 서명·공증·스토어 업로드는 인증서·계정이 있어야 함(비밀은 `release` environment 에 등록: MACOS_SIGN_P12, MACOS_SIGN_PASSWORD, MACOS_NOTARY_ISSUER_ID/KEY_ID/KEY, APPLE_CERTIFICATE(_PASSWORD), APPLE_SIGNING_IDENTITY, AZURE_SIGNING_ACCOUNT/PROFILE/ENDPOINT, AZURE_ACCESS_TOKEN, GPG_PRIVATE_KEY, GPG_FINGERPRINT, TAURI_SIGNING_PRIVATE_KEY(_PASSWORD), CHROME_EXTENSION_ID/CLIENT_ID/CLIENT_SECRET/REFRESH_TOKEN, EDGE_PRODUCT_ID/CLIENT_ID/API_KEY).
- 저장소에 LICENSE 파일이 없음 — 프로젝트 라이선스 결정 필요(아카이브는 `LICENSE*` 가 있으면 포함).
