#!/usr/bin/env bash
# install.sh — install the replication check, its alerting, and a 5-minute timer
# on play.ssharcade.dev.
#
#   scp -r -P <port> deploy/monitoring ec2-user@ssharcade.dev:/tmp/monitoring
#   ssh -p <port> ec2-user@ssharcade.dev 'sudo /tmp/monitoring/install.sh'
#
# Idempotent: safe to re-run after editing the check. Local edits to
# /etc/default/replication-check are never overwritten.
set -euo pipefail

SRC="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"

if [ "$(id -u)" -ne 0 ]; then
	echo "install.sh: must run as root (use sudo)" >&2
	exit 1
fi

for f in replication-check.sh replication-alert.sh replication-check.service \
         replication-check.timer replication-check-alert.service replication-check.defaults; do
	[ -r "$SRC/$f" ] || { echo "install.sh: missing $SRC/$f" >&2; exit 1; }
done

# Syntax-check before installing. Enabling a timer that runs a broken monitor is
# strictly worse than having no monitor, because it looks like coverage.
bash -n "$SRC/replication-check.sh"
bash -n "$SRC/replication-alert.sh"

echo "==> installing scripts"
install -m 0755 -o root -g root "$SRC/replication-check.sh" /usr/local/bin/replication-check.sh
install -m 0755 -o root -g root "$SRC/replication-alert.sh" /usr/local/bin/replication-alert.sh

echo "==> installing units"
install -m 0644 -o root -g root "$SRC/replication-check.service"       /etc/systemd/system/
install -m 0644 -o root -g root "$SRC/replication-check.timer"         /etc/systemd/system/
install -m 0644 -o root -g root "$SRC/replication-check-alert.service" /etc/systemd/system/

if [ -e /etc/default/replication-check ]; then
	echo "==> /etc/default/replication-check exists, leaving it alone"
else
	echo "==> writing /etc/default/replication-check"
	install -m 0644 -o root -g root "$SRC/replication-check.defaults" /etc/default/replication-check
fi

# A copy on the host so the Documentation= links in the units resolve.
install -d -m 0755 -o root -g root /srv/ssharcade/monitoring
install -m 0644 -o root -g root "$SRC/README.md" /srv/ssharcade/monitoring/README.md 2>/dev/null || true

echo "==> reloading systemd"
systemctl daemon-reload

# Prove the check works BEFORE enabling the timer. A non-zero exit here is a
# real finding about the fleet, not an installation problem, so report it
# clearly and still enable the timer — that is exactly when you want it running.
echo "==> test run"
set +e
/usr/local/bin/replication-check.sh
CHECK_RC=$?
set -e
echo "    check exited $CHECK_RC"

echo "==> enabling timer"
systemctl enable --now replication-check.timer >/dev/null

echo
echo "==> status"
systemctl list-timers replication-check.timer --no-pager || true
echo
if [ "$CHECK_RC" -ne 0 ]; then
	cat <<'EOF'
WARNING: the check reported a problem on its test run (exit non-zero).
The timer is enabled and will keep reporting it. Investigate before treating
this install as done:

  journalctl -u replication-check.service -n 50 --no-pager
EOF
fi

if ! grep -q '^REPLICATION_ALERT_TOPIC_ARN=' /etc/default/replication-check 2>/dev/null; then
	cat <<'EOF'
NOTE: no REPLICATION_ALERT_TOPIC_ARN is set, so a failure is recorded in the
journal and pages nobody. That is a check you have to remember to look at, not
a monitor. To finish the job you need an SNS topic with a confirmed
subscription AND sns:Publish on the EC2 instance role — see
/etc/default/replication-check for both commands.

Until then, this shows anything that has failed:

  systemctl list-units --failed | grep replication-check
EOF
fi

echo
echo "installed."
