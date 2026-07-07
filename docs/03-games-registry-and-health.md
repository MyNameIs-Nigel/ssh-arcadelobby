# Arcade 03 — Games Registry & Health Probing

**Phase:** 1 · **Blocks:** 02 (registry types), 01's live status ·
**Parallel-safe with:** 01

## Goal

The router learns what games exist from a config file — **adding a game to
the arcade must not require a router rebuild or redeploy** — and knows
which are alive by probing them. The lobby renders from this registry; the
bridge dials from it.

## Deliverables

- `internal/registry/` — `registry.go` (load/validate/hot-reload),
  `prober.go`, tests
- `games.toml` — checked-in default (dev), overridden by a mounted file in
  production (`ARCADE_GAMES_PATH`)

## Spec

### `games.toml`

```toml
[[games]]
id      = "moonminer"                 # stable key: [a-z0-9-]{1,24}, unique
name    = "MOON MINER"                # menu display
tagline = "Drill asteroids, dodge pirates."
descriptors = ["extraction", "4 worlds", "push-your-luck"]
addr    = "moonminer:22"              # private-network host:port
host_key = ""                         # optional: pinned public key (authorized_keys
                                      # format). Empty = accept any (isolated net).
order   = 10                          # menu sort, ascending
version = "1.0.0"                     # optional: fleet version scheme (below)

[[games]]
id      = "idlefarmer"
name    = "IDLE FARMER"
tagline = "Crops grow while you're away."
descriptors = ["idle", "prestige", "market"]
addr    = "idlefarmer:22"
order   = 20
version = "2.0.0"
```

Validation at load: unique ids, non-empty name/addr, parseable host_key
when set, well-formed version when set, ≥ 1 game. A file that fails
validation is **rejected as a whole** and the previous good registry stays
active (log an error); at boot with no good registry, exit non-zero.

### Fleet version scheme

Every fleet component versions as `<channel>.<major>.<minor>`:

- the **first** number is the release channel — `1.x.y` = alpha, `2.x.y` = beta
- the **second** number (`x`) is the major release
- the **third** number (`y`) is the minor patch / hotfix

`version` here is registry metadata for the lobby menu (rendered as e.g.
`v2.0.0 beta` after the descriptors); each game also pins the same string
in its own code (an `internal/version` const shown on its help screen), and
the router pins its own in `internal/version`. The registry can't enforce
that a game's binary agrees with its games.toml entry — keeping the two in
sync is part of shipping a game release. The key is optional so a router
upgrade never hard-fails at boot on an older hand-maintained production
file, but a malformed value rejects the file like any other field.

### Hot reload

Re-stat the file every probe cycle (simplest cross-platform choice — no
fsnotify dependency); on mtime change, reload + revalidate + swap
atomically (the registry hands out immutable snapshots). Adding a game to
production = edit the mounted file, done by the next cycle. Also reload on
SIGHUP for impatient operators (no-op on Windows dev hosts — guard the
signal registration).

### Health prober

- Every `ARCADE_PROBE_INTERVAL` (default `15s`), for each game
  concurrently: TCP dial (2 s timeout) + read the SSH version banner
  (`SSH-2.0-...`) + close. Banner read (not just dial) so a hung container
  that accepts but doesn't speak counts as down. A full handshake is
  unnecessary load on the games.
- Status: `Online` after 1 success; `Offline` after 2 consecutive failures
  (flap damping). Track `LastChange` for an optional "down for 3m" hint.
- `Report(id, err)` hook: the bridge reports dial failures so a game that
  died between probes flips to Offline immediately.
- Subscribers (the lobby) get a notification channel poke on any status or
  registry change; they pull fresh snapshots (`Games() []GameStatus`).

### Types (consumed by 01 and 02)

```go
type Game struct {
    ID, Name, Tagline, Addr string
    Descriptors []string
    HostKey ssh.PublicKey // nil = unpinned
    Order   int
}
type GameStatus struct {
    Game
    Online     bool
    LastChange time.Time
}
```

## Acceptance criteria

- [ ] Valid file loads; each validation rule has a rejecting test; bad
  reload keeps the old registry (test: load good → write bad → still good).
- [ ] Hot reload picks up an added game within one cycle (temp-file test
  with mtime manipulation).
- [ ] Prober against real local listeners: a `net.Listener` that speaks a
  banner probes Online; one that accepts silently probes Offline after the
  damping threshold; a closed port probes Offline; `Report` flips
  immediately.
- [ ] Concurrent `Games()` + reload + probe under `-race` is clean.
- [ ] No goroutine leaks after `Close()` (start N probers in a test, stop,
  compare goroutine counts with settle-retry).

## Out of scope / handoffs

- Menu rendering of statuses → 01. Bridge dial/pinning use → 02.
- The production `games.toml` content and its mount → 04.
- Player counts / rich presence per game — post-MVP (would need a status
  side-channel per game; note as TODO in code, don't build).
