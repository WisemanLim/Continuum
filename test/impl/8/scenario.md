# impl/8 — E8 Web UI (FR-G01~G13·G15 일부, V01~V22 UI, T19, P07, D03·D04 UI; UI 설계서 CTM-COM/OVW/VOL/NOD/DSK/TRF/ACT/SET)

실제 Chrome(Playwright `channel: chrome`) + 실제 Controller(빌드된 UI 를 같은 origin 으로 서빙) E2E. 스크린숏 `logs/*.png`.

| # | 케이스 | 기대 |
|---|---|---|
| 1 | 앱 셸 | 사이드바 6항목, breadcrumb, 헤더 "모든 Agent 온라인" (CTM-COM-010) |
| 2 | Attach Storage 위저드 1/2 → 2/2 | 경로 입력 → 사전 검사 10항목 체크리스트 → Attach (CTM-DSK-010/011) |
| 3 | Create Volume 포인터 DnD | 카드 핸들 5 px 이상 끌어 드롭존에 놓으면 순번 1 (CTM-VOL-020, §8.3) |
| 4 | 키보드 경로 | "+" 버튼 Enter, ↑/↓ 버튼으로 순번 변경 (FR-G12) |
| 5 | 요약·구성 폼·스테퍼 | "3 disks · … usable · … free" (FR-V10), 이름·경로·정책·최소 여유·spillover, 3단계 Review & save |
| 6 | Portable 경고 | 저장 시 확인 대화상자 (CTM-VOL-P01) → 볼륨 상세로 이동 |
| 7 | Copy to Volume + Preview Plan | 12 files, 디스크별 배치 막대, `excluded: role=portable` → Copy → DONE (CTM-TRF-P01/P02) |
| 8 | Files · Show physical location | 논리 폴더 탐색 → 물리 경로 `.continuum/...` (FR-K02) |
| 9 | Overview 타일 | 볼륨 1 (CTM-OVW-010) |
| 10 | 키보드 DnD (dnd-kit) | Disks 보드에서 Space 집기 → ↓ → Space 놓기 → Apply order → revision 증가 |
| 11 | 다크 테마·언어 | prefers-color-scheme: dark → `--color-bg` #0F1220, 설정에서 en 전환 |
| 12 | 단위 로직 | vitest: FR-V10 계산식(이미지 30 TB 대신 50.9 TB), 순번 재부여, 크기·ETA 포맷 |
