# Guest access across the SSH arcade

**Status:** Approved architecture; implemented on `feat/guest-auth` in all four repositories. Guest flags remain off in committed deployment configuration. The current-state findings below describe the original review baseline.
**Reviewed:** October 10, 2026, against local checkouts.
**Scope:** `ssh-arcadelobby`, `ssh-farm`, `ssh-moonminer`, and the key-only boundary of `ssh-chess`.

## Recommendation

Add an explicit **guest session identity**, carry it through the existing trusted-router bridge, and give Farm and Moon Miner disposable, in-memory game actors. Keep the existing key-backed account and save paths intact. Guests create **no account, save, preference, rename-budget, or leaderboard records** in any database.

The lobby owns the per-game permission gate. Each game also enforces its own admission policy, so an incorrect registry entry cannot enable guests in Chess. No new account service, guest database, temporary SSH keys, or save-schema migration is needed.

| Surface | Guest behavior |
| --- | --- |
| Arcade lobby | Allowed after a guest warning; yellow `Guest Account`; no persistent preferences. |
| Idle Farmer | Play in memory; title `ssh-farm`; player label `GUEST`; no rename or leaderboard participation. |
| Moon Miner | Play in memory; pilot label `GUEST`; no saved pilot, reconnect recovery, or cross-session progression. |
| GAMBIT / Chess | Key authentication required, including bot play; visible but locked in the guest menu. |

**Meaning of “requires persistence”:** lock actions whose purpose is durable identity or publishing an account's progress. Ordinary gameplay can still change temporary state. Blocking every action whose result is normally saved would also block planting, earning coins, buying ships, and most of both games. Session-only settings and progression are recommended liberties, detailed below.

**Recommended lifetime:** one game visit. Leaving a game for the lobby, disconnecting, idling out, or restarting the process discards that guest run. Re-entering starts fresh, even within the same outer SSH connection. Creating a key enables saving on a new connection; it does not claim or recover a previous guest run.

## 1. What the code does today

These findings come from the checked-out implementation, rather than assuming older design documents or the screenshot describe every current behavior. Repository heads were lobby `619128c`, Farm `356a5ac`, Moon Miner `767b932`, and Chess `ff4507c`. Production runtime was not inspected.

| Boundary | Current implementation | Required change |
| --- | --- | --- |
| Public SSH admission | Lobby [`server.go`](../internal/server/server.go) accepts public keys only. [`limits.go`](../internal/server/limits.go) separately rejects nil keys. | Add explicit guest admission and guest-aware limits. |
| Lobby account and preferences | [`handler.go`](../internal/server/handler.go) fingerprints and marshals the key, touches the account, and builds DB-backed preferences. [`prefs.go`](../internal/server/prefs.go) writes alpha acknowledgements. | Resolve identity first; use memory-only guest preferences and skip account storage entirely. |
| Bridge | [`bridge.go`](../internal/bridge/bridge.go) rejects nil player keys and sends `<hex64>.<slot>` using the router's proxy key. | Pass a resolved identity to the bridge; add a guest wire format. |
| Menu permission | [`registry.Game`](../internal/registry/registry.go) has no guest policy. [`Model.activate`](../internal/lobby/lobby.go) checks health and alpha warnings. | Add default-deny guest policy and enforce it before any launch. |
| Game identity | All three games' `internal/identity/resolver.go` require a wire key and distinguish a trusted proxy from a direct keyed player. | Farm/Moon Miner recognize a trusted guest assertion; Chess rejects it. |
| Game initialization | Both [`Farm Manager.Attach`](../../ssh-farm/internal/game/manager.go) and [`Moon Miner Manager.Attach`](../../ssh-moonminer/internal/game/manager.go) always touch an account and load/create a save. | Split persistent and ephemeral initialization before any store call. |
| Farm writes | [`actor.persist`](../../ssh-farm/internal/game/actor.go) writes save and leaderboard columns. [`Session.RenameFarm`](../../ssh-farm/internal/game/session.go) separately calls the database rename gate. | Make ephemeral actors nonpersistent; reject guest renames before the separate database gate. |
| Moon Miner cleanup | [`Manager.detach`](../../ssh-moonminer/internal/game/manager.go) retains dirty actors after failed persistence. [`actor.go`](../../ssh-moonminer/internal/game/actor.go) ticks gameplay and flushes on stop. | Explicitly dispose guests; do not route them through failed-save retention. |
| Chess account creation | [`attachSession`](../../ssh-chess/internal/server/server.go) calls [`openAccount`](../../ssh-chess/internal/server/account.go) after resolving identity. | Refuse guests before account initialization, matchmaking, or bot play. |

