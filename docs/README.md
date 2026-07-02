# ssh-arcadelobby — Build Plan Index

This folder specifies the **ssharcade router**: the single public SSH
endpoint (`play.ssharcade.dev`) that shows an arcade menu and bridges
players into individual game servers. Each numbered doc is a self-contained
task an agent can pick up independently. Read this index first.

## Why this exists (the architecture decision)

Running every game on its own instance + Elastic IP gets expensive fast.
Instead the whole fleet lives on **one EC2 host with one public IPv4**:

```
                     ┌───────────── one EC2 host · one Elastic IP ────────────┐
 players ── ssh:22 ─►│ ssh-arcadelobby ──┬── private net ──► ssh-moonminer:22 │
                     │  (menu + bridge)  ├──────────────────► ssh-idlefarmer:22│
                     │                   └──────────────────► future games…    │
                     └────────────────────────────────────────────────────────┘
```

An SSH connection cannot be handed off mid-session (the handshake is bound
to the endpoint), so the router **terminates** the player's SSH, runs the
menu, then opens a **second SSH connection** to the chosen game and pipes
the terminal streams both ways. Player identity (their public key is their
account) is forwarded via the trusted-proxy protocol in
[02-bridge-and-identity-protocol.md](02-bridge-and-identity-protocol.md) —
that doc is the **canonical contract**; game repos implement against it and
cite it by path.

Consequences to keep in mind everywhere:

- **Games deploy independently.** Restarting `moonminer` never touches the
  router or other games. A down game shows `○ OFFLINE` in the menu.
- **The router must stay small and boring.** Every bridged session dies if
  the router restarts (games flush saves on disconnect, so nothing is lost —
  but players get dropped). Ship it rarely; keep features in the games.
- **Games listen only on the private Docker network.** The router is the
  only public listener and the enforcement point for rate limits.

## The reference project

`../ssh-idlefarmer` is a finished game on this stack. Mirror its server
patterns (TOFU public-key auth, `RequirePTY`, limits, graceful shutdown,
**Windows PTY fixes in `internal/server/teaprogram.go`**) exactly as the
game repos do. `../ssh-moonminer/docs/` shows the task-doc conventions this
plan follows.

## Stack (locked)

Same as the games — see `../ssh-moonminer/docs/README.md`. Router-specific
additions: `golang.org/x/crypto/ssh` used directly as the **client** for the
bridge side.

## Package layout (the contract)

| Path | Purpose |
| --- | --- |
| `cmd/ssh-arcadelobby/` | entry point: config → registry → prober → SSH server → shutdown |
| `internal/config/` | `ARCADE_*` env vars |
| `internal/server/` | wish server, middleware, session handler (lobby ⇄ bridge loop), Windows PTY fixes |
| `internal/registry/` | games.toml loading, hot reload, health prober |
| `internal/proxyproto/` | the identity-forwarding protocol: encode (router side) — games vendor their own parse side per the spec |
| `internal/bridge/` | upstream dial, PTY request, stream piping, winch forwarding, terminal reset |
| `internal/lobby/` | Bubble Tea menu model (keyboard + mouse) |
| `var/` | host key + proxy key in dev (gitignored) |

## Build order

```
Phase 1 (parallel):  01 (lobby TUI, against a fake registry)
                     03 (registry + health prober)
Phase 2:             02 (bridge + protocol; needs 03's registry types)
Phase 3:             04 (deployment + CI/CD)   05 (tests; start anytime after its target lands)
```

## Document map

| Doc | Task |
| --- | --- |
| [01-lobby-menu-and-tui.md](01-lobby-menu-and-tui.md) | Public SSH server + arcade menu (keyboard + mouse), lobby⇄bridge session loop |
| [02-bridge-and-identity-protocol.md](02-bridge-and-identity-protocol.md) | **Canonical** trusted-proxy identity protocol + PTY bridge mechanics |
| [03-games-registry-and-health.md](03-games-registry-and-health.md) | `games.toml` schema, hot reload, health probing |
| [04-deployment-and-cicd.md](04-deployment-and-cicd.md) | Arcade-wide docker-compose, secrets, GitHub Actions CI/CD for router **and** the per-game workflow template |
| [05-tests.md](05-tests.md) | Protocol/unit tests + in-process bridge integration tests |
| [06-fleet-data-durability.md](06-fleet-data-durability.md) | **Canonical** fleet pattern: Litestream → S3 replication so player data survives instance loss |

## Conventions

Inherited from the game repos (`../ssh-moonminer/docs/README.md` §
"Project-wide conventions") — notably: never commit runtime data or keys,
`\r\n` in raw session messages, middleware order, Windows-host PTY
workarounds, `go vet`/`go test` before done. Router-specific additions:

1. **The router never inspects or logs session content.** Bridged bytes are
   opaque; log only connect/select/disconnect metadata.
2. **Protocol changes are versioned.** Any change to the username encoding
   or env vars in doc 02 bumps the protocol version and updates every game
   repo's identity doc in the same change set.
3. **The proxy private key is the crown jewel.** Whoever holds it can
   impersonate any player to any game. It exists only as a mounted secret on
   the host; never in the image, never in git.
4. **The instance is disposable; the S3 bucket is not.** All player data
   follows the durability pattern in doc 06 — a game or router change that
   would put persistent state outside its SQLite file (or the `keys/`
   prefix) is wrong by definition.
