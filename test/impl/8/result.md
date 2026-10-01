# impl/8 결과 — E8 Web UI · **PASS** (시도 7회)

| # | 판정 | 근거 |
|---|---|---|
| 1~11 | PASS | `logs/playwright-run.log`, 스크린숏 `logs/01~09-*.png` |
| 12 | PASS | `pnpm --filter @continuum/web test` 4 tests |

시도 이력 (결함 → 수정)
1. 설정 파일 ESM `__dirname` 없음 → `fileURLToPath` (시험 설정).
2. **UI 가 빌드 시 박힌 `VITE_CONTROLLER_URL`(8080)을 같은 origin 보다 우선** → Controller 가 서빙할 때는 같은 origin 우선.
3. **같은 origin POST 도 `Origin` 헤더를 보내는데 API 가드가 거부**(`origin … not allowed`) → 요청 Host 와 같은 origin 허용 + API 테스트 추가. **빈 목록을 Go 가 `null` 로 반환 → Overview `recent.map` 충돌로 앱 전체 빈 화면** → 모든 목록 `[]` 반환 + React ErrorBoundary.
4. 테스트: dnd-kit 이 드롭 후 포커스를 핸들로 복원하는 타이밍과 경합 → 드롭 후 대기.
5. 테스트: 폴더 링크 선택자 모호 → 정확 일치.
6. 테스트: 키보드 DnD 키 입력 간 측정 프레임 대기.
7. PASS. 상태 표기 점+아이콘 중복(●●) → 정상 아이콘 ✓ 로 구분.

범위 메모: 화면 문구 일부(Transfers·Nodes 대화상자)는 영문 — ko/en 리소스 완전화(FR-G13, Should)는 P2. Conflict Center(FR-G15)·SCAN 은 P2. 소스 경로는 텍스트 입력(브라우저는 로컬 경로 선택 불가) — 데스크톱 셸의 네이티브 파일 대화상자는 P2.
