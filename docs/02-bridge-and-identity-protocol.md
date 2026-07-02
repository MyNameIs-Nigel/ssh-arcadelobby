# Arcade 02 — The Bridge & Identity-Forwarding Protocol (v1)

**Phase:** 2 · **Depends on:** 01 (session loop), 03 (registry types) ·
**This document is the canonical contract.** Game repos implement the
"game side" section and cite this file by path
(`../ssh-arcadelobby/docs/02-bridge-and-identity-protocol.md`). Changing
anything under "Protocol v1" bumps the version and updates every game repo
in the same change set.

## Goal

When a player picks a game, open an SSH connection from the router to that
game's server, prove to the game that the connection comes from the trusted
router, tell it *which player* this is, and then get out of the way: pipe
raw terminal bytes both directions and forward window resizes until the
game session ends.

## The identity problem

Games use "your public key is your account." Behind the router, the game
never sees the player's key — the router cannot re-authenticate with a key
it doesn't hold (that's the whole point of asymmetric auth). So the router
authenticates with **its own key** and forwards the player's identity as
data. The game trusts that data **only** from connections authenticated by
a configured proxy key.

## Protocol v1

### Trust

- The router holds an ed25519 keypair, the **proxy key**
  (`ARCADE_PROXY_KEY_PATH`). The public half is distributed to every game.
- Each game accepts a set of trusted proxy public keys from an
  authorized_keys-format file (game env, e.g. `MOONMINER_PROXY_KEYS_PATH`).
  Multiple entries allowed (key rotation: add new, deploy router, remove
  old).
- **Game-side rule:** if and only if the session's public key exactly
  matches a trusted proxy key, the session is *proxied* and identity comes
  from the username per below. Any other key is a *direct* connection and
  identity is derived from the key itself (unchanged legacy behavior —
  keeps local dev working).

### Username encoding (router → game)

```
<fp-hex64> "." <slot>
```

- `fp-hex64` — lowercase hex of `sha256(playerPublicKey.Marshal())`,
  exactly 64 chars. The game re-encodes these 32 bytes as
  `"SHA256:" + base64rawstd(...)` — its canonical fingerprint format — so a
  player's account is **identical** whether they connect through the arcade
  or directly to the game in dev.
- `slot` — the save slot: the player's original SSH username passed through
  the shared sanitizer (`[a-z0-9_-]{1,32}`, lowercased); the router sends
  `default` if sanitization yields empty. Games still validate.
- Total length ≤ 97 bytes — well inside SSH username limits.
- Game-side parsing must be strict: 64 hex chars, one dot, valid slot —
  anything else on a proxied connection is a protocol error → refuse the
  session with a `\r\n` message (never fall back to treating the router's
  key as a player account).

### Environment (optional, auditing)

After the session channel opens, the router sends one env request:

- `ARCADE_PLAYER_KEY` — the player's public key, authorized_keys format.

Games *may* store it in their `accounts.public_key` column (replacing what
they'd learn from a direct connection). Games must not require it; treat
absence as empty. Games must ignore it entirely on non-proxied connections.

### What games must change (summary for game repos)

1. Identity resolution: proxied-vs-direct branch as above (one function).
2. **Per-key session caps must key on the resolved player fingerprint**,
   not the wire key — otherwise every arcade player shares one cap bucket
   (the proxy key's). Same for any per-account logic.
3. **Per-IP rate limiting** at the game would see only the router's private
   IP; the router is the public enforcement point. Games keep their
   limiters (for direct/dev use) but production compose config sets them
   generous.
4. Listen only on the private Docker network (no published ports).

## Bridge mechanics (router side)

`bridge.Run(sess ssh.Session, game registry.Game, proxyKey ssh.Signer) error`

1. **Dial** `game.Addr` (private network, e.g. `moonminer:22`) with a 5 s
   timeout; client config: user = encoded username above, auth = proxy key,
   `HostKeyCallback` = verify against the game's host key **if pinned in
   the registry**, else `InsecureIgnoreHostKey` — acceptable on the
   isolated Docker network, but the registry supports optional pinning
   (doc 03) and production should use it.
2. **Session setup**: `Setenv("ARCADE_PLAYER_KEY", ...)`, then
   `RequestPty(term, h, w, modes)` using the player's PTY term string and
   current window size (from `sess.Pty()`), then `Shell()`.
3. **Pipe**: `io.Copy` player→game stdin and game stdout→player in two
   goroutines. The player side is already raw (the game's own TUI sets its
   modes through the pipe). Do not buffer beyond small copy buffers —
   latency is the product.
4. **Winch forwarding**: range over the player session's window-change
   channel (`_, winCh, _ := sess.Pty()`), call
   `upstream.WindowChange(h, w)` per event. Send the current size once
   immediately after `Shell()` in case it changed during dial.
5. **Teardown**: whichever side closes first tears down the other. Return
   the upstream exit error (nil on clean game exit) so the handler (doc 01)
   can flash offline vs return to menu silently. Always leave the player's
   session open — returning to the lobby is the caller's job.
6. **Metadata logging only**: fingerprint (truncated), game id, duration,
   exit reason. Never log stream content.

### Failure modes

| Failure | Player experience |
| --- | --- |
| Dial timeout / refused | flash `MOON MINER IS OFFLINE`, back at menu, game marked unhealthy (doc 03 gets a report hook) |
| Game closes mid-session (deploy/crash) | brief `CONNECTION TO GAME LOST — PROGRESS SAVED` notice, back at menu (games flush on disconnect, so this is honest) |
| Router restart | player's whole connection drops; games see disconnects and flush. Documented tradeoff — keep the router boring and rarely deployed |

## Security notes

- The proxy private key can impersonate **any player to any game**. It is
  mounted read-only into the router container from the host, never baked
  into an image, never in git (`.gitignore` covers `proxy_key*`).
- Games must never expose their SSH ports publicly; the private network is
  part of the trust model. Doc 04 enforces this in compose (no `ports:` on
  game services).
- A hostile *direct* connection cannot forge identity: without the proxy
  private key, the proxied branch is unreachable, and the compound username
  just sanitizes into a harmless slot under the attacker's own key.

## Deliverables

- `internal/proxyproto/` — `Encode(fingerprintBytes, slot) string`,
  `Parse(username) (fp [32]byte, slot string, err error)` + exhaustive
  tests. (Games vendor/copy `Parse` — it's ~40 lines; a shared module is
  overkill at fleet size 2. Revisit at 4+ games.)
- `internal/bridge/` — `Run` per above + tests against an in-process fake
  SSH server (see doc 05).

## Acceptance criteria

- [ ] `proxyproto` round-trips all valid inputs; rejects: wrong hex length,
  uppercase hex, missing dot, empty/oversized/invalid slot, trailing junk
  (table-driven).
- [ ] Bridge integration test (doc 05 harness): player session ⇄ fake game
  echoes bytes both ways; winch events arrive upstream; env var received;
  dial-failure path returns the error without killing the player session.
- [ ] Manual: lobby → real ssh-idlefarmer container (once its proxy support
  lands) → play → quit → back at lobby with sane terminal.
- [ ] `go test -race ./internal/bridge ./internal/proxyproto` clean.

## Out of scope / handoffs

- Game-side identity changes → each game repo's framework docs (moonminer:
  `docs/framework/01-ssh-server-and-identity.md`; idlefarmer: retrofit task
  to be filed in that repo).
- Health probing → doc 03. Compose/networks/secrets → doc 04.
