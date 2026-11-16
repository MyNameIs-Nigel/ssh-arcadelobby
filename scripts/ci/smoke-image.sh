#!/usr/bin/env bash
set -euo pipefail

image="${1:?Usage: smoke-image.sh IMAGE VERSION}"
version="${2:?Usage: smoke-image.sh IMAGE VERSION}"
scratch=$(mktemp -d)
network=''
fixture=''
router=''

# Invoked by the EXIT trap.
# shellcheck disable=SC2329
cleanup() {
  if [[ -n "$router" ]]; then docker rm -fv "$router" >/dev/null || true; fi
  if [[ -n "$fixture" ]]; then docker rm -fv "$fixture" >/dev/null || true; fi
  if [[ -n "$network" ]]; then docker network rm "$network" >/dev/null || true; fi
  rm -rf "$scratch"
}
trap 'cleanup' EXIT
network=$(docker network create --internal "arcade-smoke-${scratch##*/}")

cat > "$scratch/games.toml" <<'TOML'
[[games]]
id = "fixture"
name = "Smoke Test"
tagline = "Disposable fixture"
addr = "fixture:2222"
TOML
chmod 644 "$scratch/games.toml"

# The registry only needs an SSH identification line to probe a backend.
fixture=$(docker run -d --platform linux/amd64 --network "$network" \
  --network-alias fixture --read-only --entrypoint sh "$image" -c \
  'while true; do printf "SSH-2.0-9.8.7\r\n" | nc -l -p 2222; done')

router=$(docker run -d --platform linux/amd64 --network "$network" \
  --read-only --cap-drop ALL --security-opt no-new-privileges \
  --tmpfs /tmp --tmpfs /var/lib/arcade:uid=65532,gid=65532,mode=0700 \
  --mount "type=bind,src=$scratch/games.toml,dst=/etc/arcade/games.toml,readonly" \
  "$image")

read_banner() {
  docker exec "$router" sh -c 'timeout 2 nc "$1" 2222 </dev/null | head -n 1' sh "$1" |
    tr -d '\r'
}

for _ in {1..15}; do
  if [[ $(read_banner 127.0.0.1) == "SSH-2.0-$version" &&
        $(read_banner fixture) == "SSH-2.0-9.8.7" ]]; then
    echo "Router advertised SSH-2.0-$version and could reach the fixture game."
    exit 0
  fi
  sleep 1
done

docker logs "$router" >&2
docker logs "$fixture" >&2
echo "Could not verify the router banner or fixture game." >&2
exit 1
