# impl/29 결과 — E29 보안 잔여 · **PASS** (시도 3회)

| # | 판정 | 근거 |
|---|---|---|
| 1 | PASS | `logs/go-test.log` (TestTOTPVectors·TestMFA), `logs/cli-security.log`, `logs/01-mfa-enabled.png`·`02-mfa-prompt.png` |
| 2 | PASS | TestApprovals, `logs/cli-security.log`, `logs/03-approval-pending.png`·`04-approved-delete.png` |
| 3 | PASS | TestOIDC — 정상 흐름 + PKCE 위조·서명 위조·aud·nonce·state 재사용·외부 return URL 모두 거부 |
| 4 | PASS | `logs/keychain.log` (실 macOS 키체인, 임시 서비스명, 시험 후 항목 없음 확인), TestSecretSettings |
| 5 | PASS | Playwright `zz-auth` + `zz-security` 3/3 |

시도 이력
1. FAIL(시험) — TestApprovals: 디스크 없는 볼륨 생성 400 → 시험이 디스크 연결 후 볼륨 생성.
2. FAIL(시험) — E2E: 토스트 문구(`{n} deleted`) 불일치, MFA 끄기 코드가 ±1 window 밖(step+2) → 다음 30초 step 까지 대기.
3. PASS.

가정·메모
- TOTP 비밀은 DB `users.totp_secret` 에 저장(검증 시마다 필요; 일반 서비스 관행). OS 키체인 대상은 서버 자격 증명(SMB·webhook·OIDC client secret).
- 승인 결정은 security 권한(security-admin·system-admin). 단일 사용자 모드(계정 없음)에서는 승인 게이트 비활성.
- OIDC 는 RS256 만 지원(대부분 IdP 기본값). 실 IdP(Keycloak 등) 연동은 이 환경에서 미시험 — 가짜 IdP 로 프로토콜 검증.