Farm currently uses `charm.land/ssh` and Wish v2.0.4; the other inspected repositories use `github.com/charmbracelet/ssh` and Wish v2.0.1. Apply the same wire contract and behavioral tests to each; copying concrete SSH types between repositories will not compile unchanged.

## 2. SSH admission: preserve key login, offer a guest fallback

### Recommended flow

Keep public-key authentication and add **keyboard-interactive guest admission on the lobby only**, behind a proposed `ARCADE_ALLOW_GUESTS` switch, defaulting off until rollout is complete.

1. A normal keyed client authenticates as it does today. The existing key selects the same account and username-selected save slot.
2. A client that reaches keyboard-interactive sees a short warning and an explicit choice, for example:

   ```text
   SSHARCADE guest access
   Guest progress is not saved. Leaving a game ends that run.
   To save future progress, run ssh-keygen -t ed25519 on your computer,
   then reconnect using that key.
   Type guest to continue, or cancel to leave:
   ```

3. Only the affirmative response grants guest access. Cancellation, EOF, timeout, or invalid responses must not silently grant access. This is consent to guest play, not an account password; no secret is requested or recorded.
4. After authentication, resolve the successful result into either a keyed identity or an explicitly admitted guest. Reject an unrecognized result. Carry that resolved value through limits, presence, menu, and bridge.

