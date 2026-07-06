# syntax=docker/dockerfile:1

# ---- Build stage: compile a static binary ----------------------------------
FROM golang:1.26.4-alpine3.22 AS build

WORKDIR /src

COPY go.mod go.sum ./
RUN go mod download

COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" \
        -o /out/ssh-arcadelobby ./cmd/ssh-arcadelobby

# Pre-create the data directory with the runtime user's ownership so the
# named volume inherits writable permissions on first use.
RUN mkdir -p /out/data-dir && chown 65532:65532 /out/data-dir

# ---- mc: MinIO Client, used only for the router's own host-key and proxy-
# key backup objects (doc 06: keys/router/) — S3-compatible, so the same
# entrypoint logic works against real S3 in prod and MinIO in local drills.
FROM minio/mc:RELEASE.2025-08-13T08-35-41Z AS mc

# ---- Runtime stage ----------------------------------------------------------
# alpine:3, not distroless: per the canonical fleet durability doc
# (docs/06-fleet-data-durability.md), the entrypoint needs a shell to
# restore/back up the router's keys. The router holds no SQLite, so unlike
# a game's image there is no litestream here at all.
FROM alpine:3.22

RUN apk add --no-cache ca-certificates && \
    addgroup -g 65532 nonroot && \
    adduser -D -H -u 65532 -G nonroot nonroot

COPY --from=mc /usr/bin/mc /usr/local/bin/mc
COPY --from=build /out/ssh-arcadelobby /app/ssh-arcadelobby
COPY --from=build --chown=65532:65532 /out/data-dir /var/lib/arcade
COPY entrypoint.sh /entrypoint.sh
RUN chmod 755 /entrypoint.sh

# The app's own default port is 22; inside the container it listens on an
# unprivileged port instead and the operator maps host 22 to it, so the
# process never needs root or CAP_NET_BIND_SERVICE.
#
# HOME: the nonroot user has no /home/nonroot (and can't create one — /home
# is root-owned), but `mc` writes its config there even when every alias
# comes from an MC_HOST_* env var, so without this every `mc` call in
# entrypoint.sh fails with "Unable to save new mc config" — silently, since
# entrypoint.sh redirects mc's stderr to /dev/null. /tmp is already
# world-writable in the base image. (Found the hard way in ssh-farm's own
# entrypoint — see its Dockerfile/entrypoint.sh history.)
ENV ARCADE_LISTEN_PORT=2222 \
    ARCADE_HOST_KEY_PATH=/var/lib/arcade/ssh_host_key \
    ARCADE_PROXY_KEY_PATH=/var/lib/arcade/proxy_key \
    ARCADE_GAMES_PATH=/etc/arcade/games.toml \
    HOME=/tmp

EXPOSE 2222

# The host key (and, in dev, the proxy key) live here; in production the
# proxy key is instead a mounted read-only secret at a different path (see
# deploy/docker-compose.yml). The volume is a cache: durability for the
# host/proxy keys comes from S3 backup (ARCADE_*_KEY_MC_PATH), not from the
# volume itself — the router has no other state to lose.
VOLUME ["/var/lib/arcade"]

USER nonroot:nonroot

ENTRYPOINT ["/entrypoint.sh"]
