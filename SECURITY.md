# Security policy

## Reporting a vulnerability

Please report security problems privately through GitHub:
open the repository's **Security** tab and choose **Report a vulnerability**.
That creates a private advisory only the maintainer can see.

Please do not open a public issue, pull request or discussion for a
vulnerability until a fix has shipped.

A useful report says what is affected, how to reproduce it, and what an
attacker could do with it. You should hear back within a week.

## What is in scope

This repository is the SSH Arcade lobby, part of the ssharcade fleet that runs at
`ssh ssharcade.dev`. Reports are welcome about:

- the SSH server, authentication and the identity a public key resolves to
- anything that lets one player read or change another player's data
- the trusted-proxy hand-off between the arcade lobby and the games
- the container image, the release pipeline and the deploy scripts

Denial of service by simply opening many connections is a known limit of a
single small host and is handled by rate limits rather than treated as a
vulnerability, unless a small amount of traffic has an outsized effect.

## Supported versions

Only the version running in production, which is the latest GitHub release,
receives fixes.
