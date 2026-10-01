# impl/29 — E29 보안 잔여 (FR-S02 MFA·SSO, FR-S05 2인 승인, FR-S06 OS 키체인)

| # | 수용 기준 | 방법 |
|---|---|---|
| 1 | TOTP(RFC 6238, SHA-1, 6자리, 30초, ±1 step): `setup` → 첫 코드로 `enable`, 로그인 시 비밀번호 후 코드 요구(401 `info.mfa_required`), 틀린 코드 = 로그인 실패(잠금 카운트), 사용한 코드 재사용 거부, `disable` 은 현재 코드 필요 | TestTOTPVectors(RFC 부록 B), TestMFA, E2E `zz-security` 1, CLI `user mfa`·`login --code` |
| 2 | 2인 승인: 설정 `security.approval_actions`(disk.wipe·disk.drain·volume.delete·user.delete·node.revoke 중) 에 든 작업은 첫 시도 시 요청 생성(403 `approval_required` + `info.approval_id`, 반복 시 재사용), security 권한자가 승인/거절, 요청자 본인 결정 불가, 승인 건은 요청자·작업·대상에 묶여 1회용(`X-Approval-Id` / `--approval`), 24시간 만료, 감사 로그 | TestApprovals, E2E `zz-security` 2, CLI `approval list/approve`·`volume delete --approval` |
| 3 | OIDC SSO: Authorization Code + PKCE(S256)·state·nonce, discovery·JWKS(RS256) 서명 검증, iss/aud/exp/nonce 확인, 첫 로그인 사용자 JIT 생성(`auth_source=oidc`, 기본 역할, system-admin 자동 부여 금지), UI 로 `#oidc_token=` 리다이렉트(동일 출처·허용 출처만, 그 외 `/`), SSO 사용자는 비밀번호 로그인 불가, client secret 은 비밀 저장소 + GET 응답 마스킹 | TestOIDC (in-process 가짜 IdP) |
| 4 | OS 키체인 비밀 저장: macOS `security` / Linux `secret-tool` / Windows PasswordVault, 없으면 0600 파일; `access.smb_password`·`notify.webhook_url`·`oidc.client_secret` 이 DB 대신 저장소로, 기존 DB 값 자동 이전 | TestFileStore, TestSecretSettings, TestOSStore(`CONTINUUM_TEST_KEYCHAIN=1`, 임시 서비스명) |
| 5 | UI: 로그인 화면 코드 입력·SSO 버튼, 설정 2단계 인증·승인 패널(정책 체크박스)·SSO 설정 카드, 승인 대기 작업 재시도 시 자동으로 승인 ID 첨부 | E2E `zz-security`, 스크린샷 |
