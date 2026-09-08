#!/usr/bin/env bash
# replication-alert.sh — fired by systemd as OnFailure= for
# replication-check.service. Publishes to SNS when a topic is configured, and
# always leaves a record in the journal.
#
# This script never exits non-zero. A failing alerter would give systemd a
# failed unit to report and nothing to report it with, so every failure path
# here degrades to a loud journal entry instead.
set -uo pipefail

[ -r /etc/default/replication-check ] && . /etc/default/replication-check

REGION="${AWS_REGION:-us-east-1}"
HOST="$(hostname -s 2>/dev/null || echo ssharcade)"
WHEN="$(date -u +%Y-%m-%dT%H:%M:%SZ)"

# The check writes its verdict to stderr, which systemd captures. Pull the last
# run's output so the alert says WHICH game is stale, not merely that something is.
DETAIL="$(journalctl -u replication-check.service --since '-10 min' --no-pager -o cat 2>/dev/null | tail -20)"
[ -z "$DETAIL" ] && DETAIL="(no journal output captured — check 'journalctl -u replication-check.service')"

logger -t replication-check -p daemon.err "REPLICATION CHECK FAILED on ${HOST} at ${WHEN}"
while IFS= read -r line; do
	[ -n "$line" ] && logger -t replication-check -p daemon.err "  ${line}"
done <<<"$DETAIL"

if [ -z "${REPLICATION_ALERT_TOPIC_ARN:-}" ]; then
	logger -t replication-check -p daemon.err \
		"no REPLICATION_ALERT_TOPIC_ARN set — this alert is LOG-ONLY and will page nobody"
	exit 0
fi

MESSAGE="ssharcade replication check FAILED
host:  ${HOST}
when:  ${WHEN}

${DETAIL}

This means a game's database changed but no raw WAL object followed it in
s3://ssharcade-snoigel-data/<game>/db/0000/ — i.e. the game is serving players
while nothing is being backed up (failure mode 1). Check the container's
litestream logs first:

  docker compose -f /srv/ssharcade/docker-compose.yml logs --tail 100 <game>"

if aws sns publish \
	--region "$REGION" \
	--topic-arn "$REPLICATION_ALERT_TOPIC_ARN" \
	--subject "ssharcade: replication check failed on ${HOST}" \
	--message "$MESSAGE" >/dev/null 2>&1
then
	logger -t replication-check "alert published to ${REPLICATION_ALERT_TOPIC_ARN}"
else
	# Almost always one of: the instance role lacks sns:Publish, the topic ARN is
	# wrong, or the topic is in another region. Name them, because this fires at
	# the worst possible moment to go debugging blind.
	logger -t replication-check -p daemon.err \
		"SNS publish FAILED for ${REPLICATION_ALERT_TOPIC_ARN} — check sns:Publish on the instance role, the ARN, and the region"
fi

exit 0
