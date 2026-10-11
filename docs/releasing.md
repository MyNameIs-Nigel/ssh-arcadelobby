# Publishing a release

Merging into `main` runs CI. Production changes only when a non-prerelease
GitHub Release named `vMAJOR.MINOR.PATCH` is published. The tagged commit must
be reachable from `main`. Each repository has its own independent release sequence.
Do not move or reuse published tags; fix a release with a new patch version.
Protect release tags against updates/deletion with a GitHub ruleset.

The tag is the only release version input. Docker passes its numeric portion
through Go linker flags to `internal/version.Version`. Local builds use `dev`.
The help/about screens and SSH banner use that same value; the lobby reads the
running game's banner. There are no alpha/beta channels encoded in versions.

## GitHub setup (operator)

The existing `production` environment must allow **tags** matching `v*`.
Remove the `main` deployment branch allowance after switching to releases.
Retain any desired required reviewers and the existing `ARCADE_DEPLOY_ROLE`
and `ARCADE_INSTANCE_ID` variables. The AWS OIDC trust must continue to allow
this repository's `production` environment subject. No environment or IAM
settings are changed by this PR.

## First rollout

1. Merge the release changes in all four repositories.
2. Configure the production environment's tag policy in each repository.
3. Publish the **lobby release first**. It installs the Compose image variables
   needed by the game releases and preserves GAMBIT's warning with
   `alpha_warning = true` in the games registry.
4. Publish each game's release. Check the Release workflow's success, then
   verify the version in the lobby and game help screen.

Publishing a tag alone does not deploy. Drafts and releases marked as prereleases
do not deploy. Failed validation/tests prevent publishing and deployment.
Published images get a `vX.Y.Z` tag, a commit tag, and `latest`; production uses
the image digest returned by that build. A failed deployment can therefore
leave a published release/image available without it being the running version.

## Deployment and recovery

SSM runs `scripts/deploy/remote-deploy.sh` on the host. A host-wide `flock`
serializes all four repositories. It persists the selected digest in
`/srv/ssharcade/.env` using `ROUTER_IMAGE`, `FARM_IMAGE`, `MOONMINER_IMAGE`, or
`CHESS_IMAGE`. Other entries are preserved and never printed. The lobby syncs
Compose, games.toml, and Caddyfile inside the same lock; banner.toml remains
operator-owned. Compose defaults to `latest` only for services not yet migrated.

The script verifies the running SSH banner against the release version and
restores the previous image/configuration on failure. Its exit status remains
nonzero after rollback so a recovered deployment is still reported as failed.
This is an application/configuration rollback, not a database rollback: releases
with incompatible data migrations require a separate recovery plan.

For an intentional rollback, invoke the same script via SSM with `SERVICE`,
`IMAGE=ghcr.io/mynameis-nigel/<repo>@sha256:<previous-digest>`, and the matching
numeric `VERSION`. Router rollback also requires `COMPOSE_B64`, `GAMES_B64`,
and `CADDY_B64` from the previous release. Use the release's recorded digest;
do not rebuild an old tag or move it. The host must have `flock`, Docker Compose,
and the normal private configuration already installed.

## Validation

Run `go build ./...`, `go vet ./...`, `go test -race ./...`, and
`python3 scripts/deploy/test_deploy.py`. The deployment tests use a Docker
substitute to cover digest selection, unrelated-service preservation, startup
failure/rollback, pull failure, old Compose rejection, and router config recovery.
They do not deploy to AWS. Validate workflows with actionlint.
