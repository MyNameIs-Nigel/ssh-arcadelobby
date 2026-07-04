#!/usr/bin/env bash
# Launch ssh-farm + ssh-arcadelobby locally for manual play-testing.
# Usage: ./scripts/dev-local-fleet.sh  (from ssh-arcadelobby; ssh-farm must be a sibling repo)
# Then:  ssh -p 2222 -i var/dev-fleet/dev_player_key scout@127.0.0.1
set -euo pipefail

ARCADE="$(cd "$(dirname "$0")/.." && pwd)"
FARM="$(cd "$ARCADE/../ssh-farm" && pwd)"
VAR="$ARCADE/var/dev-fleet"

mkdir -p "$VAR"

if [[ ! -f "$VAR/proxy_key" ]]; then
  ssh-keygen -t ed25519 -f "$VAR/proxy_key" -N "" -C "arcade-dev-proxy" >/dev/null
fi
PROXY_PUB=$(ssh-keygen -y -f "$VAR/proxy_key")

if [[ ! -f "$VAR/dev_player_key" ]]; then
  ssh-keygen -t ed25519 -f "$VAR/dev_player_key" -N "" -C "dev-player" >/dev/null
fi

for svc in arcade farm; do
  if [[ ! -f "$VAR/${svc}_host_key" ]]; then
    ssh-keygen -t ed25519 -f "$VAR/${svc}_host_key" -N "" -C "${svc}-dev" >/dev/null
  fi
done

echo "$PROXY_PUB" > "$VAR/proxy_keys"
chmod 600 "$VAR/proxy_key" "$VAR/proxy_keys"

cleanup() {
  echo ""
  echo "Stopping fleet..."
  [[ -n "${FARM_PID:-}" ]] && kill "$FARM_PID" 2>/dev/null || true
  [[ -n "${ARCADE_PID:-}" ]] && kill "$ARCADE_PID" 2>/dev/null || true
  wait 2>/dev/null || true
}
trap cleanup EXIT INT TERM

echo "Building..."
(cd "$FARM" && go build -o "$VAR/ssh-farm" ./cmd/ssh-farm)
(cd "$ARCADE" && go build -o "$VAR/ssh-arcadelobby" ./cmd/ssh-arcadelobby)

echo "Starting ssh-farm on :2223..."
FARM_LISTEN_PORT=2223 \
FARM_HOST_KEY_PATH="$VAR/farm_host_key" \
FARM_DB_PATH="$VAR/farm.db" \
FARM_PROXY_KEYS_PATH="$VAR/proxy_keys" \
FARM_LOG_LEVEL=info \
  "$VAR/ssh-farm" &
FARM_PID=$!
sleep 0.5

echo "Starting ssh-arcadelobby on :2222..."
ARCADE_LISTEN_PORT=2222 \
ARCADE_HOST_KEY_PATH="$VAR/arcade_host_key" \
ARCADE_PROXY_KEY_PATH="$VAR/proxy_key" \
ARCADE_GAMES_PATH="$ARCADE/games.local.toml" \
ARCADE_PROBE_INTERVAL=2s \
ARCADE_LOG_LEVEL=info \
  "$VAR/ssh-arcadelobby" &
ARCADE_PID=$!
sleep 1

echo ""
echo "Fleet is up."
echo "  ssh -p 2222 -i $VAR/dev_player_key scout@127.0.0.1"
echo ""
echo "Press Ctrl+C to stop."

wait
