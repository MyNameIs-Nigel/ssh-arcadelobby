#!/bin/sh
# Runs on the EC2 host via SSM. Keep this script identical in all four repos.
set -eu
: "${SERVICE:?}" "${IMAGE:?}" "${VERSION:?}"
case "$SERVICE" in
  router) REPO=ssh-arcadelobby; IMAGE_KEY=ROUTER_IMAGE ;;
  farm) REPO=ssh-farm; IMAGE_KEY=FARM_IMAGE ;;
  moonminer) REPO=ssh-moonminer; IMAGE_KEY=MOONMINER_IMAGE ;;
  chess) REPO=ssh-chess; IMAGE_KEY=CHESS_IMAGE ;;
  *) echo 'Unknown service' >&2; exit 1 ;;
esac
IMAGE_REPO="ghcr.io/mynameis-nigel/$REPO"
printf '%s\n' "$IMAGE" | grep -Eq "^${IMAGE_REPO}@sha256:[0-9a-f]{64}$"
printf '%s\n' "$VERSION" | grep -Eq '^(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)$'
cd "${COMPOSE_DIR:-/srv/ssharcade}"
# GitHub concurrency is repository-scoped; the host lock covers the whole fleet.
exec 9>.deploy.lock
flock -w 600 9
backup=$(mktemp -d)
changed=0
restarted=0
previous=''
previous_version=''
for file in .env docker-compose.yml games.toml Caddyfile; do
  if [ -f "$file" ]; then cp "$file" "$backup/$file"; fi
done

set_image() {
  target=$(mktemp .env.XXXXXX)
  # Preserve unrelated settings, without printing them to SSM logs.
  if [ -f .env ]; then awk -v key="$IMAGE_KEY" 'index($0, key "=") != 1' .env > "$target"; fi
  printf '%s=%s\n' "$IMAGE_KEY" "$1" >> "$target"
  chmod 600 "$target"
  mv "$target" .env
}
banner() {
  docker compose exec -T "$SERVICE" sh -c 'timeout 5 nc 127.0.0.1 2222 </dev/null | head -1' 2>/dev/null | tr -d '\r\n'
}
wait_version() {
  attempt=0
  while [ "$attempt" -lt "${PROBE_ATTEMPTS:-30}" ]; do
    got=$(banner || true)
    if [ -n "$1" ]; then
      if [ "$got" = "SSH-2.0-$1" ]; then return 0; fi
    else
      # No old banner was available for a stopped service; prove it is serving.
      case "$got" in SSH-2.0-?*) return 0 ;; esac
    fi
    sleep 2
    attempt=$((attempt + 1))
  done
  return 1
}
start() {
  docker compose up -d --no-deps --pull never "$SERVICE"
  if [ "$SERVICE" = router ]; then
    docker compose up -d --no-deps web
    docker compose exec -T web caddy reload --config /etc/caddy/Caddyfile --adapter caddyfile
  fi
}
rollback() {
  echo 'Deployment failed; restoring previous configuration and image.' >&2
  for file in .env docker-compose.yml games.toml Caddyfile; do
    if [ -f "$backup/$file" ]; then
      # Preserve inodes for single-file bind mounts.
      cat "$backup/$file" > "$file" || return 1
    elif [ "$file" = .env ]; then
      rm -f .env || return 1
    fi
  done
  if [ "$restarted" = 0 ]; then return 0; fi
  if [ -z "$previous" ]; then
    echo 'No previous local image is available; stopping the failed new container.' >&2
    docker compose stop "$SERVICE" || return 1
    return 1
  fi
  set_image "$previous" || return 1
  start || return 1
  wait_version "$previous_version" || return 1
  echo 'Previous build restored.'
}
cleanup() {
  rc=$?
  trap - EXIT
  if [ "$rc" -ne 0 ] && [ "$changed" = 1 ]; then
    rollback || echo 'ROLLBACK FAILED: inspect the service on the host.' >&2
  fi
  rm -rf "$backup"
  exit "$rc"
}
trap cleanup EXIT
trap 'exit 1' HUP INT TERM
container=$(docker compose ps --status running --quiet "$SERVICE")
if [ -n "$container" ]; then
  current_banner=$(banner || true)
  case "$current_banner" in
    SSH-2.0-*) previous_version=${current_banner#SSH-2.0-} ;;
    *) echo 'Cannot probe the current service; refusing to deploy.' >&2; exit 1 ;;
  esac
  running_image=$(docker inspect --format '{{.Image}}' "$container")
  previous=$(docker inspect --format '{{.Config.Image}}' "$container")
  case "$previous" in
    "$IMAGE_REPO"@sha256:*) ;;
    *) previous="$IMAGE_REPO:rollback"; docker tag "$running_image" "$previous" ;;
  esac
else
  # The service may be stopped or absent. Resolve its existing Compose image,
  # including .env interpolation/defaults, and snapshot it only if cached locally.
  configured=$(docker compose config --images "$SERVICE")
  case "$configured" in
    "$IMAGE_REPO"@sha256:*|"$IMAGE_REPO":*)
      if cached=$(docker image inspect --format '{{.Id}}' "$configured" 2>/dev/null); then
        case "$configured" in
          "$IMAGE_REPO"@sha256:*) previous=$configured ;;
          *) previous="$IMAGE_REPO:rollback"; docker tag "$cached" "$previous" ;;
        esac
      fi
      ;;
  esac
fi
# Pull before touching the running configuration.
docker pull --quiet "$IMAGE"
changed=1
if [ "$SERVICE" = router ]; then
  : "${COMPOSE_B64:?}" "${GAMES_B64:?}" "${CADDY_B64:?}"
  printf '%s' "$COMPOSE_B64" | base64 -d > docker-compose.yml
  printf '%s' "$GAMES_B64" | base64 -d > games.toml
  printf '%s' "$CADDY_B64" | base64 -d > Caddyfile
fi
set_image "$IMAGE"
# Older fleet compose files lack image variables: fail before restarting anything.
if ! docker compose config --images | grep -Fxq "$IMAGE"; then
  echo 'Compose does not resolve to the requested image. Release the updated lobby first.' >&2
  exit 1
fi
restarted=1
start
if ! wait_version "$VERSION"; then
  echo "Service did not advertise release $VERSION." >&2
  exit 1
fi
changed=0
echo "Deployed $SERVICE $VERSION ($IMAGE)"