OpenSSH normally attempts public-key authentication before keyboard-interactive, but clients control their authentication order. A guest therefore means **this connection did not authenticate as a keyed account**, not that the server somehow proved the computer has no key files. This recommendation follows the client ordering documented in [OpenSSH ssh_config](https://man.openbsd.org/ssh_config#PreferredAuthentications).

For troubleshooting or intentionally entering as a guest, document:

```sh
ssh -o PubkeyAuthentication=no -o PreferredAuthentications=keyboard-interactive ssharcade.dev
```

Clients with keyboard-interactive disabled, or interactive prompts disabled by batch mode, need configuration changes. Do not promise seamless access from every SSH client; verify the actual fallback with macOS, Linux, and Windows OpenSSH before release.

### Why not simply enable `NoClientAuth`?

SSH clients can send `none` to discover available authentication methods. Accepting it can finish authentication before the client offers a key, making existing players appear to have lost their accounts. This follows the protocol behavior in [RFC 4252 §5.2](https://www.rfc-editor.org/rfc/rfc4252.html#section-5.2). Keep `none` disabled on the ordinary endpoint.

Likewise, a public-key callback may be invoked when a client offers a key, before proving possession. Resolve keyed identity from the successfully authenticated session, not a mutable “last offered key” variable. Guest admission should set a server-controlled permission marker on successful keyboard-interactive completion; it must not retain key metadata from a prior failed attempt. See the callback contract in [Go SSH ServerConfig](https://pkg.go.dev/golang.org/x/crypto/ssh#ServerConfig). The pinned local SSH wrapper already reconstructs `Session.PublicKey()` from completed-authentication permissions; preserve and test that property.

Do not reserve the SSH username `guest` to select the mode: usernames already select saved slots, so that would break a legitimate keyed slot named `guest`. An alternate anonymous port would add deployment and connection instructions without helping the preferred one-address experience.

## 3. Identity and the trusted bridge

### Application identity

Introduce an explicit kind with three logical states: invalid, keyed, and guest. The zero value must be invalid. Conceptually, a resolved identity contains:

- **Keyed:** canonical fingerprint, sanitized save slot, optional public key text for auditing.
- **Guest:** a server-generated random session identifier; no account fingerprint, public key, or persistent slot.
- **Both:** identity kind and trusted origin information; presentation labels are derived separately.

`GUEST` is a display label, never an actor-map key, database identity, or shared limiter bucket. Avoid fake `SHA256:` strings and fabricated keypairs: existing store and leaderboard code would mistake them for real accounts. Validate the mutually exclusive fields at boundaries, and reject invalid combinations rather than silently downgrading to guest.

Resolve once per admitted shell session and reuse the result. In the games, resolution currently happens in both session limits and attachment; generating a new guest ID independently in those two calls would break ownership and accounting. Cache the validated identity in session-scoped state or pass it explicitly. Multiple shell channels must not accidentally share one mutable game session through connection-scoped state.

### Version 2 protocol proposal

Extend the canonical [bridge identity contract](02-bridge-and-identity-protocol.md), bump its declared protocol version, and keep the existing keyed encoding for compatibility:

| Identity | Router-to-game SSH username | Interpretation |
| --- | --- | --- |
| Keyed | `<64 lowercase hex fingerprint>.<sanitized slot>` | Existing v1 form, unchanged. |
| Guest | `guest.<32 lowercase hex random token>` | New v2 guest form; token is 128 random bits, not a fingerprint or save slot. |

This is a backward-compatible contract extension for keyed traffic; the wire format for guests is deliberately distinguishable and cannot pass the old fingerprint parser. Generate tokens with `crypto/rand` and fail the launch if generation fails; never fall back to timestamps or client usernames.

The router keeps one ephemeral ID for lobby presence/preferences and mints a fresh guest token for each game launch. Separate tokens prevent rapid exit/re-entry from attaching to an actor whose disconnect is still finishing. Neither ID is a reconnect credential or a human identity.

The bridge **still authenticates with the router's existing private proxy key**. Guest describes the player behind that trusted connection, not an unauthenticated backend transport. Only a connection whose verified wire key matches the trusted proxy list may assert a guest username. A direct client sending the same text cannot impersonate a router guest or another player.

Farm and Moon Miner parse both username forms strictly: exact token length, lowercase hex, no extra separators or trailing text. Malformed trusted-proxy identities are refused. A guest message never falls back to the proxy key's fingerprint. Guests omit `ARCADE_PLAYER_KEY`; receivers must not substitute the wire proxy key as guest account metadata. Any unexpected guest key metadata should be rejected as a contradictory message.

Chess can retain its current keyed parsing and explicitly recognize/reject the guest form with a clear message. It must never reinterpret a guest token as a saved account. Ordinary direct keyed development connections continue to work in all games.

For the first release, support guests **through the lobby only**. Game SSH listeners continue to require a key at the transport layer and remain on the private network. Proposed `FARM_ALLOW_GUESTS` and `MOONMINER_ALLOW_GUESTS` flags control acceptance of trusted proxied guests, initially false. Chess has no guest-enable switch. A separate direct-guest development mode is optional future scope.

## 4. Lobby policy, state, and presentation

### Per-game admission

Add `AllowGuests bool` to `registry.Game` and `allow_guests` to registry parsing, with missing values defaulting to false. Configure the existing game IDs:

| Existing registry ID | New `allow_guests` value |
| --- | --- |
| `farm` | `true` |
| `moonminer` | `true` |
| `chess` | `false` |

Update `deploy/games.toml`, `games.local.toml`, and `games.dev.toml` when implementing. Preserve the current all-or-nothing reload behavior. Guest permission is independent of online health and alpha/beta channel; neither an SSH version banner nor a beta label proves guest capability.

Use one admission predicate in the UI and server launch path. Keyboards, mouse clicks, double-clicks, and alpha-warning confirmation must converge on it. Immediately before dialing, re-read the selected game by ID from the current registry and recheck its existence, address, online status, and guest permission. The `Result.Game` snapshot or an open alpha modal may be stale after a hot reload.

Keep Chess visible with `KEY REQUIRED`, and explain on activation:

```text
GAMBIT requires an SSH key. Generate a key and reconnect to play.
```

A guest restriction is not an outage. Do not call the registry health-failure hook for permission refusal, capacity rejection, or an unsupported guest protocol. In particular, the current bridge groups SSH handshake failures into `ErrDialFailed`; give access/protocol failures a distinct outcome, or leave health decisions to the independent prober when the refusal cannot be classified reliably.

### Match the supplied lobby layout

In [`view.go`](../internal/lobby/view.go) and [`theme.go`](../internal/lobby/theme.go):

- Replace the lower-right fingerprint display with **`Guest Account` in yellow** for guests. Keep the fingerprint for keyed sessions.
- Replace the key-account slogan with `Guest progress won't be saved. Leaving a game resets it.`
- Use the next line for `On your computer: ssh-keygen -t ed25519`, with the command in cyan or green so it differs from the warning and the yellow account label.
- Keep the existing footer shortcuts. Mark allowed games `GUEST OK` and Chess `KEY REQUIRED` without concealing offline status.
- Retain textual labels in reduced-color or monochrome terminals; color must not be the only distinction.

The current fixed 80×24 frame has identity text on content row 16, flash text on 18, and fingerprint on 20. Row 17 can hold the short command without expanding the frame. Verify with the operator banner enabled, at the supplied screenshot's 108×32 size, at 80×24, and below minimum size. Put policy badges where descriptor truncation cannot hide them.

The command is an instruction to run locally, never something the game executes. The About/help screen should explain reconnecting, keeping the private key, and declining any overwrite prompt for an existing key. `ssh-keygen -t ed25519` is supported by [OpenSSH ssh-keygen](https://man.openbsd.org/ssh-keygen#t); retain its normal filename/passphrase prompts. A newly generated default key is normally used on the next connection. If necessary, show an explicit local command such as `ssh -o IdentitiesOnly=yes -i ~/.ssh/id_ed25519 ssharcade.dev`, preserving any chosen username for existing saved slots.

### No guest database footprint

Skip `TouchAccount` and all marshaling/fingerprinting that assumes a nonnil player key. Use a small in-memory preference object for guest alpha acknowledgements and warnings, kept for the outer lobby session rather than recreated each time the menu returns. Guest wording becomes `Don't show again this connection`; keyed acknowledgement behavior remains unchanged.

Presence tracking currently deduplicates on fingerprint. Give guests a namespaced ephemeral presence key, so two guests are not collapsed into one empty-string player. Count guests as anonymous sessions, not verified unique people. Continue publishing aggregate counts only; guest identifiers never need to enter the stats JSON or SQLite.

Replace guest-facing farewell, disconnect, and maintenance text that promises saving. The existing router error `CONNECTION ... LOST — PROGRESS SAVED` is especially unsuitable: for guests nothing is saved, and even for keyed users a crash is not proof that the final write succeeded. Prefer neutral keyed failure wording and `Guest progress was discarded` for guests.

## 5. Game runtime: same simulation, different storage lifecycle

Reuse the current manager/actor/session structure. Do not create a second simulation implementation or a per-guest SQLite database. Add a validated runtime persistence mode and separate persistent save keys from ephemeral actor IDs.

**Keyed attachment:** retain account lookup, saved-state decoding, concurrency/takeover policy, catch-up or reconnect behavior, autosave, and final flush.

**Guest attachment:** validate policy, construct `sim.New(content, seed, now)`, start an isolated actor, and return the normal session handle with guest capabilities. Do this before `TouchAccount` or `LoadOrCreateSave` can be reached. Pass guest identity/capabilities to the TUI; `Created` alone is insufficient because keyed first-time players also have new states.

**Storage invariant:** no guest-attributed calls to account creation, save creation/loading, persistence, rename budgets, preferences, or account metadata. Read-only shared content and Farm's public leaderboard remain available. Existing keyed sessions still use the same database normally, so the guarantee is no guest records or guest writes, not zero database activity for the process.

Implement the invariant at a small number of explicit boundaries:

1. Branch attachment before storage access.
2. Make actor persistence capability explicit; ephemeral actors do not serialize/write state or schedule save retries. Disable their autosave ticker while retaining gameplay tickers.
3. Guard any direct-store actions, especially Farm's rename path, independently of autosave.
4. Stop and remove ephemeral actors on all terminal paths, regardless of `dirty`. A mutation flag must not accidentally mean a guest needs a durable flush.
5. Require validated persistent identity for store-facing account operations. A fake fingerprint or accidental guest-to-persistent conversion must fail before a write.

Keep the persistence mode immutable during actor lifetime. Use a normal snapshot carrying session metadata or a separate capabilities value; there is no reason to add a guest flag to serialized simulation state. Keep both simulations pure and preserve their existing economy, clocks, random-state rules, and actor ownership.

No guest-to-account conversion is proposed. SSH authentication is already complete when the TUI is running; generating a key in another terminal does not upgrade this connection. Exit and reconnect to start or reopen the keyed account. This avoids claiming guest state into an existing save, identity races, and hidden persistence.

## 6. Idle Farmer

### Identity and controls

Derive guest presentation independently of `SessionIdentity.Slot`: display the title exactly **`ssh-farm`** and player label exactly **`GUEST`**. The existing header already falls back to `ssh-farm` when `State.FarmName` is empty; keep that fresh state and force guest presentation where appropriate. Audit Stats, help, onboarding, terminal title, and every other slot/account display so the SSH username or random guest token never leaks into the player label.

| Feature | Guest policy | Reason |
| --- | --- | --- |
| Planting, harvesting, market, land, automation, gifts | Allow within the current run. | These are the playable demo; their state fits in memory. |
| Rebirth, Starseeds, upgrades, achievements, contracts | Allow under the existing unlock rules, session-only. | Avoid an alternate balance system; clarify that “permanent” means only for this visit when guest. |
| Farm rename | Lock; keep `ssh-farm`. | Explicit requirement and a public identity feature with a direct database gate. |
| Leaderboard publication, personal rank, account name style | No participation; name-style selection locked. | Guests have no durable or public identity. |
| Viewing the public leaderboard | Allow read-only, no `YOU` row. | Lets guests explore the shared game without publishing their state. |
| News, critters, flavor, background, seasonal presentation | Allow session-only changes and label them accordingly. | Preserves comfort/accessibility and existing gameplay preferences without pretending to save. |
| Replant/tutorial acknowledgements | Remember only within the visit. | Avoid repeated prompts during play without creating persistent preferences. |
| Offline growth and reopening a farm | Unavailable. | There is no guest save to advance or restore. |

Session-only progression and settings are deliberate choices, not new durable guest features. For example, a contract reward can work during the visit without adding a leaderboard record. Locking account name-style selection remains appropriate even if a contract unlocks it.

### Enforce locks below the UI

Introduce a typed error such as `ErrGuestRequiresKey`. `RenameFarm` must return it **before** `Store.GateRename`; otherwise merely attempting a forbidden action reaches SQLite. Also guard `Session.SetFarmName`, which currently bypasses moderation, and `SetLeaderboardNameStyle`. Keep storage policy out of pure `sim.SetFarmName` and enforce it at the game/session boundary.

Route the Stats `n` shortcut, leaderboard `n`, mouse rename controls, and submit paths through the same decision. Keep locked controls discoverable and show:

```text
You're playing as a guest. An SSH key is required to rename your farm
and save progress. Run ssh-keygen -t ed25519 locally, then reconnect.
```

Use the feature name in equivalent notices for other locked actions. Do not display an operator moderation lock or generic store error for a guest restriction.

The leaderboard engine already accepts a zero `SaveRef` and returns no personal row. Use that for guest browsing; add an explicit `Guests are not ranked` explanation in the TUI instead of the normal unranked-player hint. No guest save rows means no guest earnings, names, or cosmetics can enter its database-derived rankings.

Keep a visible `GUEST · NOT SAVED` indication during play. Adapt onboarding, “settings are saved per farm,” “permanent” rewards, welcome-back, kick, idle, maintenance, and quit messages. Add a brief confirmation on intentional return to the lobby because it discards the run; automatic disconnects cannot wait for confirmation. Preserve Farm's responsive layouts and hitboxes when introducing the warning.

## 7. Moon Miner

Use the same identity and ephemeral actor model. Show **`PILOT: GUEST`**, a guest terminal title, and a compact `NOT SAVED` indicator. Onboarding/help should state that ships, credits, upgrades, ratings, permits, progress, and the ship log last only for this game visit.

Keep mining, scanning, combat, escape/death, shipyard purchases, modules, travel, and progression playable under the normal rules. These actions update actor-owned state; they do not inherently need a durable account. Settings/tweaks remain available in memory with a session-only label. There is no public player profile to rename or leaderboard identity to manage in the inspected implementation, so do not invent disabled profile screens.

The main extra work is lifecycle handling:

- Leave the gameplay ticker and snapshot revision ordering intact. Suppressing saves must not pause mining or pirates.
- On ordinary in-game escape, death, or return to port, preserve normal simulation outcomes within the visit.
- On guest disconnect or shutdown, stop the actor and discard it. Skip the persistent pilot's emergency-resolution-to-save workflow: there is no reconnectable outcome to protect. This avoids spending simulation/flush work on state being thrown away.
- Explicitly bypass dirty-state retry retention. Currently detach only stops an actor when it is sessionless and clean; a save no-op that leaves `dirty` set would retain every guest indefinitely.
- Do not show restored-location, offline-income, or “last disconnect” recovery from a prior guest visit. A new visit always starts from the normal fresh state.
- Adapt maintenance and exit copy, and confirm an intentional return to the lobby. Keyed pilots retain the existing emergency bail, failed-save recovery, location restoration, and final-flush behavior.

For any future action that really requires a persisted pilot, reject it with the same key-required capability error. At present, broad locks on ship purchases or travel would restrict the demo unnecessarily.

## 8. Chess remains key-only

Set its lobby entry to `allow_guests = false` and retain public-key-only transport admission. Add an explicit guest-protocol rejection before caps/account setup can produce a normal player, with `GAMBIT requires an SSH key. Reconnect with a key to play.`

Test both defenses separately: a guest cannot launch Chess through the lobby, and a deliberately misconfigured/test bridge carrying a guest token is still rejected by Chess without touching its account/match tables. This applies to all modes, including bots and spectating if reachable through the same session entry.

“Login only” preserves the current model: proving a key creates or opens an account automatically. It does not introduce passwords, registration approval, or a requirement that the key already exist in the database.

## 9. Limits and failure handling

Anonymous random tokens provide isolation, not abuse resistance. Keep the global session cap and use a separate concurrent guest cap by **actual client IP at the lobby**, alongside its existing per-IP rate limiter. A proposed starting limit is four guest sessions per IP, configurable and subject to real usage; shared networks may need adjustment. Add a guest-wide capacity budget so anonymous traffic cannot consume every keyed-player seat. Choose that total from measured actor memory/CPU, rather than implying a safe fleet capacity without measurement.

Do not bucket guests by an empty fingerprint, the literal `GUEST`, or the proxy key. The game sees the router's IP, so the public IP restriction belongs at the router. Backend global caps still protect actor allocation, and resolved guest tokens identify individual ephemeral sessions. Release every counter on rejection, disconnect, timeout, and shutdown.

The current Wish limiter runs after authentication. Adding an interactive auth prompt also needs bounded pre-authentication connection counts and a handshake/prompt deadline at the listener/auth boundary. A proposed initial deadline is 60 seconds. Clear that deadline after authentication; do not impose a whole-connection `MaxTimeout` that later kills an active game. Keep existing input-based idle rules after admission, and explicitly bound guest actor idle cleanup where necessary.

Keep logs operational: mode, game, a short ephemeral correlation ID, and outcome. Do not log challenge answers or create “guest accounts” for analytics. Aggregate live presence is compatible with no persistent player record; operational logs should not be represented to users as a promise of zero logging.

## 10. Implementation order and acceptance

This is a proposed sequence, not authorization to deploy. Preserve the fleet's existing save-protection and deployment procedures; no database migration, volume replacement, or key rotation is required by this design.

1. **Define and test the contract.** Update the canonical protocol and per-repo identity docs; add kind validation, parsing vectors, and Chess refusal coverage. Keep existing keyed wire/account behavior unchanged.
2. **Implement both games behind disabled flags.** Add ephemeral attachment, storage/capability guards, disposal, guest UI, and tests. Update game framework/TUI docs alongside code. Keep simulation and persistent-save regressions green.
3. **Implement the lobby behind its disabled flag.** Add fallback authentication, registry policy, guest preferences/presence/limits, authoritative launch checks, bridge support, and the requested UI copy/colors.
4. **Exercise the local fleet.** Use the same proxy trust path as production. Test with real SSH clients, both input methods, resizing, game return, multiple simultaneous guests, and forced disconnect/shutdown.
5. **Enable backend support before public admission.** Make compatible game versions available first, enable trusted guest support for Farm/Moon Miner, then enable registry permissions and lobby fallback after the matrix below passes. Chess stays disabled throughout.
6. **Rollback by closing admission.** Disable new guest admission/launches first. Do not downgrade a guest-capable backend while the router can still send guests. Drain active guest sessions or warn that a restart ends them; preserve all keyed saves and data volumes.

| Acceptance area | Required evidence before enabling |
| --- | --- |
| Keyed compatibility | Same key + same slot opens identical existing saves direct and proxied; key callback success still requires possession; default keyed OpenSSH connection never becomes guest. |
| Guest admission | No-key OpenSSH clients can explicitly continue; cancellation/EOF/disabled feature reject; failed key attempts cannot contaminate guest identity; username `guest` remains a valid keyed slot. |
| Protocol trust | Guest frames accepted only from trusted proxy keys and only by opted-in games; malformed tokens and contradictory metadata reject; old keyed frames remain valid; old backends fail closed on guest frames. |
| Chess boundary | UI, mouse, stale alpha modal, server launch bypass, direct no-key connection, and trusted guest-proxy connection cannot reach a game or create records. |
| No persistence | Spy/failing writer detects zero guest-attributed store calls through attach, actions, rename, autosave interval, detach, idle, and shutdown; disposable SQLite table contents confirm no guest account/save/preferences/rename/leaderboard writes. |
| Isolation | Two guests, even with the same SSH username/IP, have independent state and cannot take over each other or keyed saves; token namespaces cannot collide with keyed account IDs. |
| Disposal | Guest actor counts/goroutines and limiter/presence counts return to baseline after all exit paths, including Moon Miner dirty/active-run disconnect; rapid re-entry starts fresh. |
| Farm controls | `ssh-farm` and `GUEST` appear consistently; keyboard and mouse rename/style attempts explain the lock; leaderboard has no guest `YOU` row; allowed progression/settings work only for the visit. |
| Moon Miner play | Mining tick cadence, combat, ship purchases, settings, and snapshots work in guest mode; fresh re-entry has no previous pilot/log; keyed emergency/reconnect behavior is unchanged. |
| UX and layout | Yellow account label, differently colored local command, visible lock markers, honest save/exit copy; 80×24 and screenshot size, banner on/off, small-terminal fallbacks, Windows PTY, and reduced-color rendering. |
| Reload and failures | Removing/disabling a game while a menu/modal is open prevents a new launch; policy denial does not mark it offline; backend rejection returns safely to the lobby. |
| Resource controls | Guest per-IP/global limits, authentication deadline, stalled prompts, and cleanup verified; limits do not share a single proxy/empty-ID bucket or terminate long-running keyed games. |

During implementation, use each repository's normal build/vet/test gates and relevant race tests for identity, managers, limits, presence, bridge, and TUI. Existing tests passing is baseline evidence only; guest acceptance requires the new cases above. Keep protocol changes coordinated across repos, with the lobby as contract owner; a shared dependency is not necessary for this small extension, but common parser test vectors prevent drift.

### Baseline verification performed for this proposal

`go build ./...` and `go vet ./...` passed in all four repositories using the installed Go 1.27.1 toolchain and a writable temporary build cache. After rerunning tests with localhost networking permitted, `go test ./...` passed in Lobby, Moon Miner, and Chess. Farm's server, identity, game, simulation, store, and leaderboard tests passed, but its TUI package reported five existing failures:

- `TestCanvasClamp`
- `TestCanvasHeightStableAcrossScreensAndNotices`
- `TestComposedCanvasIsRectangular`
- `TestOverlayBoxesFitContentWidthAtStockSize`
- `TestComposedCanvasIsRectangularAtStockSize`

The output includes seasonal sky width/height mismatches and a picker overlay exceeding its content width. These are observed test failures in unchanged code, not a complete diagnosis or proof that every corresponding runtime screen is broken. Keep them separate from guest implementation regressions and verify guest warning layouts with seasonal decoration enabled. No application code was changed, no new guest behavior was executed, and no production or remote-CI validation was performed.

## 11. Judgment calls, recommendations, and pitfalls

| Type | Decision or finding | Why it matters |
| --- | --- | --- |
| Recommendation | Public-key login plus explicit keyboard-interactive guest fallback. | Preserves normal key login; blanket `NoClientAuth` can make keyed clients guests. |
| Tradeoff | Guests are identified by successful admission, not absence of key files. | Client auth order is configurable; the server cannot inspect the user's computer. |
| Recommendation | Guest support only through the trusted lobby in the first release. | Meets fleet usage without opening another anonymous ingress path. |
| Design choice | Guest progress ends on leaving each game; no later account conversion. | Simple disposal, no reconnect tokens or hidden save-transfer rules. |
| Design choice | Normal progression and comfort settings remain usable in RAM. | A playable demo needs state; persistent identity/publication features remain locked. |
| Required behavior | Farm title `ssh-farm`, player `GUEST`; rename and leaderboard identity locked. | Matches the requested presentation and prevents guest public presence. |
| Recommendation | Browse Farm's leaderboard without a personal row. | Existing engine supports this; no guest ranking or database entry is needed. |
| Pitfall | Rename writes independently of autosave; a direct setter bypasses rename checks. | Guard all player-facing session methods before database access or state mutation. |
| Pitfall | Moon Miner retains dirty actors for save retries. | A naive no-op save leaks guest actors; disposal must ignore persistent retry rules. |
| Design choice | Discard guest mining runs on disconnect without emergency save resolution. | No reconnectable state exists; normal in-game escape/death rules remain intact. |
| Pitfall | Empty/shared fingerprints merge caps, actors, or live player counts. | Use explicit identity kinds and random namespaced IDs; never `GUEST` as an internal key. |
| Pitfall | Menu and alpha-modal snapshots become stale; refusals resemble bridge failures. | Recheck at launch and keep access policy separate from game health. |
| Pitfall | Guest instructions may falsely promise saved progress or automatic key activation. | Change save-related copy and require a new keyed SSH connection. |
| Existing issue | Farm has five baseline layout/overflow test failures, including seasonal decoration. | Establish a known layout baseline before adding guest warnings; findings are unchanged by this proposal. |
| Operational choice | Add bounded guest/pre-auth limits and confirm intentional game exit. | Protects finite actor resources and makes the disposable-run boundary clear. |
| Compatibility | Keep keyed wire format and database identities unchanged; no guest schema. | Avoids migrating player saves and supports a backend-first rollout. |

## Implementation review

Game PRs: [Farm #40](https://github.com/MyNameIs-Nigel/ssh-farm/pull/40),
[Moon Miner #64](https://github.com/MyNameIs-Nigel/ssh-moonminer/pull/64),
[Chess #27](https://github.com/MyNameIs-Nigel/ssh-chess/pull/27).

The implementation preserves the keyed v1 wire format and adds only the guest
v2 assertion. The lobby also bounds authenticated guest transports, because a
client can authenticate without opening a shell; shell-only caps would miss it.
Default guest capacity is 32 of 200 global sessions, with four guests per public
IP. Pending authentication has a separate 64-connection, 60-second budget; total
transports are bounded at global sessions plus pending capacity. These values
are starting points for a measured rollout. SSH host-key/access failures no
longer poison liveness, and crash notices no longer promise a completed save.

Validation includes real SSH client/server integration, no-store spies, fresh
visit identity, actor disposal and isolation, keyed compatibility, stale alpha
and policy checks, per-IP/session/transport caps, and 80×24/108×32 guest layouts.
Full build, vet and race checks run in each repository. Native OpenSSH checks
and remaining platform limits are recorded in the PR validation evidence.

| Judgement call | Why |
| --- | --- |
| Ordinary gameplay, upgrades, contracts and settings remain usable in RAM. | Blocking every normally saved action would prevent meaningful guest play. Durable identity/publishing actions remain locked. |
| Guest progress is discarded rather than upgraded into an account. | Avoids ambiguous ownership, recovery tokens and a new persistence model. |
| Guest alpha dismissal lasts for one lobby shell. | Reduces repeated warnings during play without writing an account preference. |
| Guest transport caps accompany shell caps. | Prevents idle authenticated transports from consuming the keyed connection reserve. |
| Access rejection does not mark a game offline. | A healthy backend can reject policy, capacity or credentials independently of liveness. |
| Guest and keyed disconnect copy differs, without guarantees of a final save. | Guest runs are disposable; a crash can interrupt the latest keyed write. |
| Full bug tally lives in [guest-auth-bug-tally.md](guest-auth-bug-tally.md). | Counts verified pre-existing defects separately from implementing the requested feature. |

### Local end-to-end evidence

Native macOS OpenSSH connected to all four locally built services with fresh
throwaway proxy/player/host keys and separate temporary SQLite databases.
Consent, lobby warning/markers, Farm `ssh-farm`/`GUEST` identity and rename lock,
both game discard confirmations, return to the lobby, and Chess refusal passed.
After guest play all four `accounts` counts stayed zero, both games' `saves`
stayed zero, Chess `matches` stayed zero, and lobby `alpha_warning_acks` stayed
zero. Keyed Farm play created one account/save; reconnecting with the same key
and username reused those rows. Test services, keys and databases were removed.

Linux CI exercises the Go SSH integrations. Native Linux/Windows OpenSSH and a
production deployment were not run here; those remain rollout checks. Farm's
remote Windows race/build coverage is distinct from a native OpenSSH UX check.
