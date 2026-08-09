# ssh-arcadelobby

The front door of **ssharcade** — a fleet of terminal games played over SSH.
One address, every game:

```bash
ssh play.ssharcade.dev
```

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

**Live** at `play.ssharcade.dev`, router `1.1.2` (alpha channel), fronting
Idle Farmer and Moon Miner. Every push to `main` rebuilds the image and
redeploys the host, so `main` is production.

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
| `ssh-farm` | game (private repo): Idle Farmer v2 — mouse, leaderboards, durable data. **Live** |
| `ssh-moonminer` | game: asteroid-mining extraction loop. **Live** |
| `ssh-chess` | game: GAMBIT — chess over SSH. In build; deploys at its `phase2/06` |

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
