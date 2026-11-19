# Working on ssh-arcadelobby

This is the public SSH entry point for ssharcade. The router presents a terminal menu, checks game availability, and bridges a player's terminal to the selected private game server. A game disconnect can return the player to the menu on the same connection.

## Where to work

| Path | Responsibility |
| --- | --- |
| `cmd/ssh-arcadelobby/`, `internal/config/`, `internal/server/` | Bootstrap, configuration, SSH listener, session handling, and rate limits |
| `internal/lobby/`, `internal/banner/`, `internal/presence/` | Menu, operator notices, player presence, and terminal RTT in About |
| `internal/bridge/`, `internal/proxyproto/`, `internal/identity/` | SSH handoff and trusted forwarding of player identity |
| `internal/registry/` | Registry reload and game health checks |
| `deploy/docker-compose.yml`, `deploy/games.toml` | Fleet topology and production menu entries |
| `docs/`, `deploy/README.md` | Design contracts and operational instructions |

Start with [the docs index](docs/README.md). Read [the bridge and identity protocol](docs/02-bridge-and-identity-protocol.md) before changing routing or authentication. When changing fleet configuration, check both the Compose service and its registry address. A registered game with no running replica should appear offline.

## Tools and verification

- Run `go build ./...`, `go vet ./...`, and `go test ./...` with the Go version in `go.mod`. Use `go test -race ./internal/server ./internal/bridge ./internal/registry` for session lifecycle or concurrency changes.
- Use `scripts/dev-local-fleet.sh` with the sibling repos for real lobby-to-game flows. Use the isolated loopback setup in the [terminal tools guide](scripts/tui-tools/README.md) for menu-only work.
- Use `scene.json` to capture the About screen and `handoff.json` with a running local Farm to verify launch and return. Render PNGs for layout, GIFs for transitions, and add latency with `latency.mjs` for timing investigations.
- For fleet changes, run `docker compose -f deploy/docker-compose.yml config --format json` and pass the result and `deploy/games.toml` to `scripts/ci/check-fleet.py`. Use a Compose dry run to verify which containers will start.
- Work on a branch and open a PR against `main`. Publishing a release can deploy to production. Check the workflow and deployment runbook before any release work.
