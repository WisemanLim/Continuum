# impl/30 결과 — E30 운영 잔여 · **PASS** (시도 2회)

| # | 판정 | 근거 |
|---|---|---|
| 1 | PASS | `logs/go-test.log` TestEmailNotifications — 다이제스트 1통·수신자 2·AUTH PLAIN·SMTP 비밀번호 DB 미저장·STARTTLS 강등 거부·헤더 주입 거부 |
| 2 | PASS | TestGrafanaDashboard — 14 패널의 메트릭 전부 export 확인, Helm 사본 동일성 확인. 실제 Grafana import 는 이미지 미보유로 미실행 |
| 3 | PASS | `logs/helm.log`, `logs/helm-template.yaml` (7 리소스), `logs/container-securitycontext.log` — linux/arm64 현행 빌드를 `--read-only --user 65532 --cap-drop ALL` 로 실행: healthz/readyz 200, /data 에 DB·pki(0600 CA 키)·secrets(0600) 생성, 비밀 마스킹 |
| 4 | PASS | TestOTLPExport·TestAPITracing (race 포함), `logs/otel-live.log` — 실 컨트롤러 → OTLP 수신기: HTTP span 6 + `job COPY` span, traceparent 의 parent span id 이어받음 |

시도 이력
1. FAIL(시험) — 라이브: Python 수신기로 span 0건 + 종료 시 전송이 취소 ctx 로 실패해 span 유실. 수정: 주기 flush 는 `context.WithoutCancel`, 실패 batch 재큐(다음 flush·종료 flush 에서 재전송). 수신기를 Go 로 교체(Python 스크립트 결함으로 판단 — 같은 바이너리가 nc·Go 수신기로는 정상 전송).
2. PASS.

가정·메모
- OTel SDK 의존성(go.opentelemetry.io/*) 대신 OTLP/HTTP JSON 을 직접 구현 — CGO 없는 단일 바이너리·의존성 최소 유지, 표준 OTEL_* env 호환. gRPC(4317) 는 미지원(4318 HTTP 사용).
- Helm 이미지는 저장소 Dockerfile `controller` 타깃(distroless). 이 이미지는 웹 UI 를 포함하지 않음(API 전용) — 웹 UI 는 `CONTINUUM_WEB_DIR` 로 제공하는 별도 빌드 필요.
- Kubernetes 클러스터 실배포는 하지 않음(prod 배포 금지 규칙) — 렌더·lint·동일 보안 컨텍스트 컨테이너 실행으로 검증.
