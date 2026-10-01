# continuum — custom (base: go-gin) 정적 템플릿 + Continuum 델타
# 표준 타겟 / standard targets (모든 환경 동일 네이밍):
#   make <env>-all | <env>-build | <env>-logs | <env>-stop | <env>-restart | <env>-ps   env = local | dev | staging | prod
#   make db-migrate | db-seed | db-reset | db-fresh [ENV=<env>]     make test | deploy | preflight | help
# 델타: local=SQLite(M1, docker 생략) · Redis 없음 · pnpm workspace(apps/web·desktop·extension) 병행
.PHONY: help preflight test e2e deploy release-check \
        local-all local-build local-logs local-stop local-restart local-ps \
        $(foreach e,dev staging prod,$(e)-all $(e)-build $(e)-logs $(e)-stop $(e)-restart $(e)-ps) \
        db-migrate db-seed db-reset db-fresh
ENV  ?= local
ENVS := dev staging prod
# compose 호출: .env.<env> 있으면 주입, app 프로파일 포함 (컨테이너 전체 스택)
DC   = docker compose $(if $(wildcard .env.$(ENV)),--env-file .env.$(ENV),) --profile app
DCE  = docker compose $(if $(wildcard .env.$*),--env-file .env.$*,) --profile app
# .env.<ENV> 를 셸 환경으로 로드 후 명령 실행 (host 측 migrate/seed 용)
WITH_ENV = { [ -f .env.$(ENV) ] && set -a && . ./.env.$(ENV) && set +a || true; } &&
# local 은 SQLite(M1) 라 docker infra 기본 생략. Postgres 로 local 검증 시: LOCAL_INFRA=postgres make local-all
LOCAL_INFRA ?=
SQLITE_DB   ?= local.db
# goreman: PATH 우선, 없으면 `go install` 기본 위치($GOPATH/bin) 사용
GOREMAN_BIN ?= $(or $(shell command -v goreman 2>/dev/null),$(shell go env GOPATH 2>/dev/null)/bin/goreman)
GOREMAN ?= $(GOREMAN_BIN) -f Procfile.dev -env .env.local
MIGRATE ?= go run ./cmd/migrate up
SEED    ?= echo "seed 미구현 — cmd/seed 추가 후 SEED 교체"
help:  ## 타겟 목록
	@grep -hE '^[a-z][a-z0-9-]*:.*## ' $(MAKEFILE_LIST) | sed -E 's/:[^#]*##/\t/' | awk -F'\t' '{printf "  %-16s %s\n", $$1, $$2}'
	@echo "  <env>-all        [dev|staging|prod] infra + app 컨테이너 기동 (기존 이미지)"
	@echo "  <env>-build      [dev|staging] 이미지 재빌드 후 기동 (prod 미제공 — CI/CD 산출물)"
	@echo "  <env>-logs       [dev|staging|prod] 컨테이너 로그 추적 (SVC=<service>)"
	@echo "  <env>-stop       [dev|staging|prod] app + infra 전체 정리"
	@echo "  <env>-restart    [dev|staging|prod] 컨테이너 재기동 (SVC=<service>)"
	@echo "  <env>-ps         [dev|staging|prod] 컨테이너 상태"
	@echo "  db-* 는 ENV=<env> 로 대상 지정 (기본 local)"

