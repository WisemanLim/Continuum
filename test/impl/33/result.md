# impl/33 결과 — E33 LDAP / AD · **PASS** (시도 2회)

| # | 판정 | 근거 |
|---|---|---|
| 1 | PASS | `logs/go-test.log` TestLDAPLogin |
| 2 | PASS | storage 그룹 → storage-admin, ops 로 변경 → operator, 무그룹 + default viewer |
| 3 | PASS | 빈 비밀번호·`x)(uid=alice` 주입(필터에 `x\29\28uid=alice` 로 이스케이프 확인)·로컬 admin 비전환·잠금 |
| 4 | PASS | StartTLS 미지원 → 409 "StartTLS", 사설 CA ldaps: CA 없으면 409, `ldap.ca_file` 지정 시 200 |
| 5 | PASS | ldap 사용자 2명 자동 생성, `/v1/auth/me` 세션 |
| 6 | PASS | 잘못된 role_map·필터 400, 비밀번호 `********`, web/확장 tsc, Playwright 설정 관련 4 스펙 PASS |

시도 이력
1. PASS(기본 흐름).
2. 추가 — 기업 AD 에서 흔한 사설 CA 를 위해 `ldap.ca_file` 추가, LDAPS·잠금 시험 보강 → PASS.

미실측
- 실제 AD/OpenLDAP 서버: 로컬 `continuum-samba:local` 로 Samba AD DC 를 띄우려 했으나 `samba-ad-provision`(스키마) 패키지가 없어 프로비저닝 불가(`logs/live-samba-ad-unavailable.log`). 패키지·이미지 설치는 네트워크 설치 금지 규칙으로 생략. 프로토콜은 go-ldap 클라이언트 ↔ 같은 BER 라이브러리로 만든 서버로 검증.
- 의존성: `github.com/go-ldap/ldap/v3` v3.4.14 (+ asn1-ber, golang.org/x/crypto 추가, x/text 0.29→0.40).
