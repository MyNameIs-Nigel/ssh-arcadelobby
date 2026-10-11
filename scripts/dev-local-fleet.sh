#!/usr/bin/env bash
# Launch all four sibling repos; --guests explicitly enables disposable play.
# Usage: ./scripts/dev-local-fleet.sh [--guests]
set -euo pipefail

GUESTS=false
case "${1:-}" in
  "") ;;
  --guests) GUESTS=true ;;
  *) echo "Usage: $0 [--guests]" >&2; exit 2 ;;
esac

ARCADE="$(cd "$(dirname "$0")/.." && pwd)"
FARM="$(cd "$ARCADE/../ssh-farm" && pwd)"
MOON="$(cd "$ARCADE/../ssh-moonminer" && pwd)"
CHESS="$(cd "$ARCADE/../ssh-chess" && pwd)"
VAR="$ARCADE/var/dev-fleet"
mkdir -p "$VAR"

for key in proxy_key dev_player_key arcade_host_key farm_host_key moon_host_key chess_host_key; do
  if [[ ! -f "$VAR/$key" ]]; then
    ssh-keygen -t ed25519 -f "$VAR/$key" -N "" -C "arcade-dev-$key" >/dev/null
  fi
done
ssh-keygen -y -f "$VAR/proxy_key" > "$VAR/proxy_keys"
chmod 600 "$VAR/proxy_key" "$VAR/proxy_keys"

PIDS=()
cleanup() {
  trap - EXIT INT TERM
  echo "Stopping fleet..."
  for pid in "${PIDS[@]}"; do kill "$pid" 2>/dev/null || true; done
  wait 2>/dev/null || true
}
trap cleanup EXIT INT TERM

echo "Building..."
(cd "$FARM" && go build -o "$VAR/ssh-farm" ./cmd/ssh-farm)
(cd "$MOON" && go build -o "$VAR/ssh-moonminer" ./cmd/ssh-moonminer)
(cd "$CHESS" && go build -o "$VAR/ssh-chess" ./cmd/ssh-chess)
(cd "$ARCADE" && go build -o "$VAR/ssh-arcadelobby" ./cmd/ssh-arcadelobby)

FARM_LISTEN_HOST=127.0.0.1 FARM_LISTEN_PORT=2223 FARM_HOST_KEY_PATH="$VAR/farm_host_key" \
FARM_DB_PATH="$VAR/farm.db" FARM_PROXY_KEYS_PATH="$VAR/proxy_keys" \
FARM_ALLOW_GUESTS="$GUESTS" FARM_LOG_LEVEL=info \
  "$VAR/ssh-farm" &
PIDS+=("$!")

MOONMINER_LISTEN_HOST=127.0.0.1 MOONMINER_LISTEN_PORT=2224 MOONMINER_HOST_KEY_PATH="$VAR/moon_host_key" \
MOONMINER_DB_PATH="$VAR/moonminer.db" MOONMINER_PROXY_KEYS_PATH="$VAR/proxy_keys" \
MOONMINER_ALLOW_GUESTS="$GUESTS" MOONMINER_LOG_LEVEL=info \
  "$VAR/ssh-moonminer" &
PIDS+=("$!")

CHESS_LISTEN_HOST=127.0.0.1 CHESS_LISTEN_PORT=2225 CHESS_HOST_KEY_PATH="$VAR/chess_host_key" \
CHESS_DB_PATH="$VAR/chess.db" CHESS_PROXY_KEYS_PATH="$VAR/proxy_keys" \
CHESS_LOG_LEVEL=info \
  "$VAR/ssh-chess" &
PIDS+=("$!")

ARCADE_LISTEN_HOST=127.0.0.1 ARCADE_LISTEN_PORT=2222 ARCADE_HOST_KEY_PATH="$VAR/arcade_host_key" \
ARCADE_PROXY_KEY_PATH="$VAR/proxy_key" ARCADE_GAMES_PATH="$ARCADE/games.local.toml" \
ARCADE_DB_PATH="$VAR/arcade.db" ARCADE_PROBE_INTERVAL=2s \
ARCADE_ALLOW_GUESTS="$GUESTS" ARCADE_LOG_LEVEL=info \
  "$VAR/ssh-arcadelobby" &
PIDS+=("$!")

echo ""
echo "Fleet starting; offline rows become available after the first probe."
echo "  ssh -p 2222 -i $VAR/dev_player_key scout@127.0.0.1"
if [[ "$GUESTS" == true ]]; then
  echo "Guest: ssh -p 2222 -o PubkeyAuthentication=no -o PreferredAuthentications=keyboard-interactive guest@127.0.0.1"
  echo "Type guest at the prompt. Farm and Moon Miner reset when you leave."
fi
echo "Press Ctrl+C to stop."
wait