## --- 사전 호환성 점검 / Preflight (env-init 전 실행 권장) ---
preflight:  ## 런타임·도구 버전 호환성 점검
	@echo "=== Preflight: continuum (go + pnpm) ==="
	@command -v go >/dev/null 2>&1 || (echo "FAIL: Go 미설치 (1.26+)" && exit 1)
	@go version | awk '{split($$3,v,"go"); split(v[2],n,"."); if(n[1]+0<1 || (n[1]+0==1 && n[2]+0<26)) print "FAIL: Go 1.26+ 필요 (현재: " $$3 ")"}'
	@command -v node >/dev/null 2>&1 || (echo "FAIL: Node 미설치 (22+)" && exit 1)
	@node -e 'const m=+process.versions.node.split(".")[0]; if(m<22){console.log("FAIL: Node 22+ 필요 (현재: "+process.version+")");process.exit(1)}'
	@command -v pnpm >/dev/null 2>&1 || echo "WARN: pnpm 미설치 — corepack enable (apps/* 빌드 필요)"
	@command -v docker >/dev/null 2>&1 || echo "WARN: Docker 미설치 — dev/staging/prod 컨테이너 스택 필요"
	@docker compose version >/dev/null 2>&1 || echo "WARN: docker compose v2 없음 (docker-compose v1 아님)"
	@[ -x "$(GOREMAN_BIN)" ] || echo "WARN: goreman 미설치 — go install github.com/mattn/goreman@latest (make local-all 필요)"
	@command -v cargo >/dev/null 2>&1 || [ -x "$$HOME/.cargo/bin/cargo" ] || echo "WARN: Rust(cargo) 미설치 — apps/desktop(Tauri) 빌드 시만 필요 (rustup 설치 후 PATH 에 ~/.cargo/bin)"
	@echo "=== Preflight OK ==="

## --- 공통 / common ---
test:    ## 테스트 실행 (go test + apps vitest)
	go test ./...
	pnpm -r --if-present test
e2e:     ## 브라우저 E2E (Playwright: web UI + Chrome 확장, 실제 Controller 기동)
	pnpm --filter @continuum/web build
	pnpm --filter @continuum/web e2e
	pnpm --filter @continuum/extension build
	cd apps/extension && CONTINUUM_EXT_NATIVE=required pnpm exec wxt build   # T3 test build (.output-t3)
	pnpm --filter @continuum/extension e2e
release-check: ## 공급망 정적 검증 (재현 빌드 · 라이선스 고지 · 릴리즈 설정 lint, 네트워크·서명 없음)
	scripts/repro-check.sh
	go run ./tools/notices -o THIRD_PARTY_NOTICES.md
	python3 scripts/release-lint.py
deploy:  ## Controller(M2) K8s 배포 (Helm)
	helm upgrade --install continuum ./deploy/helm/continuum

## --- local: 호스트 프로세스 매니저 (goreman) + 선택 infra(docker) ---
local-build:    ## [local] 의존성 설치/호스트 빌드 (+ docker 있으면 SMB 접근점 이미지)
	go mod download && go build ./...
	pnpm install
	@if command -v docker >/dev/null 2>&1 && docker info >/dev/null 2>&1; then \
	  docker build -q -t continuum-samba:local deploy/samba; \
	else echo "skip: docker 없음 — SMB 접근점(E15) 이미지 미빌드"; fi
GOREMAN_PIDFILE := .make/goreman.pid
GOREMAN_LOGFILE := .make/goreman.log
local-all:      ## [local] (LOCAL_INFRA 지정 시 docker infra) + goreman 일괄 기동 (백그라운드)
	$(if $(LOCAL_INFRA),docker compose $(if $(wildcard .env.local),--env-file .env.local,) up -d $(LOCAL_INFRA),@echo "local: SQLite(M1) — docker infra 생략 (LOCAL_INFRA=postgres 로 기동 가능)")
	@mkdir -p .make
	@if [ -f $(GOREMAN_PIDFILE) ] && kill -0 $$(cat $(GOREMAN_PIDFILE)) 2>/dev/null; then \
	  echo "goreman 이미 실행 중 (pid $$(cat $(GOREMAN_PIDFILE)))"; \
	else \
	  [ -x "$(GOREMAN_BIN)" ] || { echo "FAIL: goreman 없음 — go install github.com/mattn/goreman@latest"; exit 1; }; \
	  nohup $(GOREMAN) start > $(GOREMAN_LOGFILE) 2>&1 & echo $$! > $(GOREMAN_PIDFILE); \
	  sleep 2; \
	  if ! kill -0 $$(cat $(GOREMAN_PIDFILE)) 2>/dev/null; then \
	    echo "FAIL: goreman 기동 직후 종료 — 로그:"; tail -20 $(GOREMAN_LOGFILE); rm -f $(GOREMAN_PIDFILE); exit 1; \
	  fi; \
	  echo "goreman 백그라운드 기동 (pid $$(cat $(GOREMAN_PIDFILE)), 로그: $(GOREMAN_LOGFILE))"; \
	  echo "  controller → http://127.0.0.1:8080/healthz · web → http://localhost:5173 (첫 기동은 go build 로 수 초 소요)"; \
	fi
