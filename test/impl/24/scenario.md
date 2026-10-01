# impl/24 — E24 확장 T3 네이티브 브리지 (FR-X03·X04·X05)

| # | 수용 기준 | 방법 |
|---|---|---|
| 1 | `continuum-agent` 가 Chrome native messaging host: Chrome 이 넘기는 `chrome-extension://<id>/` 인자를 감지해 stdio 호스트로 동작 (4바이트 LE 길이 + JSON, 응답 1 MB 제한) | TestNativeMessaging, E2E |
| 2 | 요청 `hello`(버전·OS·호스트명·등록 여부·node_id·로컬 Controller URL), `discover`(로컬 볼륨 UUID·여유·버스) | TestNativeMessaging, E2E |
| 3 | 매니페스트 설치/제거: `continuum-agent native-host install --browser chrome|chromium|edge|brave --extension-id …` (macOS/Linux 사용자 NativeMessagingHosts, Windows HKCU 레지스트리) | E2E(`--manifest-dir`), 코드 |
| 4 | 사이드 패널: 권한(optional `nativeMessaging`)·호스트가 있으면 "T3 native bridge" + 로컬 Agent 카드 + 로컬 디스크, Controller URL 기본값을 Agent 가 알려준 값으로 | t3-native.spec |
| 5 | 자동 강등: 권한 없음 또는 호스트 미설치 → T1 유지 + "Connect local agent (T3)" 버튼(권한 요청 → 설치 안내) | t3-native.spec (downgrade) |
| 6 | 스토어 빌드 권한은 `storage, sidePanel` 만 유지(FR-X05), nativeMessaging 은 optional | extension.spec (manifest 검사) |
