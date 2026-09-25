# Arcade 07 — Live Player Count API

**Phase:** 4 · **Depends on:** 01 (session loop), 03 (registry), 04 (compose,
Caddy) · **Consumers:** the website (`ssharcade-web`), anyone else — the data
is public

## Goal

A live count of who is playing, readable by anyone over HTTPS:

```
GET https://api.ssharcade.dev/v1/players
```

It should cost the host next to nothing, add no new service, credential or
external dependency, and leave the `web` container's isolation (doc 04,
deploy/README.md § "Blast radius") exactly as strong as it was.

## Design

**The router is the only source.** Every player session goes through the
router — games publish no ports — and the router's session loop
(`internal/server/handler.go`) already knows when a session is in the menu
and when it is bridged into a game. So the games report nothing and need no
changes; a new game is counted the moment it is in `games.toml`.

```
 players ─ssh─► router ─── presence.Tracker (in memory: one Seat per session)
                  │
                  │ every 5s, atomic rename
                  ▼
        arcade-stats volume (tmpfs) ── players.json
                  │ read-only mount
                  ▼
        web (Caddy, edge network) ── https://api.ssharcade.dev/v1/players
                  ▲
                  │ browser polling (never server-side)
             www.ssharcade.dev
```

- `internal/presence/tracker.go` — `Tracker`/`Seat`. The handler `Join`s a
  seat once the session has passed the caps and PTY checks (so a rejected
  connection is never a player), `Enter`s the game ID around `bridge.Run`,
  goes back to `Lobby` after, and `Leave`s on disconnect.
- `internal/presence/publisher.go` — writes the snapshot immediately at
  boot, then every `ARCADE_STATS_INTERVAL`, and a final `"online": false`
  document as the first step of graceful shutdown.
- **A file, not an HTTP listener.** Caddy reverse-proxying to the router
  would need a network path from the one container with a public
  unauthenticated listener to the one holding the proxy key. A shared
  volume gives Caddy nothing to talk to. The file is replaced by
  write-temp-then-rename, so Caddy never serves a torn document.
- **Why not push to Vercel:** Vercel functions keep no state between
  requests, so a pushed count would need an external store (KV/Redis), a
  shared secret on the host, a push loop, and a TTL to notice a dead
  router — four moving parts to relay one number that already lives here.

### Privacy

Only counts leave the router. Fingerprints are held in memory solely to
count a player with several terminals open once; they are never written to
the snapshot, logged by this package, or persisted.

## API reference

### `GET https://api.ssharcade.dev/v1/players`

```json
{
  "version": 1,
  "online": true,
  "updated_at": "2026-09-25T18:04:05Z",
  "interval_seconds": 5,
  "players": 7,
  "sessions": 9,
  "in_lobby": 2,
  "games": [
    { "id": "farm",      "name": "IDLE FARMER", "status": "online",  "players": 3 },
    { "id": "moonminer", "name": "MOON MINER",  "status": "online",  "players": 2 },
    { "id": "chess",     "name": "GAMBIT",      "status": "offline", "players": 0 }
  ]
}
```

| Field | Meaning |
| --- | --- |
| `version` | Schema version, currently `1`. Bumped only for a breaking change (a renamed/removed field or a changed meaning). New fields may appear at any time without a bump — ignore unknown ones. |
| `online` | `true` while the router is serving. `false` is written as the router shuts down; every count is then `0` and every game `offline`. |
| `updated_at` | When this snapshot was written, UTC, RFC 3339, whole seconds. |
| `interval_seconds` | How often the router rewrites the snapshot. |
| `players` | **The headline number.** Unique SSH keys connected to the arcade right now, whether in the menu or in a game. One person with two terminals counts once. |
| `sessions` | Open SSH sessions (≥ `players`). |
| `in_lobby` | Unique keys with at least one session sitting in the arcade menu. |
| `games[]` | Every cabinet in `games.toml`, in menu order. |
| `games[].id` / `name` | The registry `id` (stable — key on this) and the menu display name (may change). |
| `games[].status` | `"online"` or `"offline"`, from the router's health prober (doc 03). |
| `games[].players` | Unique keys currently bridged into that game. |

Counting rules worth knowing before displaying the numbers:

- `in_lobby` plus the per-game counts can **exceed** `players`: someone with
  one terminal in the menu and another in Moon Miner is one player, counted
  in both places. Never sum the parts to get the total — use `players`.
- A game removed from `games.toml` while people are still in it drops out of
  `games[]` immediately; those players still count in `players` until they
  leave.

### Responses and headers

| Situation | Response |
| --- | --- |
| Normal | `200`, `Content-Type: application/json` |
| Router has never written a snapshot since the host (or both containers) last started | `404` |
| Any other path on `api.ssharcade.dev` | `404` |

Every response carries:

- `Access-Control-Allow-Origin: *` — callable from any page, including
  Vercel preview deploys. It is a plain `GET` with no custom headers, so
  browsers send no preflight.
- `Access-Control-Expose-Headers: Date` — so browser code can read the
  server's clock (see staleness below).
- `Cache-Control: public, max-age=4` — under the 5s write interval, so no
  cache ever serves a count more than one write behind.
- `ETag` / `Last-Modified` — conditional requests get a `304`.

### Staleness: the rule every client must apply

If the router crashes (as opposed to shutting down cleanly), nobody writes
`"online": false` — the last snapshot simply stops changing. So clients
decide freshness themselves:

> Treat the snapshot as **unknown** if `online` is `false`, or if its age is
> more than `3 × interval_seconds` (15s today).

Compute the age as the response's `Date` header minus `updated_at`. Both
come from the host's clock, so this is immune to a visitor whose own clock
is minutes off; `Date.now()` is not. Fall back to `Date.now()` only when the
header is missing.

