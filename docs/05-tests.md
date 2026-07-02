# Arcade 05 — Test Suite

**Phase:** 3+ (start as each target lands) · **Depends on:** 01–03

## Goal

The router is small but sits in front of everything, so its tests bias
toward **integration**: a real (in-process) player connection, through the
real handler, bridged to a real (in-process) fake game server. If these
pass, a router deploy is safe.

## References

| Source | What to take |
| --- | --- |
| `../../ssh-idlefarmer/internal/server/server_test.go` | in-process SSH harness: ephemeral ports, test keys, PTY requests |
| `../../ssh-moonminer/docs/tests/02-server-integration-tests.md` | harness conventions (Windows-safe, no sleeps-as-assertions, goroutine-leak checks) |

## Deliverables

- `internal/proxyproto/` — exhaustive table tests (see doc 02 criteria)
- `internal/registry/` — load/reload/prober tests (see doc 03 criteria)
- `internal/lobby/` — model update-loop + hitbox tests, golden renders of
  the menu (online/offline mix, long names truncated, 80×24 + 120×40)
- `internal/bridge/` + `internal/server/` — the integration suite below
- `testutil/` — `fakeGame`: a minimal `golang.org/x/crypto/ssh` server that
  records the username + env it receives, allocates the requested PTY, and
  runs a scriptable handler (echo, hang, close-after-N-bytes, exit-code N)

## Spec — integration suite

`startRouter(t, registry)` boots the full router on `127.0.0.1:0` with a
temp host key + proxy key; `dialPlayer(t, key, username)` connects a real
SSH client with an 80×24 PTY. Then:

**Lobby**
- Player connects → output contains the game names; arrow key + Enter on
  an online fake game reaches the bridge (fakeGame records a connection).
- Enter on an offline game → flash text, still in menu, connection alive.

**Bridge & protocol (the core matrix)**
- fakeGame receives username `<hex64>.<slot>` that decodes to the sha256 of
  the *player's* key — for usernames `Scout`, `sc out!!`, `""` → slots
  `scout`, `scout`, `default`.
- fakeGame receives `ARCADE_PLAYER_KEY` matching the player's public key.
- Bytes written by fakeGame appear on the player stream and vice versa
  (echo script).
- Player window resize → fakeGame records a window-change with the new
  size.
- fakeGame exits cleanly → player is back in a rendered menu (assert menu
  text reappears **after** the game output, and a terminal-reset sequence
  was emitted between them).
- fakeGame hangs on accept (never speaks SSH) → dial times out ≤ ~5s,
  player gets the offline flash, prober `Report` flipped the game.
- fakeGame closes mid-stream → "connection lost" notice, back at menu.
- Host-key pinning: registry entry pinned to fakeGame's key connects; pinned
  to a different key refuses and flashes offline (never bridges).

**Security**
- A *direct* connection to fakeGame (bypassing the router) using a random
  key and a forged `<hex64>.<slot>` username: assert the fake's recorded
  session shows the attacker's key ≠ proxy key — i.e. the test encodes the
  game-side rule that the proxied branch requires the proxy key. (The real
  enforcement test lives in each game's suite; this documents the contract
  from the router side.)
- Router logs from a bridged session contain no payload bytes (capture
  slog output; assert on fields only).

**Lifecycle**
- Router SIGTERM with an active bridge: player stream ends, fakeGame sees
  disconnect, process exits within grace budget.
- Session/connection caps + rate limit behave as configured (mirror
  idlefarmer's tests).

## Conventions

Windows + Linux clean under `-race -count=2`; ephemeral ports only; no
external processes; suite < 60 s; every matrix row is an individually
runnable named test.

## Acceptance criteria

- [ ] All rows above implemented and passing on both OSes.
- [ ] Goroutine-leak check wraps the integration harness.
- [ ] Deliberately breaking the username encoding (swap hex for base64)
  fails at least two named tests — verify once, note in PR.
- [ ] CI (`ci.yml` from doc 04) runs this suite on every PR.
