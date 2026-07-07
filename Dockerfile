# syntax=docker/dockerfile:1

# ---- Build stage: compile a static binary ----------------------------------
FROM golang:1.26.4-alpine3.22 AS build

WORKDIR /src

COPY go.mod go.sum ./
RUN go mod download

COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" \
        -o /out/ssh-arcadelobby ./cmd/ssh-arcadelobby

RUN mkdir -p /out/data-dir && chown 65532:65532 /out/data-dir

# ---- Litestream: pinned, pulled as a static binary --------------------------
FROM litestream/litestream:0.5.12 AS litestream

# ---- mc: MinIO Client for host/proxy key backup objects ---------------------
FROM minio/mc:RELEASE.2025-08-13T08-35-41Z AS mc

# ---- Runtime stage ----------------------------------------------------------
FROM alpine:3.22

RUN apk add --no-cache ca-certificates && \
    addgroup -g 65532 nonroot && \
    adduser -D -H -u 65532 -G nonroot nonroot

COPY --from=litestream /usr/local/bin/litestream /usr/local/bin/litestream
COPY --from=mc /usr/bin/mc /usr/local/bin/mc
COPY --from=build /out/ssh-arcadelobby /app/ssh-arcadelobby
COPY --from=build --chown=65532:65532 /out/data-dir /var/lib/arcade
COPY etc/litestream.yml /etc/litestream.yml
COPY entrypoint.sh /entrypoint.sh
RUN chmod 755 /entrypoint.sh

ENV ARCADE_LISTEN_PORT=2222 \
    ARCADE_HOST_KEY_PATH=/var/lib/arcade/ssh_host_key \
    ARCADE_PROXY_KEY_PATH=/var/lib/arcade/proxy_key \
    ARCADE_GAMES_PATH=/etc/arcade/games.toml \
    ARCADE_BANNER_PATH=/etc/arcade/banner.toml \
    ARCADE_DB_PATH=/var/lib/arcade/arcade.db \
    HOME=/tmp

EXPOSE 2222

VOLUME ["/var/lib/arcade"]

USER nonroot:nonroot

ENTRYPOINT ["/entrypoint.sh"]
