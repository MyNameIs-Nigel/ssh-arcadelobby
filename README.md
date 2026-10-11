# ssh-arcadelobby

The front door of **ssharcade** — a fleet of terminal games played over SSH.
One address, every game:

```bash
ssh ssharcade.dev
```

The same apex hostname serves SSH on port 22 and redirects web traffic to
`www.ssharcade.dev`. See
[deploy/README.md](deploy/README.md#dns-the-apex-and-the-web-redirector)
for why the apex has to serve both protocols.

Players land in an arcade menu (Bubble Tea TUI, keyboard + mouse), pick a
game, and are bridged transparently into that game's own SSH server running
on a private network. Games deploy independently; when one is down the
lobby stays up and marks it `○ OFFLINE`.

```
                     ┌───────────── one EC2 host · one Elastic IP ────────────┐
 players ── ssh:22 ─►│ ssh-arcadelobby ──┬── private net ──► farm:2222        │
                     │  (menu + bridge)  ├──────────────────► moonminer:2222   │
                     │                   └──────────────────► future games…    │
                     └────────────────────────────────────────────────────────┘
```

## Status

**Live** at `ssharcade.dev`, router `1.2.1` (alpha channel), fronting
Idle Farmer and Moon Miner. Every push to `main` rebuilds and publishes the
image; putting it on the host is a separate manual step
([`deploy/README.md`](deploy/README.md) § "Deploying by hand"). `main` is
still the production branch — treat it as such — it just no longer ships
itself.

The live player count is public JSON at
`https://api.ssharcade.dev/v1/players`, written by the router and served by
the fleet's Caddy — see
[docs/07-live-player-count.md](docs/07-live-player-count.md) for the API and
how to poll it.

`docs/` remains the build plan: [docs/README.md](docs/README.md) is the
index; each numbered doc is a self-contained task an agent can pick up
independently. The identity-forwarding protocol in
[docs/02-bridge-and-identity-protocol.md](docs/02-bridge-and-identity-protocol.md)
is the canonical contract every game repo implements against.

## Stack

- **Go 1.26+**, pure Go (`CGO_ENABLED=0`)
- **charm.land/wish/v2** — public SSH server (menu side)
- **golang.org/x/crypto/ssh** — SSH client (bridge side)
- **charm.land/bubbletea/v2** + **lipgloss/v2** — lobby TUI
- **Docker** — hardened alpine image (non-root uid 65532, read-only root FS;
  not distroless — the entrypoint needs a shell plus litestream/`mc`); this
  repo also owns the arcade-wide `docker-compose.yml`

## Fleet repos

| Repo | Role |
| --- | --- |
| `ssh-arcadelobby` | this — router, lobby TUI, arcade compose + deploy docs, fleet durability pattern |
| `ssh-farm` | game: Idle Farmer v2 — mouse, leaderboards, durable data. **Live** |
| `ssh-moonminer` | game: asteroid-mining extraction loop. **Live** |
| `ssh-chess` | game: GAMBIT — chess over SSH, server-authoritative clocks, Stockfish bots. Compose service is in `deploy/`; goes live on its first `main` release |

Idle Farmer v1 (`ssh-idlefarmer`) was the fleet's reference implementation
and was retired at the v2 cutover; its repo is gone and `ssh-farm` carries
the v1 save importer. Docs across the fleet still cite it as the source to
port from — those references are historical.

## Commands

```bash
go build -o bin/ssh-arcadelobby ./cmd/ssh-arcadelobby   # build
go test ./...                                            # test
go vet ./...                                             # vet
```


## Licence

MIT — see [`LICENSE`](LICENSE).

[`NOTICE`](NOTICE) covers third-party software redistributed inside the
published container image (currently Litestream, Apache-2.0). Update it if the
image ever gains another bundled binary — Apache-2.0 requires that attribution
to travel with the artifact, not merely with the source.

## Guest access

Guest play is opt-in. Normal SSH key login keeps the same accounts and saves.
With `ARCADE_ALLOW_GUESTS=true`, a client without a usable key can type `guest`
at the keyboard-interactive prompt. The menu displays **Guest Account**, warns
that progress resets when leaving a game, and shows `ssh-keygen -t ed25519` to
run in a local terminal. Reconnecting with a key starts saving future progress;
existing guest progress is never converted or recovered.

Only registry entries with `allow_guests=true` can launch for guests. Farm and
Moon Miner use memory-only visits; Chess remains key-only. Both game backends
also require their guest flags and the existing trusted proxy key. Deploy the
backends first, verify keyed saves, then enable backend flags and the lobby last.
The committed Compose file leaves all guest flags **false**. No migration,
production configuration change, merge or deployment is part of this PR.

| Setting | Default | Purpose |
| --- | --- | --- |
| `ARCADE_ALLOW_GUESTS` | `false` | Keyboard-interactive guest admission. |
| `ARCADE_MAX_GUEST_SESSIONS` | `32` | Guest shell and authenticated transport budgets; must be below the global session cap when enabled. |
| `ARCADE_MAX_GUEST_SESSIONS_PER_IP` | `4` | Concurrent guest shells and transports per public client IP. Shared NATs share this budget. |
| `ARCADE_MAX_PENDING_AUTH` | `64` | Pending handshakes, independent of authenticated shell caps. |
| `ARCADE_AUTH_TIMEOUT` | `60s` | Pre-authentication deadline; cleared after successful authentication. |

Total transports are bounded by `ARCADE_MAX_CONNECTIONS + ARCADE_MAX_PENDING_AUTH`.
The existing global, per-key and rate limits still apply. These initial guest
budgets are configurable rollout values, not measured capacity guarantees.

Local fleet: `./scripts/dev-local-fleet.sh --guests` builds all four siblings,
uses loopback listeners and isolated development databases, and enables guest
flags. Without `--guests`, admission stays key-only. A forced guest client is:

```sh
ssh -p 2222 -o PubkeyAuthentication=no -o PreferredAuthentications=keyboard-interactive guest@127.0.0.1
```

See [the approved architecture](docs/08-guest-access-plan.md) and
[protocol v2](docs/02-bridge-and-identity-protocol.md).
