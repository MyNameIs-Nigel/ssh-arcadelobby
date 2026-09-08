#!/bin/sh
# Container entrypoint implementing the canonical fleet durability pattern
# (docs/06-fleet-data-durability.md) for the router: SQLite player prefs plus
# the SSH host key that gives play.ssharcade.dev its identity.
set -eu

DB_PATH="${ARCADE_DB_PATH:-/var/lib/arcade/arcade.db}"
HOST_KEY_PATH="${ARCADE_HOST_KEY_PATH:-/var/lib/arcade/ssh_host_key}"
# Read-only bind mount of the host's canonical copy of the router's SSH host
# key (/srv/ssharcade/private/keys/router on the box).
#
# This replaces an `mc cat` from S3, and with it the static AWS key that used
# to sit in this container's environment as MC_HOST_s3. mc has no support for
# the EC2 instance role, so using it meant a long-lived access key pair in the
# environment of all four services — the one credential in the fleet that could
# not be replaced by the role litestream already uses.
HOST_KEY_SOURCE="${ARCADE_HOST_KEY_SOURCE:-}"

# The proxy key is deliberately absent from this script. It arrives as a Docker
# secret at ARCADE_PROXY_KEY_PATH (/run/secrets/proxy_key), provisioned by hand
# on the host and never in git — see the hard rules in the fleet CLAUDE.md. It
# used to be restored from and uploaded to S3 here, which meant this container
# held credentials that could read and overwrite the fleet's crown jewel:
# whoever holds that key can forge any player's identity into any game. It is
# now host state only, and the host backs it up with its own instance role.

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

# Seed the data volume from the host's copy when this volume has no key yet —
# a rebuilt host, or a volume recreated by a compose change. A normal boot
# skips this entirely, because the volume already holds the key.
#
# This matters more for the router than for any game: its host key is the one
# players actually pin, because play.ssharcade.dev is the only endpoint they
# ever connect to. Losing it gives every returning player a host-key-changed
# warning, which is indistinguishable from a MITM.
seed_host_key() {
	[ -n "$HOST_KEY_SOURCE" ] || return 0
	[ -f "$HOST_KEY_PATH" ] && return 0
	if [ ! -s "$HOST_KEY_SOURCE" ]; then
		# Not fatal: a genuine first-ever boot has no key anywhere and the app
		# generating one is correct. Loud, because on any later boot it means
		# the host mount is missing and players are about to be warned.
		echo "entrypoint: WARNING — $HOST_KEY_SOURCE is empty or absent; the app will generate a NEW host key and every returning player will see a host-key-changed warning" >&2
		return 0
	fi
	echo "entrypoint: seeding host key from $HOST_KEY_SOURCE"
	# Write to a temp path and promote only once the copy is complete. A
	# partially written file at $HOST_KEY_PATH would still satisfy the app's
	# "does a key already exist" check and skip generation, which crashes the
	# server with "ssh: no key found".
	if ! cp "$HOST_KEY_SOURCE" "$HOST_KEY_PATH.tmp"; then
		rm -f "$HOST_KEY_PATH.tmp"
		echo "entrypoint: FATAL — $HOST_KEY_SOURCE is mounted but unreadable; refusing to start rather than serve under a different host key" >&2
		exit 1
	fi
	chmod 600 "$HOST_KEY_PATH.tmp"
	mv "$HOST_KEY_PATH.tmp" "$HOST_KEY_PATH"
}

seed_host_key

# Dev mode: no replica configured, skip straight to the app with a loud log
# line. Local dev and CI must never require AWS credentials.
if [ -z "${LITESTREAM_REPLICA_URL:-}" ]; then
	echo "entrypoint: LITESTREAM_REPLICA_URL not set — running WITHOUT replication (dev mode)"
	exec /app/ssh-arcadelobby
fi

echo "entrypoint: restoring $DB_PATH from $LITESTREAM_REPLICA_URL if needed"
litestream restore -if-db-not-exists -if-replica-exists "$DB_PATH"

# There is deliberately no upload-the-keys-back-to-S3 step any more. It existed
# because the keys lived only in the container's volume, so the container had to
# seed a bucket to survive volume loss — and doing that needed S3 write
# credentials in the container. Both keys are host state now, backed up with the
# host's own instance role (see deploy/README.md), so this container
# authenticates to nothing except through litestream, which uses that same role.

exec litestream replicate -exec "/app/ssh-arcadelobby"
