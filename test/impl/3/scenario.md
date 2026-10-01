# impl/3 — E3 볼륨 관리 (FR-V10·V11·V13~V17·V19~V22, D42)

| # | 케이스 | 테스트 | 기대 |
|---|---|---|---|
| 1 | 생성(단일 트랜잭션) | `TestVolumeLifecycle` | slug `research-pool`, 순번 1..3 |
| 2 | 요약 계산식 | 같은 테스트 | usable = Σ(total − min_free), free = Σ effective_free (이미지 수치 대신 FR-V10) |
| 3 | Portable 경고 | 같은 테스트 | portable 디스크 포함 시 warnings |
| 4 | 이름 중복 | 같은 테스트 | slug 충돌 → conflict |
| 5 | 순번 변경 + 낙관적 잠금 | 같은 테스트 | revision+1, stale revision → `revision_mismatch`(ABORTED), 마커 ordinal 갱신 |
| 6 | 설정 변경 | 같은 테스트 | placement_policy 변경 |
| 7 | Export/Import | 같은 테스트 | 새 Controller 에서 YAML import → 같은 순번·정책 |
| 8 | 삭제 | 같은 테스트 | 레지스트리만 삭제, 디스크 미할당으로 복귀 |
| 9 | slug 규칙 | `TestSlugify` | 한글 전용 이름 → `vr001` 형식 |
