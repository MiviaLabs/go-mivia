FROM golang:1.26.3-bookworm AS build

WORKDIR /src

COPY go.mod go.sum ./
RUN --mount=type=cache,target=/go/pkg/mod \
    go mod download

COPY . .
RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=1 go build -trimpath -ldflags="-s -w" -o /out/mivia-server ./cmd/mivia-server

FROM debian:bookworm-20260518-slim AS runtime

RUN apt-get update \
    && apt-get install -y --no-install-recommends ca-certificates curl git socat \
    && rm -rf /var/lib/apt/lists/*

RUN useradd --create-home --uid 10001 --shell /usr/sbin/nologin mivia \
    && mkdir -p /var/lib/mivia /app \
    && chown -R mivia:mivia /var/lib/mivia /app

COPY --from=build /out/mivia-server /usr/local/bin/mivia-server
COPY docker/entrypoint.sh /usr/local/bin/mivia-entrypoint
RUN chmod 0755 /usr/local/bin/mivia-entrypoint

USER mivia
WORKDIR /app

ENV MIVIA_INTERNAL_ADDR=127.0.0.1:18080
ENV MIVIA_PUBLIC_PORT=8080
ENV MIVIA_LADYBUG_PATH=/var/lib/mivia/mivialabs.lbug
ENV MIVIA_SQLITE_PATH=/var/lib/mivia/mivialabs-config.sqlite

EXPOSE 8080

HEALTHCHECK --interval=10s --timeout=3s --start-period=5s --retries=6 \
    CMD curl -fsS "http://${MIVIA_INTERNAL_ADDR}/readyz" >/dev/null || exit 1

ENTRYPOINT ["mivia-entrypoint"]
