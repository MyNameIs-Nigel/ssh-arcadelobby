#!/usr/bin/env bash
# Exit 3 for fixable critical findings, 1 for a scanner error.
set -euo pipefail

image="${1:?Usage: scan-image.sh IMAGE@DIGEST REPORT.json}"
report="${2:?Usage: scan-image.sh IMAGE@DIGEST REPORT.json}"
if [[ ! "$image" =~ @sha256:[a-f0-9]{64}$ ]]; then
  echo "Expected an image pinned by digest: $image" >&2
  exit 1
fi

partial=$(mktemp "$report.XXXXXX")
trap 'rm -f "$partial"' EXIT

cache=()
if [[ -n "${TRIVY_CACHE_DIR:-}" ]]; then
  cache=(--user "$(id -u):$(id -g)" --env TRIVY_CACHE_DIR=/cache
    --mount "type=bind,src=$TRIVY_CACHE_DIR,dst=/cache")
fi

if ! docker run --rm "${cache[@]}" \
  ghcr.io/aquasecurity/trivy@sha256:8f6bb947dbc528902e11f2f5be52f7b519f6a39d5d148f41166f2040499768fb \
  image --image-src remote --platform linux/amd64 --scanners vuln --no-progress \
  --severity HIGH,CRITICAL --format json "$image" > "$partial"; then
  echo "Trivy failed to scan $image." >&2
  exit 1
fi

if ! jq -e '.Results | type == "array" and length > 0' "$partial" >/dev/null; then
  echo "Trivy returned no scan results for $image." >&2
  exit 1
fi

mv "$partial" "$report"
jq -r '.Results[]?.Vulnerabilities[]? |
  [.Severity, .VulnerabilityID, .PkgName, .InstalledVersion, (.FixedVersion // "")] |
  @tsv' "$report"

if ! jq -e 'all(.Results[]?.Vulnerabilities[]?; .Severity != "CRITICAL" or (.FixedVersion // "") == "")' \
  "$report" >/dev/null; then
  echo "Fixable critical vulnerabilities found in $image." >&2
  exit 3
fi
