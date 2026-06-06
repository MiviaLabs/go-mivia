FROM golang:1.26.3-bookworm AS build

WORKDIR /src

COPY go.mod go.sum ./
RUN go mod download

COPY . .
RUN CGO_ENABLED=1 go build -trimpath -ldflags="-s -w" -o /out/mivia-server ./cmd/mivia-server \
    && CGO_ENABLED=1 go build -trimpath -ldflags="-s -w" -o /out/mivia-automation-runner ./cmd/mivia-automation-runner

FROM node:24-bookworm-slim AS codex-cli

RUN npm install -g @openai/codex@latest pnpm@latest \
    && codex --version

FROM debian:bookworm-20260518-slim AS runtime

RUN apt-get update \
    && apt-get install -y --no-install-recommends bash ca-certificates curl file gh git libglu1-mesa openssh-client python3 python3-pip python3-venv ripgrep socat unzip xz-utils zip \
    && rm -rf /var/lib/apt/lists/*

RUN python3 -m venv /opt/mivia-python-tools \
    && /opt/mivia-python-tools/bin/pip install --no-cache-dir --upgrade pip \
    && /opt/mivia-python-tools/bin/pip install --no-cache-dir 'semgrep>=1.108.0' \
    && ln -s /opt/mivia-python-tools/bin/semgrep /usr/local/bin/semgrep \
    && semgrep --version

ARG FLUTTER_GIT_REF=stable
RUN git clone --depth 1 --branch "${FLUTTER_GIT_REF}" https://github.com/flutter/flutter.git /opt/flutter \
    && git config --global --add safe.directory /opt/flutter \
    && /opt/flutter/bin/flutter --version \
    && /opt/flutter/bin/dart --version

RUN useradd --create-home --uid 10001 --shell /usr/sbin/nologin mivia \
    && mkdir -p /var/lib/mivia/projects /app \
    && chown -R mivia:mivia /var/lib/mivia /app

COPY --from=build /out/mivia-server /usr/local/bin/mivia-server
COPY --from=build /out/mivia-automation-runner /usr/local/bin/mivia-automation-runner
COPY --from=build /usr/local/go /usr/local/go
COPY --from=codex-cli /usr/local/bin/node /usr/local/bin/node
COPY --from=codex-cli /usr/local/lib/node_modules /usr/local/lib/node_modules
COPY docker/entrypoint.sh /usr/local/bin/mivia-entrypoint
COPY docker/automation-runner-entrypoint.sh /usr/local/bin/mivia-runner-entrypoint
COPY docker/graphify /usr/local/bin/graphify
ENV PATH="/opt/flutter/bin:/usr/local/go/bin:/usr/local/bin:${PATH}"
RUN ln -s ../lib/node_modules/@openai/codex/bin/codex.js /usr/local/bin/codex \
	&& ln -s ../lib/node_modules/pnpm/bin/pnpm.cjs /usr/local/bin/pnpm \
	&& ln -s ../lib/node_modules/pnpm/bin/pnpx.cjs /usr/local/bin/pnpx \
	&& ln -s /opt/flutter/bin/flutter /usr/local/bin/flutter \
	&& ln -s /opt/flutter/bin/dart /usr/local/bin/dart \
	&& chmod 0755 /usr/local/lib/node_modules/pnpm/bin/pnpm.cjs \
	&& chmod 0755 /usr/local/lib/node_modules/pnpm/bin/pnpx.cjs \
	&& chmod 0755 /usr/local/bin/mivia-entrypoint \
	&& chmod 0755 /usr/local/bin/mivia-runner-entrypoint \
	&& chmod 0755 /usr/local/bin/graphify \
	&& pnpm --version \
	&& flutter --version \
	&& dart --version \
	&& go version \
	&& graphify --version \
	&& codex --version

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
