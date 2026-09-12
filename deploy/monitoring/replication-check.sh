#!/usr/bin/env bash
# replication-check.sh — detects failure mode 1 (silent replication stop), §4.4.
#
# Install to /usr/local/bin/replication-check.sh on ssharcade.dev, wired to a
# systemd timer every 5 minutes with SNS/email on non-zero exit.
#
# WHY THIS DIFFERS FROM THE PLAN'S §4.4 VERSION
# ---------------------------------------------
# The plan reads the newest object under "$g/db/". In Litestream v0.5's LTX layout
# that prefix also holds compaction tiers (db/0002, db/0003, db/0009) which are
# written on a TIMER whether or not any real data replicated. Raw WAL lands only in
# db/0000/. Observed on 2026-09-06:
#
#     chess/db/0000/...  last real WAL write   2026-09-05T19:37:58Z
#     chess/db/0009/...  compaction object     2026-09-06T00:00:01Z   (4.4h later)
#
# Run at 00:05Z the plan's check sees a 300s-old object and reports "ok: chess" while
# chess had not replicated a real write in over four hours. A false negative on the
# highest-ranked data-loss risk in the plan.
#
# Watching db/0000/ alone instead swaps that for a false POSITIVE: a genuinely quiet
# game (chess routinely has no writes for hours) would page constantly, and a check
# that cries wolf gets muted, which is how you end up back at a false negative.
#
# So this version compares two facts: did the database change, and did a WAL object
# follow it? Alert only when the answer is "yes" then "no" — the game is writing but
# not replicating, which is exactly failure mode 1 and nothing else.
set -euo pipefail

# Bound our own runtime. This runs on a 5-minute timer, and the AWS CLI's default
# retry policy will happily spin for minutes against an unreachable bucket — an
# alerting script that hangs fails just as silently as one that lies, so cap the
# retries and let a genuine outage surface as a fast non-zero exit.
export AWS_MAX_ATTEMPTS="${AWS_MAX_ATTEMPTS:-2}"
export AWS_RETRY_MODE="${AWS_RETRY_MODE:-standard}"
AWSCLI=(aws --cli-connect-timeout 5 --cli-read-timeout 10)

BUCKET="${BUCKET:-ssharcade-snoigel-data}"
REGION="${AWS_REGION:-us-east-1}"
GRACE="${GRACE:-900}"        # seconds a write may lag replication before alerting
GAMES="${GAMES:-farm moonminer chess router}"

now=$(date -u +%s)
rc=0

for g in $GAMES; do
	case "$g" in
		router) vol="ssharcade_arcade-router-data"; db="arcade.db" ;;
		*)      vol="ssharcade_${g}-data";          db="${g}.db"   ;;
	esac

	# Newest RAW WAL object. Compaction tiers are deliberately excluded.
	wal_ts=$("${AWSCLI[@]}" s3api list-objects-v2 --bucket "$BUCKET" --region "$REGION" \
		--prefix "${g}/db/0000/" \
		--query 'sort_by(Contents,&LastModified)[-1].LastModified' \
		--output text 2>/dev/null || echo "None")

	if [ "$wal_ts" = "None" ] || [ -z "$wal_ts" ]; then
		wal_age=999999
	else
		wal_age=$(( now - $(date -u -d "${wal_ts%%+*}" +%s 2>/dev/null \
			|| date -u -j -f "%Y-%m-%dT%H:%M:%S" "${wal_ts%%+*}" +%s) ))
	fi

	# When was the database itself last written? mtime is game-agnostic — no schema
	# knowledge, so this keeps working if a game adds or renames tables.
	db_age=$(docker run --rm -v "${vol}":/d:ro alpine:3 \
		sh -c "expr \$(date -u +%s) - \$(stat -c %Y /d/${db} 2>/dev/null || echo 0)" 2>/dev/null || echo 999999)

	# How long has the container been up? Starting a service touches the database
	# mtime — sqlite opening in WAL mode is enough — WITHOUT necessarily producing
	# a WAL object, because a game nobody is playing has nothing to replicate.
	# Observed on 2026-09-07: a restart moved every db mtime, and farm and router
	# got fresh WAL within seconds while moonminer and chess (quiet for ~21h and
	# ~30h) did not. Judged on mtime alone that reads exactly like failure mode 1,
	# so without this the check pages falsely for every quiet game for GRACE
	# seconds after any restart — which now means after every deploy.
	started=$(docker inspect "ssharcade-${g}-1" --format '{{.State.StartedAt}}' 2>/dev/null || echo "")
	if [ -n "$started" ]; then
		up_age=$(( now - $(date -u -d "$started" +%s 2>/dev/null \
			|| date -u -j -f "%Y-%m-%dT%H:%M:%S" "${started%%.*}" +%s 2>/dev/null || echo 0) ))
	else
		up_age=999999
	fi

	if [ "$up_age" -lt "$GRACE" ] && [ "$wal_age" -gt "$GRACE" ]; then
		# Recently restarted with no WAL since. Explainable, not evidence of a
		# fault — but say so rather than printing a bare "ok", because this is
		# also the window in which a genuine failure would be invisible.
		printf 'ok:     %-10s restarted %ss ago, no writes since (wal %ss) — recheck after %ss\n' \
			"$g" "$up_age" "$wal_age" "$GRACE"
	elif [ "$db_age" -gt "$GRACE" ]; then
		# Nothing written recently. Replication having nothing to do is correct,
		# not a fault. This is the quiet-game case that must never page.
		printf 'ok:     %-10s idle (db untouched %ss, wal %ss)\n' "$g" "$db_age" "$wal_age"
	elif [ "$wal_age" -le "$GRACE" ]; then
		printf 'ok:     %-10s replicating (db %ss, wal %ss)\n' "$g" "$db_age" "$wal_age"
	else
		# The database changed but no WAL object followed it. This is failure mode 1.
		printf 'STALE:  %-10s WROTE %ss ago but last replicated %ss ago\n' "$g" "$db_age" "$wal_age" >&2
		rc=1
	fi
done

# The entrypoint's dev-mode escape hatch serving unreplicated in production is the
# same failure wearing a different hat, and it says so loudly in the logs.
if docker compose -f /srv/ssharcade/docker-compose.yml logs --since 10m 2>/dev/null \
	| grep -qi 'no replica url\|dev mode\|replication disabled'; then
	echo "STALE:  a container reports running WITHOUT replication" >&2
	rc=1
fi

exit $rc
