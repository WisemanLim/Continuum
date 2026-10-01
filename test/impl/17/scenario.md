# impl/17 — E17 UI·i18n 마감 (FR-G13·T20·G06)

| # | 수용 기준 | 방법 |
|---|---|---|
| 1 | ko(기본)/en 리소스 완전화 — 모든 화면의 제목·버튼·탭·라벨·표 머리글·빈 상태·토스트·aria-label. `ko: Record<Key,string>` 로 키 누락 컴파일 차단 | tsc, E2E(ko 기본 시나리오 + en 시나리오) |
| 2 | 설정에서 언어 전환 시 즉시 반영 | `job-detail.e2e.ts`, `create-volume.e2e.ts` (dark + ko/en) |
| 3 | 작업 상세 `#/transfers/<id>`: 진행률·디스크별 배치·생성/시작/완료 시각·일시정지/재개/취소/재시도·항목 표(상태 필터)·오류 CSV | `job-detail.e2e.ts` |
| 4 | 폴더 선택기: 복사 원본·Attach 경로에서 "Browse…" → Controller 호스트 폴더 탐색(`GET /v1/fs/browse`, 루트: Home·/Volumes·드라이브 문자) | `TestBrowse`, E2E |
| 5 | 설정 화면 묶음: 접근점(볼륨 › Access), 알림(설정 › Notifications), SCAN 주기·고아 정책(볼륨 › Settings) | E2E (E12·E15·E16) |
| 6 | 확장 콘솔(web UI 재사용) 회귀 | `extension.spec.ts` |
