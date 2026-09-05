# Arcade 04 — Deployment & CI/CD

**Phase:** 3 · **Depends on:** 01–03 buildable · **This repo owns the
arcade-wide compose file and the deploy conventions every game repo
follows.**

## Goal

One EC2 host, one Elastic IP, one `docker compose` stack: the router
public on 22, every game private, everything redeployable **per service**
from GitHub: push → tested; merge to main → image built → only that
service restarted on the host. Adding a game touches the compose file and
`games.toml`, never the router binary.

## Topology

```
DNS:  ssharcade.dev       A     →  <elastic IP>    (ssh + web redirect)
      play.ssharcade.dev  A     →  <elastic IP>    (ssh, the documented name)
      www.ssharcade.dev   CNAME →  Vercel          (the website; not this host)
EC2 host (t3.small+, Docker + compose plugin):
  /srv/ssharcade/
    docker-compose.yml        (this repo, deployed copy)
    games.toml                (mounted into router, hot-reloaded)
    Caddyfile                 (mounted into web, apex → www redirect)
    secrets/proxy_key         (ed25519 private key, chmod 600)
  volumes: arcade-router-data, moonminer-data, idlefarmer-data, caddy-data
  networks: ssharcade (bridge, internal service DNS: moonminer, idlefarmer)
            edge      (web only — no route to the games)
```

Optional vanity DNS (`moonminer.ssharcade.dev` → same IP) is cosmetic only:
SSH has no SNI, everything lands on the router regardless.

**The apex carries both protocols.** DNS resolves names to addresses, not
to ports, so an A record for `ssharcade.dev` necessarily serves port 22 and
port 443 under the same name — the apex cannot point at Vercel for the web
and here for SSH. Pointing it here is what makes `ssh ssharcade.dev` work,
and the `web` service (Caddy) is the consequence: it answers browsers on
80/443 with a 301 to `www.ssharcade.dev`, where the site itself still lives
on Vercel. The site is deliberately *not* served from this box — that would
trade Vercel's CDN and preview deploys for EC2 egress and a build artifact
CI would have to ship. `deploy/README.md` has the records, the security
group rules, and the DNS-before-merge ordering ACME requires.

## Compose (shape — maintained in this repo as `deploy/docker-compose.yml`)

```yaml
services:
  router:
    image: ghcr.io/mynameis-nigel/ssh-arcadelobby:latest
    ports: ["22:2222"]                    # host 22 → container 2222 (non-root bind)
    environment:
      ARCADE_LISTEN_PORT: "2222"
      ARCADE_HOST_KEY_PATH: /var/lib/arcade/ssh_host_key
      ARCADE_PROXY_KEY_PATH: /run/secrets/proxy_key
      ARCADE_GAMES_PATH: /etc/arcade/games.toml
    volumes:
      - arcade-router-data:/var/lib/arcade
      - ./games.toml:/etc/arcade/games.toml:ro
    secrets: [proxy_key]
    networks: [ssharcade]
    read_only: true
    tmpfs: [/tmp]
    restart: unless-stopped
    stop_grace_period: 15s               # router holds no state; die fast

  moonminer:
    image: ghcr.io/mynameis-nigel/ssh-moonminer:latest
    # NO ports: — private network only; the router is the only way in
    environment:
      MOONMINER_LISTEN_PORT: "2222"
      MOONMINER_DB_PATH: /var/lib/moonminer/moonminer.db
      MOONMINER_HOST_KEY_PATH: /var/lib/moonminer/ssh_host_key
      MOONMINER_PROXY_KEYS_PATH: /etc/moonminer/proxy_keys
      MOONMINER_RATE_LIMIT_PER_SECOND: "50"   # router does public throttling
    volumes:
      - moonminer-data:/var/lib/moonminer
      - ./proxy_keys:/etc/moonminer/proxy_keys:ro   # PUBLIC half(s) of proxy key
    networks: [ssharcade]
    read_only: true
    tmpfs: [/tmp]
    restart: unless-stopped
    stop_grace_period: 45s               # must exceed the 30s save-flush context

  # idlefarmer: same shape as moonminer

networks:
  ssharcade: {}
volumes:
  arcade-router-data: {}
  moonminer-data: {}

secrets:
  proxy_key:
    file: ./secrets/proxy_key
```

Key properties to preserve whatever else changes: games have **no
published ports**; proxy **private** key only in the router; proxy
**public** key list mounted into games; per-service `stop_grace_period`
respecting each service's flush budget; non-root read-only containers all
around; and the `web` redirector isolated on its own `edge` network, since
it is the one container with an unauthenticated public listener and has no
business reaching the router, the games, or the proxy key.

**Durability (see doc 06 — canonical):** every game service additionally
sets `LITESTREAM_REPLICA_URL: s3://<bucket>/<game>/db` (+ the `keys/`
prefix env) and its image wraps the game in Litestream (alpine base +
entrypoint, superseding the earlier distroless note). AWS access comes
from the **EC2 instance profile** — no credentials in this file, ever. The
one-time bucket/IAM provisioning checklist lives in doc 06; treat it as a
prerequisite of first deploy.

### Secrets provisioning (one-time per host)

```bash
ssh-keygen -t ed25519 -N "" -f secrets/proxy_key -C "ssharcade-router"
cp secrets/proxy_key.pub proxy_keys        # the games' trust file
```

Rotation: append new public key to `proxy_keys`, restart games, swap the
router's secret, restart router, remove old public key.

## CI/CD

### Router (this repo) — `.github/workflows/`

- **`ci.yml`** — on `pull_request` and pushes to non-main branches:
  `go vet ./...`, `go build ./...`, `go test -race ./...`.
