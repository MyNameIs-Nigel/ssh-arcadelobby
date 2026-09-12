# Arcade 01 — Public SSH Server & Lobby Menu TUI

**Phase:** 1 · **Blocks:** 02 (session loop hosts the bridge) ·
**Parallel-safe with:** 03

## Goal

The public-facing half of the router: a Wish v2 SSH server on port 22 that
accepts anyone (trust-on-first-use), shows the arcade menu — game list with
live online/offline status, keyboard **and mouse** — and hands the session
to the bridge (doc 02) when a game is chosen. When the game session ends,
the player returns to the menu, not to a dropped connection.

## References (mirror these)

| File | What to take |
| --- | --- |
| `../../ssh-idlefarmer/internal/server/server.go` | wish server construction, middleware order |
| `../../ssh-idlefarmer/internal/server/pty.go` | `RequirePTY()` + `\r\n` convention |
| `../../ssh-idlefarmer/internal/server/limits.go` | connection caps |
| `../../ssh-idlefarmer/internal/server/teaprogram.go` | **copy nearly verbatim** — Windows-host PTY fixes |
| `../../ssh-moonminer/docs/tui/01-app-shell-input-and-mouse.md` | the hitbox-registry mouse pattern (reuse the design; the packages live per-repo) |

## Deliverables

- `internal/server/` — `server.go`, `handler.go` (the lobby⇄bridge loop),
  `pty.go`, `limits.go`, `teaprogram_windows_opts.go` (or same-named file as
  idlefarmer), `shutdown.go`
- `internal/lobby/` — menu model, theme, tests
- `cmd/ssh-arcadelobby/main.go` — real wiring

## Spec

### Server & middleware

Same shape as the games: public-key TOFU auth (any non-nil key), no
password auth, middleware bottom-up = logging → rate limit → connection
caps → `RequirePTY()` → session handler. Differences from a game:

- **No save/attach middleware** — the router has no persistence at all.
- **Rate limiting is stricter here** (it protects the whole fleet):
  defaults `ARCADE_RATE_LIMIT_PER_SECOND=2`, burst 5, and
  `ARCADE_MAX_CONNECTIONS=200`, `ARCADE_MAX_SESSIONS_PER_KEY=4` (a player
  may be in 2 games + 2 lobbies).
- Host key at `ARCADE_HOST_KEY_PATH` (default `var/ssh_host_key`) — in
  production this lives on the data volume so players never see host-key
  warnings across redeploys.

### The session handler (custom, not the bubbletea middleware)

The stock `bubbletea.Middleware` owns the session for its whole life; the
router instead alternates between two modes, so write a plain
`ssh.Handler`:

```
for {
    choice := runLobbyProgram(session)      // a tea.Program over the session's PTY
    if choice == quit/disconnect { return }
    err := bridge.Run(session, choice)      // doc 02 — blocks until game exit
    resetTerminal(session)                  // see below
    if err != nil { flash offline; mark game unhealthy }
}
```

- `runLobbyProgram` builds the `tea.Program` manually with the session as
  input/output (this is what wish's middleware does internally — mirror
  `bubbletea.MakeOptions(s)`), **plus** the Windows-host options from
  idlefarmer's `teaprogram.go`, **plus** mouse cell-motion enabled (verify
  exact bubbletea v2 option name against the vendored source).
- **`resetTerminal`**: after a game exits, the terminal may be left with
  mouse tracking, alt-screen, or odd modes enabled. Before restarting the
  lobby program, write a conservative reset: leave alt screen, disable all
  mouse-tracking modes, show cursor, SGR reset (`\x1b[?1003l\x1b[?1006l`
  `\x1b[?1049l\x1b[?25h\x1b[0m` — exact minimal set to be validated in 05's
  integration test). Without this, the second lobby visit renders garbage.
- Idle timeout **in the lobby**: 5 minutes without input disconnects with a
  farewell line. During a bridged game, the game's own idle rules apply and
  the router imposes none.

### The menu (80×24, same phosphor-blue language as the games)

