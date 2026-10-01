# impl/5 결과 — E5 전송 엔진 · **PASS** (시도 4회)

| # | 판정 | 근거 |
|---|---|---|
| 1 | PASS | 물리 배치 disk-01 **24.00 MiB (2,400)** · disk-02 **6.00 MiB (600)** |
| 2~9 | PASS | `logs/go-test.log` |
| 10 | PASS | `logs/race.log` |

시도 이력
1. FAIL 3건 — (a) `Start` 에서 `req.Type` 기본값 누락 → 디렉터리 소스 할당 키 불일치로 전부 unplaced. (b) 이동 테스트 연쇄 실패. **코드 결함 수정.**
2. FAIL 2건 — (a) FR-P08 E2E 25/5 MiB: 테스트 환경이 디스크별 min_free=0 오버라이드를 남김(테스트 결함). (b) 3번 디스크 미사용: **Plan 이 DB 의 오래된 용량을 사용**(statfs 재조회 없음) → Plan·재배치 전 `RefreshCapacity` 추가. 가짜 statfs 를 실사용량 반영으로 변경.
3. FAIL 2건 — (a) 재복사 시 skip 될 기존 파일까지 공간 요구 → `on_conflict=skip` 이면 배치 제외 + `skipped` 기록. (b) 분리된 디스크를 이제 Plan 이 감지 → 오류 코드를 `offline`(종료 6)으로, 실행 중 분리 감지(data root 소멸 시 PAUSED) 추가, 테스트를 "복사 중 분리"로 강화.
4. PASS (race 포함).

성능 메모: 작은 파일 3,000개 ≈24 s(파일당 ≈8 ms). 원인은 macOS 에서 Go `File.Sync` = F_FULLFSYNC(파일 + 부모 디렉터리 2회, PRD §11.2 10단계). 무결성 우선으로 유지. 대용량 파일 처리량은 NVMe/USB 벤치(NFR-P01)로 P2 에서 측정.
