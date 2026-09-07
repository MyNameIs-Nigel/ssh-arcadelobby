#!/bin/sh
# Container entrypoint implementing the canonical fleet durability pattern
# (docs/06-fleet-data-durability.md) for the router: SQLite player prefs,
# SSH host key, and proxy (bridge client) key backup objects.
set -eu

DB_PATH="${ARCADE_DB_PATH:-/var/lib/arcade/arcade.db}"
HOST_KEY_PATH="${ARCADE_HOST_KEY_PATH:-/var/lib/arcade/ssh_host_key}"
PROXY_KEY_PATH="${ARCADE_PROXY_KEY_PATH:-/var/lib/arcade/proxy_key}"
HOST_KEY_MC_PATH="${ARCADE_HOST_KEY_MC_PATH:-}"
PROXY_KEY_MC_PATH="${ARCADE_PROXY_KEY_MC_PATH:-}"

restore_key() {
	key_path="$1"
	mc_path="$2"
	[ -z "$mc_path" ] && return 0
	[ -f "$key_path" ] && return 0
	echo "entrypoint: restoring $key_path from $mc_path"
	if mc cat "$mc_path" >"$key_path.tmp" 2>/dev/null && [ -s "$key_path.tmp" ]; then
		mv "$key_path.tmp" "$key_path"
		chmod 600 "$key_path"
	else
		rm -f "$key_path.tmp"
		echo "entrypoint: no $key_path found in bucket yet — a new one will be generated and uploaded"
	fi
}

backup_key_when_present() {
	key_path="$1"
	mc_path="$2"
	[ -z "$mc_path" ] && return 0
	i=0
	while [ "$i" -lt 30 ]; do
		if [ -f "$key_path" ]; then
			if mc stat "$mc_path" >/dev/null 2>&1; then
				echo "entrypoint: $mc_path already present — not overwriting"
			else
				mc pipe "$mc_path" <"$key_path" 2>/dev/null \
					&& echo "entrypoint: $key_path uploaded to $mc_path"
			fi
			return 0
		fi
		i=$((i + 1))
		sleep 1
	done
}

# Production guard (§4.1, failure mode 1). The dev-mode fall-through below is a
# real convenience — local dev and CI must never need AWS credentials — but in
# production it is the most dangerous failure in the fleet: the router serves
# players normally while nothing replicates, and the loss stays silent until
# someone actually needs a restore. Setting ARCADE_REQUIRE_REPLICATION=true
# turns that fall-through into a crash loop, noticed in seconds instead.
if [ "${ARCADE_REQUIRE_REPLICATION:-}" = "true" ] && [ -z "${LITESTREAM_REPLICA_URL:-}" ]; then
	echo "entrypoint: FATAL — ARCADE_REQUIRE_REPLICATION=true but LITESTREAM_REPLICA_URL is unset; refusing to serve unreplicated" >&2
	exit 1
fi

# Dev mode: no replica configured — skip litestream, still allow optional
# S3 key backup when only MC paths are set.
if [ -z "${LITESTREAM_REPLICA_URL:-}" ]; then
	if [ -z "$HOST_KEY_MC_PATH" ] && [ -z "$PROXY_KEY_MC_PATH" ]; then
		echo "entrypoint: LITESTREAM_REPLICA_URL not set — running WITHOUT replication (dev mode)"
		exec /app/ssh-arcadelobby
	fi
	echo "entrypoint: LITESTREAM_REPLICA_URL not set — running WITHOUT DB replication (key backup only)"
	restore_key "$HOST_KEY_PATH" "$HOST_KEY_MC_PATH"
	restore_key "$PROXY_KEY_PATH" "$PROXY_KEY_MC_PATH"
	(
		backup_key_when_present "$HOST_KEY_PATH" "$HOST_KEY_MC_PATH"
		backup_key_when_present "$PROXY_KEY_PATH" "$PROXY_KEY_MC_PATH"
	) &
	exec /app/ssh-arcadelobby
fi

restore_key "$HOST_KEY_PATH" "$HOST_KEY_MC_PATH"
restore_key "$PROXY_KEY_PATH" "$PROXY_KEY_MC_PATH"

echo "entrypoint: restoring $DB_PATH from $LITESTREAM_REPLICA_URL if needed"
litestream restore -if-db-not-exists -if-replica-exists "$DB_PATH"

if [ -n "$HOST_KEY_MC_PATH" ]; then
	(
		i=0
		while [ "$i" -lt 30 ]; do
			if [ -f "$HOST_KEY_PATH" ]; then
				if mc stat "$HOST_KEY_MC_PATH" >/dev/null 2>&1; then
					echo "entrypoint: host key already in bucket at $HOST_KEY_MC_PATH — not overwriting"
				else
					mc pipe "$HOST_KEY_MC_PATH" <"$HOST_KEY_PATH" 2>/dev/null \
						&& echo "entrypoint: host key uploaded to $HOST_KEY_MC_PATH"
				fi
				break
			fi
			i=$((i + 1))
			sleep 1
		done
	) &
fi
if [ -n "$PROXY_KEY_MC_PATH" ]; then
	(
		i=0
		while [ "$i" -lt 30 ]; do
			if [ -f "$PROXY_KEY_PATH" ]; then
				if mc stat "$PROXY_KEY_MC_PATH" >/dev/null 2>&1; then
					echo "entrypoint: proxy key already in bucket at $PROXY_KEY_MC_PATH — not overwriting"
				else
					mc pipe "$PROXY_KEY_MC_PATH" <"$PROXY_KEY_PATH" 2>/dev/null \
						&& echo "entrypoint: proxy key uploaded to $PROXY_KEY_MC_PATH"
				fi
				break
			fi
			i=$((i + 1))
			sleep 1
		done
	) &
fi

exec litestream replicate -exec "/app/ssh-arcadelobby"
