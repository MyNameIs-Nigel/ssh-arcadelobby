#!/usr/bin/env bash
set -euo pipefail

report_dir="${1:?Usage: scan-published-images.sh REPORT_DIRECTORY}"
router='ghcr.io/mynameis-nigel/ssh-arcadelobby'
summary="${GITHUB_STEP_SUMMARY:-/dev/stdout}"
scanner="$(dirname "$0")/scan-image.sh"
failed=false

mkdir -p "$report_dir"
TRIVY_CACHE_DIR=$(mktemp -d)
export TRIVY_CACHE_DIR
trap 'rm -rf "$TRIVY_CACHE_DIR"' EXIT

resolve_router() {
  local manifest digest
  manifest=$(docker buildx imagetools inspect "$router:$1" --format '{{json .Manifest}}') || return 1
  digest=$(jq -er '.digest | select(test("^sha256:[a-f0-9]{64}$"))' <<< "$manifest") || return 1
  printf '%s@%s\n' "$router" "$digest"
}

summary_row() {
  printf '| %s | %s | %s | %s | %s |\n' "$@" >> "$summary"
}

scan_target() {
  local label="$1" image="$2" report_prefix="$3"
  local digest="${image##*@}" report status=0 high critical result
  report="$report_dir/$report_prefix-${digest#sha256:}.json"
  echo "Scanning $label ($image)"
  "$scanner" "$image" "$report" || status=$?

  case "$status" in
    0) result="Scanned" ;;
    3) result="Fixable critical findings"; failed=true ;;
    *)
      summary_row "$label" "\`$digest\`" "-" "-" "Scanner error"
      failed=true
      return
      ;;
  esac

  high=$(jq '[.Results[]?.Vulnerabilities[]? | select(.Severity == "HIGH")] | length' "$report")
  critical=$(jq '[.Results[]?.Vulnerabilities[]? | select(.Severity == "CRITICAL")] | length' "$report")
  summary_row "$label" "\`$digest\`" "$high" "$critical" "$result"
}

{
  echo '### Published image scan'
  echo
  echo 'Fixable critical findings fail the scan. High and unfixed critical findings are reported.'
  echo
  echo '| Image | Digest | High | Critical | Result |'
  echo '| --- | --- | ---: | ---: | --- |'
} >> "$summary"

production=''
latest=''
if resolved=$(resolve_router production 2>&1); then
  production="$resolved"
elif [[ "$resolved" == *"$router:production: not found"* ]]; then
  summary_row "Production router" "-" "-" "-" "No deployment digest recorded"
else
  echo "Could not resolve the production router: $resolved" >&2
  summary_row "Production router" "-" "-" "-" "Registry lookup error"
  failed=true
fi

if resolved=$(resolve_router latest 2>&1); then
  latest="$resolved"
else
  echo "Could not resolve the latest approved router: $resolved" >&2
  summary_row "Latest approved router" "-" "-" "-" "Registry lookup error"
  failed=true
fi

if [[ -n "$production" && "$production" == "$latest" ]]; then
  scan_target "Production and latest approved router" "$production" router
else
  if [[ -n "$production" ]]; then scan_target "Production router" "$production" router; fi
  if [[ -n "$latest" ]]; then scan_target "Latest approved router" "$latest" router; fi
fi

caddy="${CADDY_PRODUCTION_IMAGE:-}"
if [[ -z "$caddy" ]]; then
  summary_row "Caddy" "-" "-" "-" "No deployment digest recorded"
elif [[ "$caddy" =~ ^(docker\.io/library/)?caddy@sha256:[a-f0-9]{64}$ ]]; then
  scan_target "Production Caddy" "$caddy" caddy
else
  echo "ARCADE_CADDY_PRODUCTION_IMAGE must be caddy@sha256:<digest>." >&2
  summary_row "Caddy" "-" "-" "-" "Invalid deployment digest"
  failed=true
fi

[[ "$failed" == false ]]
