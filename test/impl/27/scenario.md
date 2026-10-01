# impl/27 — E27 확장 T2 브라우저 로컬 풀 (FR-X02, X04)

| # | 수용 기준 | 방법 |
|---|---|---|
| 1 | File System Access API 로 부여한 폴더들을 순번 풀로 구성(추가·순서 변경·제거·폴더별 할당량), IndexedDB 에 핸들 보관 → 새로고침 후 유지, 권한 만료 시 "Grant access" | t2-pool.spec (OPFS 대체), 코드 |
| 2 | 배치: 순서대로 채움(할당량 − 사용량 − 예약), 파일 분할 없음, 맞지 않으면 unplaced | `t2plan.test.ts`, t2-pool.spec |
| 3 | 복사: 대상 경로 생성, createWritable(닫을 때 원자적 반영), 재읽기 SHA-256 비교로 검증 | t2-pool.spec (3/3 verified, 실제 폴더별 파일 확인) |
| 4 | 병합 목록: 같은 이름은 최저 순번 폴더 우선 | `t2plan.test.ts`, t2-pool.spec |
| 5 | Controller 없이 동작, 사이드 패널 기능 표에 T2 사용 가능 표시 + "Browser-local pool (T2)" 진입 | t2-pool.spec, extension.spec |