local-logs:     ## [local] 통합 로그 추적 (Ctrl-C 로 tail 종료 — 프로세스는 유지)
	@mkdir -p .make && touch $(GOREMAN_LOGFILE) && tail -f $(GOREMAN_LOGFILE)
local-stop:     ## [local] goreman 종료 + infra 정리
	@$(GOREMAN_BIN) run stop-all 2>/dev/null || true
	@if [ -f $(GOREMAN_PIDFILE) ]; then kill $$(cat $(GOREMAN_PIDFILE)) 2>/dev/null || true; rm -f $(GOREMAN_PIDFILE); fi
	$(if $(LOCAL_INFRA),docker compose $(if $(wildcard .env.local),--env-file .env.local,) down --remove-orphans,@true)
local-restart:  ## [local] goreman 재기동 (infra 유지)
	@$(GOREMAN_BIN) run restart-all 2>/dev/null || true
local-ps:       ## [local] 프로세스 상태
	@$(GOREMAN_BIN) run status 2>/dev/null || echo "먼저 make local-all"

## --- dev/staging/prod: Docker 전체 스택 (.env.<env> + app 프로파일) ---
$(ENVS:%=%-all):     %-all:      # [<env>] infra + app 컨테이너 기동 (기존 이미지)
	$(DCE) up -d
$(filter-out prod-build,$(ENVS:%=%-build)): %-build:  # [dev|staging] 이미지 재빌드 후 기동 (prod 는 CI/CD 산출물 — 미제공)
	$(DCE) up -d --build
$(ENVS:%=%-logs):    %-logs:     # [<env>] 컨테이너 로그 추적 (SVC=<service>)
	$(DCE) logs -f $(SVC)
$(ENVS:%=%-stop):    %-stop:     # [<env>] app + infra 전체 정리
	$(DCE) down --remove-orphans
$(ENVS:%=%-restart): %-restart:  # [<env>] 컨테이너 재기동 (SVC=<service>, 비우면 전체)
	$(DCE) restart $(SVC)
$(ENVS:%=%-ps):      %-ps:       # [<env>] 컨테이너 상태
	$(DCE) ps
prod-build:
	@echo "prod-build 미제공 — prod 이미지는 CI/CD 파이프라인 산출물을 사용 (make prod-all)"; exit 1

## --- DB (ENV=local|dev|staging|prod, 기본 local) ---
db-migrate:  ## 마이그레이션 적용 (MIGRATE 변수)
	$(WITH_ENV) $(MIGRATE)
db-seed:     ## 시드 데이터 적재 (SEED 변수)
	$(WITH_ENV) $(SEED)
db-reset:    ## DB 스키마 초기화 + 마이그레이션 (local=SQLite 파일 삭제 · 그 외=postgres schema 재생성) — 데이터 삭제!
ifeq ($(ENV),prod)
	@echo "prod DB reset 금지"; exit 1
else ifeq ($(ENV),local)
	rm -f $(SQLITE_DB)
else
	$(DC) exec -T postgres sh -c 'psql -U "$$POSTGRES_USER" -d "$$POSTGRES_DB" -c "DROP SCHEMA public CASCADE; CREATE SCHEMA public;"'
endif
	$(MAKE) db-migrate ENV=$(ENV)
db-fresh: db-reset  ## db-reset + 시드 적재
	$(MAKE) db-seed ENV=$(ENV)
