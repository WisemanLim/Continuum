# impl/22 — E22 인증·RBAC 5역할 (FR-S01·S02·S05)

| # | 수용 기준 | 방법 |
|---|---|---|
| 1 | 사용자 0명 = 단일 사용자 모드(기존 동작, `CONTINUUM_TOKEN` 선택 게이트). 첫 사용자는 system-admin 만 허용, 생성 순간 인증 모드 | TestAuthRBAC, live CLI |
| 2 | 비밀번호 PBKDF2-SHA256 600k(표준 라이브러리), 10자 이상, 세션 토큰은 해시만 저장·12h | 코드, live (로그인 56 ms) |
| 3 | 권한 매트릭스: read(전원) · transfer(Operator·Storage·System) · storage(Storage·System) · security(Security·System) · system(System). 경로·메서드 규칙, DRAIN/REBALANCE 는 /v1/jobs 라도 storage 필요 | TestAuthRBAC (7 경로 × 5 역할) |
| 4 | 미인증 401(`unauthenticated`), 권한 부족 403 + 부족한 권한 이름. CLI 종료 코드 5 | live CLI |
| 5 | 로그인 5회 실패 → 5분 잠금, 사용자 열거 방지(동일 메시지·더미 해시), 비활성화·비밀번호 변경 시 세션 종료, 로그아웃 | TestAuthRBAC |
| 6 | 마지막 system-admin 강등·비활성·삭제 거부 | TestAuthRBAC |
| 7 | 감사 로그 행위자 = 로그인 사용자 (요청 컨텍스트 → audit.Append, 작업 requested_by) | TestAuthRBAC, live (`admin disk.attach`) |
| 8 | CLI `login/logout/whoami`, `user list|add|set-role|disable|enable|passwd|delete`, 세션 파일 0600 | live CLI |
| 9 | UI: 401 시 로그인 화면, 사용자 메뉴(로그아웃), 설정 › 사용자 및 역할(security 권한), 계정(비밀번호 변경) | e2e/zz-auth.e2e.ts |
