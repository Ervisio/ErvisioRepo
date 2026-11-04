# syntax=docker/dockerfile:1
# ErvisioRepo image: server, runner and CLI in one Alpine image.
#   docker build --build-arg VERSION=1.2.3 --build-arg COMMIT=abc1234 -t ervisiorepo:local .

FROM node:22-alpine AS web
WORKDIR /src/web
COPY web/package.json web/package-lock.json ./
RUN npm ci --no-audit --no-fund
COPY web/ ./
RUN npm run build

FROM golang:1.26-alpine AS build
ARG VERSION=dev
ARG COMMIT=unknown
ENV CGO_ENABLED=0
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
COPY --from=web /src/web/dist/ web/dist/
RUN set -eu; \
    ldflags="-s -w -X github.com/Ervisio/ErvisioRepo/internal/buildinfo.version=${VERSION} -X github.com/Ervisio/ErvisioRepo/internal/buildinfo.commit=${COMMIT}"; \
    mkdir -p /out; \
    go build -trimpath -ldflags "$ldflags" -o /out/ervisio-repo ./cmd/ervisio-repo; \
    for c in ervisio-runner erepo; do \
      if ls cmd/$c/*.go >/dev/null 2>&1; then go build -trimpath -ldflags "$ldflags" -o /out/$c ./cmd/$c; fi; \
    done

FROM alpine:3.22
# git: every repository operation (internal/git); git-lfs: LFS clients' filters;
# postgresql16-client: pg_dump / pg_restore for backups and `restore`.
RUN apk add --no-cache ca-certificates tzdata git git-lfs openssh-client postgresql16-client tini wget \
    && addgroup -g 1001 -S ervisio-repo \
    && adduser -u 1001 -S -G ervisio-repo -h /var/lib/ervisio-repo ervisio-repo \
    && mkdir -p /var/lib/ervisio-repo /run/ervisio-repo /etc/ervisio-repo \
    && chown ervisio-repo:ervisio-repo /var/lib/ervisio-repo /run/ervisio-repo
COPY --from=build /out/ /usr/local/bin/
USER ervisio-repo
ENV HOME=/var/lib/ervisio-repo
EXPOSE 3400 2222
VOLUME ["/var/lib/ervisio-repo"]
HEALTHCHECK --interval=30s --timeout=5s --start-period=20s --retries=3 \
    CMD wget -q -O /dev/null http://127.0.0.1:3400/healthz || exit 1
ENTRYPOINT ["/sbin/tini", "--"]
CMD ["ervisio-repo", "serve", "--config", "/etc/ervisio-repo/config.toml"]
