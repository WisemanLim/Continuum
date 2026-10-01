# continuum — cgo 없는 정적 바이너리 (PRD §12.6). 타깃: controller(기본) · agent
FROM golang:1.26 AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN mkdir -p /out/data && CGO_ENABLED=0 go build -o /out/controller ./cmd/controller \
 && CGO_ENABLED=0 go build -o /out/continuum-agent ./cmd/agent \
 && CGO_ENABLED=0 go build -o /out/continuum ./cmd/continuum

# Agent (FR-N04/N08): docker run … continuum-agent run --controller https://controller:8443 --token <CTM1…> --root /disks/d1
FROM gcr.io/distroless/static-debian12 AS agent
COPY --from=build /out/continuum-agent /continuum-agent
ENV CONTINUUM_AGENT_DIR=/state
VOLUME ["/state"]
ENTRYPOINT ["/continuum-agent"]
CMD ["run"]

# Web UI (apps/web) built once and served by the Controller from /web (same origin, no separate web server)
FROM node:22-bookworm-slim AS web
WORKDIR /src
RUN corepack enable && corepack prepare pnpm@11.10.0 --activate
COPY package.json pnpm-lock.yaml pnpm-workspace.yaml ./
COPY apps/web/package.json apps/web/
COPY apps/desktop/package.json apps/desktop/
COPY apps/extension/package.json apps/extension/
RUN pnpm install --frozen-lockfile --filter @continuum/web...
COPY apps/web apps/web
RUN pnpm --filter @continuum/web build

FROM gcr.io/distroless/static-debian12:nonroot AS controller
COPY --from=build /out/controller /controller
COPY --from=build /out/continuum /continuum
COPY --from=web /src/apps/web/dist /web
COPY --from=build --chown=65532:65532 /out/data /data
ENV CONTINUUM_WEB_DIR=/web
USER 65532:65532
EXPOSE 8080 8443
CMD ["/controller"]
