#!/usr/bin/env bash
set -euo pipefail

cd "$(dirname "$0")/../.."

case "${1:-}" in
  windows)
    go build ./...
    go test -count=1 -run '^Test(RejectsSessionWithoutPTY|MenuRendersGamesAndIdentity|LobbyBridgesToGameAndBack)$' ./internal/server
    exit
    ;;
  ''|release) ;;
  *) echo "Usage: $0 [windows|release]" >&2; exit 2 ;;
esac

python="${PYTHON:-python3}"
scratch=$(mktemp -d)
trap 'rm -rf "$scratch"' EXIT

actionlint
shellcheck entrypoint.sh scripts/dev-local-fleet.sh scripts/ci/*.sh scripts/deploy/remote-deploy.sh deploy/monitoring/*.sh

go vet ./...
go build ./...
go test -race -count=1 -coverprofile="$scratch/coverage.out" ./...
"$python" scripts/ci/check-coverage.py "$scratch/coverage.out"
go test -tags releasecheck \
  -ldflags="-X github.com/mynameis-nigel/ssh-arcadelobby/internal/version.Version=2.4.3" \
  ./internal/version ./internal/server ./internal/lobby
"$python" scripts/deploy/test_deploy.py

docker compose --env-file /dev/null -f deploy/docker-compose.yml config --format json > "$scratch/compose.json"
caddy_image=$("$python" scripts/ci/check-fleet.py "$scratch/compose.json" deploy/games.toml)
docker run --rm --network none --entrypoint caddy \
  --mount "type=bind,src=$PWD/deploy/Caddyfile,dst=/etc/caddy/Caddyfile,readonly" \
  "$caddy_image" validate --adapter caddyfile --config /etc/caddy/Caddyfile
if [[ ${1:-} != release ]]; then
  image=$(docker build --platform linux/amd64 --quiet .)
  scripts/ci/smoke-image.sh "$image" dev
fi
