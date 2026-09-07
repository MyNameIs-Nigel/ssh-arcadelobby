# Replication monitoring

Detects **failure mode 1** from the going-public plan: a game serving players
normally while nothing is being replicated to S3. That is the fleet's most
dangerous failure because it is silent — you find out when you need a restore.

| File | Installed to |
| --- | --- |
| `replication-check.sh` | `/usr/local/bin/replication-check.sh` |
| `replication-alert.sh` | `/usr/local/bin/replication-alert.sh` |
| `replication-check.service` | `/etc/systemd/system/` |
| `replication-check.timer` | `/etc/systemd/system/` (every 5 min) |
| `replication-check-alert.service` | `/etc/systemd/system/` (fired by `OnFailure=`) |
| `replication-check.defaults` | `/etc/default/replication-check` (only if absent) |

## Install

```bash
scp -r -P <port> deploy/monitoring ec2-user@ssharcade.dev:/tmp/monitoring
ssh -p <port> ec2-user@ssharcade.dev 'sudo /tmp/monitoring/install.sh'
```

Idempotent. Re-run it after editing the check; it syntax-checks and test-runs
before enabling the timer, because a timer running a broken monitor is worse
than no monitor — it looks like coverage.

## What it actually checks, and why it is not the plan's version

§4.4 of the plan reads the newest object under `<game>/db/`. In Litestream
v0.5's LTX layout that prefix also holds **compaction tiers** (`db/0002`,
`db/0003`, `db/0009`) which are written on a *timer*, whether or not anything
replicated. Raw WAL lands only in `db/0000/`. Observed 2026-09-06:

```
chess/db/0000/…  last real WAL write  2026-09-05T19:37:58Z
chess/db/0009/…  compaction object    2026-09-06T00:00:01Z   ← 4.4h later
```

Run at 00:05Z, the plan's check sees a 300-second-old object and reports
`ok: chess` — while chess had not replicated a real write in over four hours.
A false negative on the highest-ranked data-loss risk in the plan.

Watching `db/0000/` alone just swaps that for a false *positive*: a quiet game
legitimately writes nothing for hours, and a check that cries wolf gets muted,
which lands you back at the false negative by another route.

So this version alerts on the **conjunction**: the database changed *and* no raw
WAL object followed it. That is failure mode 1 and nothing else.

### The restart exception

Starting a container touches the database mtime — sqlite opening in WAL mode is
enough — **without** necessarily producing a WAL object, because a game nobody
is playing has nothing to replicate. Observed 2026-09-07: a fleet restart moved
every database's mtime; farm and router got fresh WAL within seconds, while
moonminer and chess (quiet for ~21h and ~30h) did not.

On mtime alone that is indistinguishable from failure mode 1. Without the
exception the check would page falsely for every quiet game for `GRACE` seconds
after any restart — which, now that deploys go through SSM and restart
containers on every release, means after **every deploy**.

The check therefore excuses a container that started less than `GRACE` ago and
says so explicitly rather than printing a bare `ok`, because that same window is
where a genuine failure would be invisible:

```
ok:     chess      restarted 240s ago, no writes since (wal 109314s) — recheck after 900s
```

## Alerting is not finished

Out of the box a failure is written to the journal and **pages nobody**. Making
it a real monitor needs two things that do not exist yet:

1. An SNS topic with a confirmed subscription.
2. `sns:Publish` on that topic added to the EC2 instance role
   (`ssharcade-litestream-role`).

Both commands are in `/etc/default/replication-check`. Until then:

```bash
systemctl list-units --failed | grep replication-check
journalctl -u replication-check.service -n 50 --no-pager
```

If the ARN is set but the grant is missing, the publish fails and the alert
degrades back to log-only — `replication-alert.sh` says exactly that in the
journal rather than failing silently.

## Operating it

```bash
systemctl list-timers replication-check.timer     # when it next runs
systemctl start replication-check.service         # run it now
journalctl -u replication-check.service -f        # watch it
GRACE=3000 /usr/local/bin/replication-check.sh    # widen the window by hand
```

Exit codes: `0` everything healthy or explainably quiet, `1` at least one game
is writing but not replicating, or a container reports running without
replication at all.
