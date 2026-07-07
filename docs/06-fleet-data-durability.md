# Arcade 06 — Fleet Data Durability (Litestream + S3)

**Canonical fleet pattern.** Every game implements this; game repos cite
this file by path. First implementation: `../../ssh-farm/docs/framework/02-store-durability-and-v1-import.md`.
Fleet decision (2026-07): Litestream replication was chosen over managed
Postgres/DynamoDB — it keeps the per-game SQLite architecture unchanged at
near-zero cost while making instance loss a non-event.

## The requirement

**Player progress must survive the loss of the EC2 instance, its EBS
volumes, or the whole AZ.** The instance is disposable; the S3 bucket is
what actually holds the fleet's data. Recovery must be automatic — a fresh
host + `docker compose up -d` restores every game with no manual steps.

## How it works

Each game keeps its single SQLite file (WAL mode, single connection —
already the fleet standard). [Litestream](https://litestream.io) wraps the
game process in its container and continuously streams WAL frames to S3:

```
┌─ game container ────────────────────────────┐
│ entrypoint:                                  │
│  1. restore host key from s3://…/keys/<game>/ (if volume empty)
│  2. litestream restore -if-db-not-exists     │      ~1s lag
│  3. litestream replicate -exec /app/<game> ──┼──► s3://<bucket>/<game>/db
└──────────────────────────────────────────────┘      (versioned bucket)
```

- **RPO** (worst-case loss): seconds — litestream syncs every ~1 s; combined
  with 30 s autosaves, a catastrophic instance loss costs at most one
  autosave interval of play.
- **RTO**: minutes — new instance, compose up, entrypoints self-restore.
- **The game binary needs zero code changes.** Durability is entirely
  container plumbing; the store specs in every repo stand as written.

## Bucket layout & AWS setup (one-time)

```
s3://<org>-ssharcade-data/
  farm/db/…            litestream generations per game
  moonminer/db/…
  router/db/…          lobby SQLite (alpha-warning acks, account touch)
  keys/farm/           host keys (one-time upload, restored on boot)
  keys/moonminer/
  keys/router/         router host key + proxy key backup
  archive/…            retired-game DB archives (e.g. idlefarmer at cutover)
```

- **Versioning ON**, SSE-S3 encryption, block public access.
- Lifecycle: litestream manages generation retention (config `retention:
  72h`); bucket lifecycle expires noncurrent versions after 30 d as
  belt-and-braces.
- **IAM**: the EC2 instance profile gets one policy — `Get/Put/List/Delete`
  scoped to this bucket only. **No AWS keys in compose files or images**;
  the SDK inside litestream picks up the instance role automatically.
- Region = the instance's region (no cross-region latency; S3's 11-nines
  is the durability story).

## Container pattern (each game's Dockerfile)

- Final stage: `alpine:3` (not distroless — the entrypoint needs `sh`, and
  litestream ships as a static binary to copy in). Keep the rest of the
  hardening: non-root uid, `read_only: true`, writable volume + `/tmp`
  only. This supersedes the "distroless" line in earlier fleet docs; the
  tradeoff (a shell exists in the image) is accepted for a dependable
  restore path.
- `entrypoint.sh` (~15 lines, per game):
  1. if `$<GAME>_HOST_KEY_PATH` missing and `keys/<game>/` has one →
     download; if present locally and absent in S3 → upload (first boot).
  2. `litestream restore -if-db-not-exists -if-replica-exists $DB_PATH`
  3. `exec litestream replicate -exec "/app/<game>" $DB_PATH $LITESTREAM_REPLICA_URL`
- **Dev mode**: when `LITESTREAM_REPLICA_URL` is unset, skip straight to
  `exec /app/<game>` with a loud log line — local dev and CI must never
  require AWS credentials. Local durability testing uses MinIO
  (`ssh-farm/docs/tests/02` owns the drill scripts).
- Pin the litestream version in the Dockerfile; upgrade deliberately.

## Rules

1. **One SQLite file per game holds all persistent state.** Anything else
   a game wants to keep must go in the DB or explicitly into `keys/`-style
   one-time objects — never loose files on the volume.
2. **The volume is a cache now.** Fast local writes, but deleting it must
   always be recoverable from the bucket (that's the kill drill).
3. **Restores are drilled, not assumed.** Quarterly: restore each game's
   live generation to a scratch container, `PRAGMA integrity_check` +
   decode-every-save, record observed RPO/RTO. The drill script lives in
   ssh-farm and is reused fleet-wide.
4. **Watch the replica age.** MVP: litestream logs land in `docker logs`
   (compose logging), and the quarterly drill catches rot. Post-MVP TODO:
   a scheduled check that alerts when `<game>/db` generations stop
   advancing while the game has active sessions.
5. **Archives on retirement.** A decommissioned game's final DB goes to
   `archive/<game>/<date>/` before its volume is removed (see the
   idlefarmer cutover, `../../ssh-farm/docs/framework/03`).

## Per-repo responsibilities

| Repo | Owns |
| --- | --- |
| this repo (`deploy/`) | bucket name/env wiring in compose, instance-profile documentation, `keys/router/` for host+proxy keys, `router/db/` Litestream replica for lobby SQLite |
| `ssh-farm` | first game implementation: Dockerfile/entrypoint pattern, MinIO drill scripts (`framework/02`, `tests/02`) |
| `ssh-moonminer` | adopts the same Dockerfile/entrypoint pattern (its `framework/04` references this doc) |

## Cost

S3 storage for WAL generations of small SQLite files + request pricing:
realistically **under $1/mo for the whole fleet**. The entire durability
layer costs less than a coffee per year until the games are very popular —
at which point revisit RDS with joy.

## Acceptance criteria (fleet-level)

- [ ] Kill drill passes for every game: `docker kill` + volume delete +
  `up -d` → all saves back, measured loss ≤ autosave interval.
- [ ] Fresh-host drill: new instance from scratch, compose up → every
  game serves its restored data and host keys (no SSH host-key warnings
  through the router).
- [ ] No AWS credentials appear in any repo, image, or compose file
  (instance role only); dev runs everywhere with no credentials.
- [ ] Quarterly drill runbook exists and has been executed once for real.
