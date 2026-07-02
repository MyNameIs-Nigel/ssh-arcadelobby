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
 players ── ssh:22 ─►│ ssh-arcadelobby ──┬── private net ──► ssh-moonminer:22 │
                     │  (menu + bridge)  ├──────────────────► ssh-idlefarmer:22│
                     │                   └──────────────────► future games…    │
                     └────────────────────────────────────────────────────────┘
```

## Status

**Planning.** `docs/` contains the complete build plan:
[docs/README.md](docs/README.md) is the index; each numbered doc is a
self-contained task an agent can pick up independently. The
identity-forwarding protocol in
[docs/02-bridge-and-identity-protocol.md](docs/02-bridge-and-identity-protocol.md)
is the canonical contract every game repo implements against.

## Stack (planned)

- **Go 1.26+**, pure Go (`CGO_ENABLED=0`)
- **charm.land/wish/v2** — public SSH server (menu side)
- **golang.org/x/crypto/ssh** — SSH client (bridge side)
- **charm.land/bubbletea/v2** + **lipgloss/v2** — lobby TUI
- **Docker** — distroless static image; this repo also owns the arcade-wide
  `docker-compose.yml`

## Fleet repos

| Repo | Role |
| --- | --- |
| `ssh-arcadelobby` | this — router, lobby TUI, arcade compose + deploy docs, fleet durability pattern |
| `ssh-moonminer` | game: asteroid-mining extraction loop |
| `ssh-farm` | game (private repo): Idle Farmer v2 — mouse, leaderboards, durable data |
| `ssh-idlefarmer` | game: idle farming v1 (reference implementation; retired at the v2 cutover) |

## Commands

```bash
go build -o bin/ssh-arcadelobby ./cmd/ssh-arcadelobby   # build
go test ./...                                            # test
go vet ./...                                             # vet
```