```
┌◇ SSHARCADE ─────────────────────────────────── ssharcade.dev ┐
│                                                              │
│   ▸ ● MOON MINER     Drill asteroids, dodge pirates.         │
│       extraction · 4 worlds · push-your-luck                 │
│     ● IDLE FARMER    Crops grow while you're away.           │
│       idle · prestige · market                               │
│     ○ ROGUE-9        OFFLINE — back soon                     │
│                                                              │
│   Your key is your account. Same key, same saves, any game.  │
│                                                              │
└ ↑↓ SELECT · ENTER PLAY · R REFRESH · ? ABOUT · Q QUIT ──── █ ┘
```

- One row per game from the registry (doc 03): status dot (`●` bright =
  online, `○` dim = offline), name, tagline, second line of descriptors.
  Selected row gets `▸` + bright text. Offline rows are selectable but
  Enter flashes `GAME OFFLINE — TRY LATER` instead of bridging.
- **Input contract** (identical semantics to the games' contract in
  `../../ssh-moonminer/docs/tui/01-app-shell-input-and-mouse.md`): ↑/↓ and
  wheel move selection; Enter/Space or click-on-selected/double-click
  launches; single click selects; `R` forces a health re-probe; `?` shows
  an about overlay (what ssharcade is, how identity works, links); `Q` /
  `Ctrl+C` disconnects with a goodbye line.
- Status updates arrive as messages from the prober (doc 03) and re-render
  live while the menu is open.
- A slim footer credit line may show the player's key fingerprint
  (truncated) so "your key is your account" is tangible.
- Render the entire terminal viewport with a fixed dark background via
  Lip Gloss, including terminals larger than 80×24 and the resize prompt,
  so the lobby remains legible regardless of the player's terminal theme.

### Config (env)

| Variable | Default | Meaning |
| --- | --- | --- |
| `ARCADE_LISTEN_HOST` / `ARCADE_LISTEN_PORT` | `0.0.0.0` / `22` | public bind |
| `ARCADE_HOST_KEY_PATH` | `var/ssh_host_key` | server identity |
| `ARCADE_PROXY_KEY_PATH` | `var/proxy_key` | bridge client key (doc 02) |
| `ARCADE_GAMES_PATH` | `games.dev.toml` | registry file (doc 03) |
| `ARCADE_LOBBY_IDLE_TIMEOUT` | `5m` | menu-only idle disconnect |
| `ARCADE_MAX_CONNECTIONS` / `ARCADE_MAX_SESSIONS_PER_KEY` | `200` / `4` | caps |
| `ARCADE_RATE_LIMIT_*` | as idlefarmer | per-IP throttling |
| `ARCADE_LOG_LEVEL` / `ARCADE_LOG_FORMAT` | `info` / `text` | slog |

Config loader mirrors `../../ssh-idlefarmer/internal/config/config.go`.

## Acceptance criteria

- [ ] `ARCADE_LISTEN_PORT=2222 go run ./cmd/ssh-arcadelobby` +
  `ssh -p 2222 localhost` shows the menu; arrows, wheel, click, Enter all
  work; `Q` disconnects politely.
- [ ] No-PTY sessions get the hint text; password auth impossible.
- [ ] With a fake always-fails bridge, choosing a game flashes offline and
  returns to a **functional** menu (terminal reset verified).
- [ ] Lobby idles out at the configured timeout; menu re-renders on prober
  status change.
- [ ] Renders correctly from a Windows host (teaprogram fixes in place).
- [ ] `go vet ./...`, `go test ./...` pass; menu model has update-loop and
  hitbox tests (fake registry).

## Out of scope / handoffs

- Bridge internals + identity forwarding → doc 02 (`bridge.Run` interface:
  takes the `ssh.Session`, the chosen game's registry entry, and the proxy
  key; blocks until the upstream session ends).
- Registry/prober internals → doc 03 (consume an interface:
  `Games() []GameStatus`, `Subscribe() <-chan struct{}`).
- Compose/DNS/deploy → doc 04.
