# Publishing a release

Merging into `main` runs CI. Production changes only when a non-prerelease
GitHub Release named `vMAJOR.MINOR.PATCH` is published. The tagged commit must
be reachable from `main`. Each repository has its own independent release sequence.
Do not move or reuse published tags; fix a release with a new patch version.
Keep **Release immutability** enabled in GitHub repository settings so GitHub
locks new published release tags and assets. Use **Generate release notes**
in the draft and add a sentence if there's anything players or operators should know.

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

Publishing a tag alone does not deploy. Drafts and prereleases do not deploy.
The release workflow builds one candidate image by digest. It boots the image
with disposable keys and a fake game, checks the SSH banner, scans the image,
and signs the build provenance. The scan reports high and critical findings
and blocks promotion when a critical finding has a fix available.

The same digest gets the `vX.Y.Z`, commit, and `latest` tags after those checks.
`production` is updated only after SSM confirms the router is serving the
expected version. Use `latest` for the newest approved image and `production`
for the last deployment this workflow confirmed.

```sh
image=ghcr.io/mynameis-nigel/ssh-arcadelobby
digest=$(docker buildx imagetools inspect "$image:production" --format '{{json .Manifest}}' | jq -er .digest)
gh attestation verify "oci://$image@$digest" --repo MyNameIs-Nigel/ssh-arcadelobby
docker buildx imagetools inspect "$image@$digest" --format '{{json .SBOM}}'
```

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
The Actions summary has the config revision, previous recorded image, candidate
digest, banner verification, and rollback outcome when SSM reports one. If
SSM succeeds but the `production` tag update fails, the summary shows both
outcomes. Confirm the running digest before repairing the tag.
This is an application/configuration rollback, not a database rollback: releases
with incompatible data migrations require a separate recovery plan.

For an intentional rollback, invoke the same script via SSM with `SERVICE`,
`IMAGE=ghcr.io/mynameis-nigel/<repo>@sha256:<previous-digest>`, and the matching
numeric `VERSION`. Router rollback also requires `COMPOSE_B64`, `GAMES_B64`,
and `CADDY_B64` from the previous release. Use the release's recorded digest;
do not rebuild an old tag or move it. The host must have `flock`, Docker Compose,
and the normal private configuration already installed.
After confirming a manual rollback, point the registry record at the restored
image so it matches what is serving players:

```sh
docker buildx imagetools create --prefer-index=false --tag "$image:production" "$image@$digest"
```

Use the confirmed rollback digest in that command and authenticate to GHCR
with package write access.

## Validation

Run `scripts/ci/verify.sh` with Go, Python 3.11+, actionlint, ShellCheck,
and Docker with Compose and Linux amd64 build support. Set `PYTHON` if your
`python3` is older. CI and Release share this gate, including the Windows
SSH and PTY subset (`bash scripts/ci/verify.sh windows`).

The deployment tests use a Docker substitute to exercise digest selection,
service isolation, and rollback. The fleet checks validate Compose, the game
registry, and Caddy before building and smoke-testing an unpublished Linux
image. No AWS credentials are needed for verification.

### GitHub merge gate

Before enabling PR CI on the default branch, review the fork-run approval
setting under **Settings → Actions → General**. Select **Require approval for
all external contributors** for maintainer approval of outside workflow runs.
In a ruleset targeting `main`, require a pull request and the GitHub Actions
check named **CI gate** after it has run successfully.