When unknown, show nothing or a neutral "—", never the stale number and
never `0` (zero is a real answer: an open arcade nobody is in).

## Consuming it from the website (browser-side polling)

The website work is deferred; this is the contract it should follow when it
lands. Poll **from the browser**, never from a Next.js server component or
route handler: a server-side fetch with `revalidate` would have Vercel
hitting the API on a schedule whether anyone is looking or not, while
browser polling only costs anything while a visitor has the page open.

- Poll every **15–30 seconds**. Faster buys nothing — the data changes at
  most every 5s and players come and go on a scale of minutes.
- **Pause while the tab is hidden** (`visibilitychange`), and fetch once
  immediately when it becomes visible again.
- On any error or non-`200`, show the unknown state and keep polling.
- Key per-game counts on `games[].id`, not on `name`.

```ts
"use client";

import { useEffect, useState } from "react";

export type PlayerCount = {
  version: number;
  online: boolean;
  updated_at: string;
  interval_seconds: number;
  players: number;
  sessions: number;
  in_lobby: number;
  games: { id: string; name: string; status: "online" | "offline"; players: number }[];
};

const URL = "https://api.ssharcade.dev/v1/players";
const POLL_MS = 20_000;

/** null = unknown (unreachable, stale, or the arcade is down). */
export function usePlayerCount(): PlayerCount | null {
  const [data, setData] = useState<PlayerCount | null>(null);

  useEffect(() => {
    let timer: ReturnType<typeof setTimeout> | undefined;
    let ctrl: AbortController | undefined;

    const poll = async () => {
      ctrl?.abort();
      ctrl = new AbortController();
      try {
        const res = await fetch(URL, { signal: ctrl.signal, cache: "no-store" });
        if (!res.ok) throw new Error(String(res.status));
        const snap: PlayerCount = await res.json();
        const serverNow = Date.parse(res.headers.get("Date") ?? "") || Date.now();
        const ageMs = serverNow - Date.parse(snap.updated_at);
        const fresh = snap.online && ageMs <= 3 * snap.interval_seconds * 1000;
        setData(fresh ? snap : null);
      } catch (err) {
        if ((err as Error).name !== "AbortError") setData(null);
      }
      if (document.visibilityState === "visible") timer = setTimeout(poll, POLL_MS);
    };

    const onVisibility = () => {
      clearTimeout(timer);
      if (document.visibilityState === "visible") poll();
    };

    poll();
    document.addEventListener("visibilitychange", onVisibility);
    return () => {
      clearTimeout(timer);
      ctrl?.abort();
      document.removeEventListener("visibilitychange", onVisibility);
    };
  }, []);

  return data;
}
```

`cache: "no-store"` keeps each poll honest about the browser cache;
the `max-age=4` header is there for anything that sits in between.

### Quick checks from a terminal

```bash
curl -s https://api.ssharcade.dev/v1/players | jq .
curl -s https://api.ssharcade.dev/v1/players | jq '.players'
curl -s https://api.ssharcade.dev/v1/players | jq '.games[] | {id, players}'
curl -sI https://api.ssharcade.dev/v1/players   # headers: CORS, Cache-Control, Date
```

## Configuration

| Variable | Default | Meaning |
| --- | --- | --- |
| `ARCADE_STATS_PATH` | *(unset — off)* | File to write the snapshot to. Production: `/var/lib/arcade-stats/players.json`. |
| `ARCADE_STATS_INTERVAL` | `5s` | Rewrite interval; minimum `1s`. Published as `interval_seconds`, which clients use for staleness — change it and they adapt. |

Off by default so dev and tests write nothing. To try it locally:

```bash
ARCADE_LISTEN_PORT=2222 ARCADE_STATS_PATH=var/stats/players.json go run ./cmd/ssh-arcadelobby
watch -n1 cat var/stats/players.json     # then ssh -p 2222 localhost from another terminal
```

(`var/` is gitignored.)

## Deploy

`deploy/docker-compose.yml` and `deploy/Caddyfile` carry all of it:

- **`arcade-stats` volume** — tmpfs (size 1 MB, owned by uid 65532, mode
  0755). Mounted read-write in `router` at `/var/lib/arcade-stats`, read-only
  in `web` at `/srv/stats`. tmpfs because the data is worthless after a
  restart and rewritten every 5s: nothing to back up, no disk writes. The
  mount is shared and lives while either container uses it, so during a
  router-only redeploy the API keeps serving the router's final
  `"online": false` document until the new router's first write.
- **`api.ssharcade.dev` site block** in the Caddyfile — `/v1/players` is
  rewritten to exactly `players.json`, so nothing else in the volume
  (including the router's dot-named temp files mid-write) is reachable, and
  every other path is a `404`.
- **Permissions** — the router writes the file `0644`. That matters: Caddy
  runs as root with every capability dropped, so without `CAP_DAC_OVERRIDE`
  it gets ordinary permission checks and needs the world-read bit.

The one-time host steps — the DNS record (which **must** exist before the
Caddyfile change reaches the host) and the deploy commands — are in
`deploy/README.md` § "Live player-count API".

## Acceptance criteria

- `internal/presence` tests: unique-key counting across multiple sessions,
  lobby/game transitions, concurrent use under `-race`, snapshot field
  names (the public contract), world-readable file mode, atomic write with
  no temp files left behind, offline document on `Close`.
- `internal/server/presence_test.go`: one player walked through menu →
  bridged game → menu → quit, with the tally checked at each step; a
  session rejected by the per-key cap is never counted.
- On the host: `curl https://api.ssharcade.dev/v1/players` returns `200`
  JSON with `"online": true`; `players` rises when you `ssh ssharcade.dev`
  and falls when you quit; `docker compose stop router` flips it to
  `"online": false` within the stop grace period.
