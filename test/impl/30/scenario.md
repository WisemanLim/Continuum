# impl/30 — E30 운영 잔여 (FR-D26 이메일, FR-O01 Grafana·OTel, NFR 배포 Helm)

| # | 수용 기준 | 방법 |
|---|---|---|
| 1 | 이메일 알림: 설정 `notify.smtp_host`(host:port)·`smtp_user`·`smtp_password`(비밀 저장소)·`smtp_tls`(starttls 기본·tls·none)·`email_from`·`email_to`(쉼표); 주목 이벤트를 틱당 1통 다이제스트로; STARTTLS 미제공 서버는 거부(평문 강등 없음); 헤더 개행 주입 거부; `POST /v1/notifications/test` 가 webhook·이메일 모두 시험; 설정 화면 이메일 필드, 비밀번호 마스킹 | TestEmailNotifications (in-process SMTP), tsc |
| 2 | Grafana 대시보드 JSON(`deploy/grafana/`): 데이터소스 입력 변수·`$volume` 필터·디스크 순번별 사용률·볼륨 용량·작업·처리량·충돌·SMB; 쿼리하는 모든 메트릭이 `/metrics` 에 존재 | TestGrafanaDashboard |
| 3 | Helm chart(`deploy/helm/continuum`): Deployment(Recreate, 1 replica, 비root 65532·읽기전용 rootfs·cap drop ALL·seccomp), DB URL·API 토큰은 기존 Secret 참조(값 미포함, DB Secret 필수), PVC(/data: pki·access·secrets, keep), API/agent Service(agent 는 mTLS passthrough), Ingress(SSE 버퍼링 off), ServiceMonitor·Grafana 대시보드 ConfigMap 옵션, probes /healthz·/readyz | `helm lint --strict`, `helm template` 전 기능/최소 구성, 필수값 누락 시 렌더 실패, 차트와 같은 보안 컨텍스트로 컨테이너 실행 |
| 4 | OpenTelemetry 트레이스: 표준 env(`OTEL_EXPORTER_OTLP_ENDPOINT`/`_TRACES_ENDPOINT`·`_HEADERS`·`OTEL_SERVICE_NAME`·`OTEL_TRACES_SAMPLER_ARG`), OTLP/HTTP JSON, 요청당 SERVER span(라우트 패턴 이름·상태 코드·5xx 오류), W3C traceparent 이어받기, 작업 실행 span(job id·type·state·files·bytes), 비활성 시 no-op, 수집기 장애 시 재큐(상한 8192) | TestOTLPExport, TestOffAndSampling, TestAPITracing, live 수집기 |
