# impl/33 — E33 LDAP / Active Directory 로그인 (FR-S02 엔터프라이즈)

| # | 수용 기준 | 방법 |
|---|---|---|
| 1 | 로컬 계정이 없는 사용자는 디렉터리로 로그인: 서비스 계정 bind → `ldap.user_filter`(사용자명 RFC 4515 이스케이프) 검색 → 결과 정확히 1건 → 사용자 DN 으로 bind | TestLDAPLogin (in-process LDAP 서버, go-ldap BER) |
| 2 | 그룹(`ldap.group_attr`, 기본 memberOf) → 역할(`ldap.role_map`, 가장 높은 역할), 매칭 없으면 `ldap.default_role`, 비어 있으면 거부. 로그인마다 역할 재평가(그룹 변경 반영, 감사 기록) | TestLDAPLogin |
| 3 | 보안: 빈 비밀번호(무인증 bind) 거부, 필터 주입 이스케이프, 같은 이름의 로컬 계정은 디렉터리로 넘어가지 않음, 5회 실패 잠금, 사용자 존재 여부 비노출(동일 오류) | TestLDAPLogin |
| 4 | 전송 보안: `ldaps://` 또는 `ldap://` + StartTLS(기본 켬, 미지원 디렉터리 → "directory unavailable" 409, 평문 강등 없음), 사설 CA `ldap.ca_file` | TestLDAPLogin (StartTLS 거부 서버, 사설 CA ldaps) |
| 5 | 첫 로그인 시 계정 자동 생성(`auth_source=ldap`, 비밀번호 없음), 로컬 TOTP MFA 그대로 적용, `GET /v1/auth/status` 에 `ldap` | TestLDAPLogin, 코드 |
| 6 | 설정 검증(`ldap.url` 스킴, 필터 `%s` 1개, role_map 역할 이름, default_role), `ldap.bind_password` 비밀 저장소·응답 마스킹, UI 설정 카드 | TestLDAPLogin, tsc, E2E 설정 화면 회귀 |