- **`release.yml`** — on push to `main`:
  1. run the same tests (never deploy untested merges),
  2. `docker/build-push-action` → `ghcr.io/mynameis-nigel/ssh-arcadelobby`
     tagged `latest` + `sha-<short>`.

  There is no third step. **Merging to `main` produces an image; it does not
  ship it.** Putting that image on the host is manual — see
  `deploy/README.md`, "Deploying by hand".

There used to be a third job, `runs-on: [self-hosted, production]`, running on
a runner installed on the `play.ssharcade.dev` host. That shape was chosen
because the host's real sshd is locked to one dev IP and GitHub-hosted runners
come from an ever-changing range that would never match it, so an
`appleboy/ssh-action`-style job would just time out. It is not survivable on a
public repo: for `pull_request` events GitHub reads the workflow file from the
PR head, so a fork PR can retarget `runs-on:` at the `self-hosted` label and
execute on the box, in the docker group, next to every player save. Fork-PR
approval does not close the gap — it cannot be configured while the repo is
private, and one merged PR grants standing approval afterwards. All four
runners were deregistered and uninstalled on 2026-09-04.

**Where this is going.** The replacement is GitHub OIDC + AWS SSM Run Command:
the deploy job assumes a short-lived role by OIDC and sends the
`docker compose pull && up -d` to the instance through SSM. Outbound only —
no inbound port, no IP allowlist, no standing AWS keys, and no GitHub-
controlled process on the host. Until that lands, deploys are manual.

### Per-game workflow (template — each game repo copies this)

Identical `ci.yml`; `release.yml` differs only in image name and service.
No game repo has, or should have, a runner registered on the host.

```yaml
# release.yml (game repo)  — test → image, and stop there
on: { push: { branches: [main] } }
jobs:
  test:    # vet + build + go test -race ./...
  publish: # needs: test → ghcr.io/mynameis-nigel/<repo>:latest + sha
```

Restarting the one service is manual, per `deploy/README.md`:

```bash
cd /srv/ssharcade
docker compose pull moonminer
docker compose up -d moonminer
```

Because of each game's graceful shutdown (SIGTERM → flush saves →
auto-bail active runs), a deploy mid-session costs players a reconnect,
never progress. The router keeps serving the menu throughout; the game
shows `○ OFFLINE` for the seconds it's restarting.

Registry/pull auth: the host is logged in to GHCR with a read-only PAT.
Settle whether the packages are actually public **before** running
`docker logout` anywhere — if any image is still private, logging out
breaks every subsequent `docker compose pull`.

### Compose changes (adding a game)

The compose file + `games.toml` live in this repo under `deploy/`. The
`release.yml` deploy job used to copy them to `/srv/ssharcade/` on every merge
to main; that sync is now a manual `cp` from a checkout on the host, run before
`docker compose up -d router` — `games.toml` hot-reloads, compose changes take
effect on that same `up -d`. It is a plain `cp` and not a network rsync or a
`mv`, for the inode reason below.
`deploy/banner/banner.toml` is deliberately **not** synced this way: it's an
operator notice meant to be edited directly on the host without a code
deploy (see doc 03's "Operator banner"), so CI never touches
`/srv/ssharcade/banner/banner.toml` once it exists. The directory
(`./banner:/etc/arcade/banner:ro`) is bind-mounted rather than the file
itself — a single-file mount pins to the inode present at container start,
so any edit that replaces the file instead of truncating it in place (an
editor's atomic save, `scp`, `mv`) silently orphans the mount until the
container is recreated.

## Cost sanity check (the reason this architecture exists)

- Before: N games × (t3.small + public IPv4) ≈ N × ~$19/mo.
- After: 1 × t3.small (~$15) + 1 × IPv4 (~$3.65) + EBS ≈ **~$20/mo flat**
  regardless of game count, until CPU says otherwise (idle TUI games are
  near-free; the sim ticks are microseconds).

## Acceptance criteria

- [ ] Fresh host bootstrap documented in `deploy/README.md` (Docker
  install, dir layout, secrets, first `up -d`) and tested once for real.
- [ ] `docker compose up -d` from scratch: `ssh play.<host>` → menu → both
  games playable through the bridge.
- [ ] `docker compose stop moonminer`: menu shows `○ OFFLINE` within one
  probe cycle + damping; players in idlefarmer unaffected; `start` flips it
  back.
- [ ] A manual `docker compose pull <game> && up -d <game>` restarts only
  that service (watch `docker compose ps` timestamps); a player in another
  game stays connected throughout.
- [ ] Host reboot: `restart: unless-stopped` brings the stack back; host
  keys and saves intact (volumes).
- [ ] No game port reachable from the public internet (scan the host).
      The scan's expected answer is now 22, 80, 443 and the dev sshd — 80
      and 443 are Caddy and are *supposed* to be open.
- [ ] `ssh ssharcade.dev` and `ssh play.ssharcade.dev` both reach the menu.
- [ ] `curl -sI https://ssharcade.dev` → 301 to `https://www.ssharcade.dev`,
      with a certificate that validates.
- [ ] `docker compose exec web ping router` fails to resolve — the edge
      network really is isolated from `ssharcade`.
- [ ] **Instance-loss drill** (doc 06): terminate the instance, bootstrap
  a fresh one, `compose up -d` → every game restores from S3 with correct
  host keys and ≤ seconds of lost play.

## Out of scope

- The website's *content and hosting* — that stays on Vercel at
  `www.ssharcade.dev`. This repo owns only the apex redirector that lets
  the SSH endpoint and the site share one name (see "Topology").
- Multi-host scaling, monitoring/alerting stack — post-MVP (a healthcheck
  cron + uptime pinger is fine to start).
