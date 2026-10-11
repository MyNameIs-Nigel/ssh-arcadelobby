# Guest access implementation bug tally

Count distinct application defects verified and fixed during implementation, including
reviewed guest regressions. Exclude feature gaps and transient development/test setup errors. Severity reflects
impact in this fleet. No production changes are performed by this work.

| Severity | Defect | Evidence and fix | Repositories |
| --- | --- | --- | --- |
| MODERATE | A replaced Farm session can still read/advance/mutate the actor until detach; rename also touches its database budget before checking ownership. | Actor ownership checks in read/intent; rename gate executes after ownership validation. Regression test replaces a session and attempts both actions. | Farm |
| MODERATE | Per-shell attachment/TUI values are stored in a transport-wide SSH context and can overwrite another channel's values. | Add channel-local context wrapper and verify two shells over the same context remain independent. | Farm, Moon Miner, Chess; Lobby also uses the wrapper for new channel-local guest identity |
| LIGHT | Generic layout tests depend on the calendar and unexpectedly activate seasonal decoration. | Fixed off-season timestamps, plus separate explicit seasonal geometry tests. | Farm |
| LIGHT | Seasonal sky rows omit right padding, making the canvas ragged. | Pad the complete row; assert every seasonal canvas row has the expected width. | Farm |
| LIGHT | Crop picker content exceeds the stock-size modal's width. | Bound content to the overlay text width; existing overflow regression now passes. | Farm |
| LIGHT | Moon Miner tweaks wheel selects a hidden development-only row in production. | Use the same visible-row traversal as keyboard navigation; regression checks the first wheel step. | Moon Miner |
| MODERATE | Farm build pins Go/Litestream dependencies with reachable security advisories, blocking the PR supply-chain gate. | CI reported 12 upstream advisories; bump Go to 1.26.9, x/net to 0.60.0 and OpenTelemetry to 1.45.0. Litestream builds and govulncheck reports zero affected vulnerabilities. Counted once as a build-dependency defect, not 12 application defects. | Farm |

| MODERATE | Alpha continuation and final bridging use stale game records after registry reload. | Re-resolve by stable game ID on all activation paths, alpha continuation and immediately before bridge. Regressions cover removal, offline, policy change and address change. | Lobby |
| MODERATE | SSH host-key/auth/channel rejection is treated as an unreachable game and poisons shared health. | Separate access errors from TCP/stalled-handshake errors; real SSH regression keeps a healthy pinned-key-mismatch backend online. | Lobby |
| LIGHT | Unexpected game loss promises progress was saved even after a crash. | Remove the unsupported guarantee and show guest discard copy. | Lobby |

| LIGHT | Ctrl+C replaces a kicked guest's terminal notice with a cancelable discard dialog. | Regression fails before the fix; preserve the terminal kick state and quit immediately. Found in Moon Miner review and verified in Farm too. This was introduced by the new guest confirmation flow. | Farm, Moon Miner |

Current total: **SEVERE 0 · MODERATE 5 · LIGHT 6**. Updated after each repository.
