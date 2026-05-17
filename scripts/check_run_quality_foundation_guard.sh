#!/usr/bin/env bash
set -euo pipefail

ROOT="${REPO_ROOT:-$(cd "$(dirname "$0")/.." && pwd)}"
cd "$ROOT"

search() {
  local pattern="$1"
  shift

  if command -v rg >/dev/null 2>&1; then
    rg -n "$pattern" "$@"
    return $?
  fi

  local paths=()
  local path
  for path in "$@"; do
    if [[ -e "$path" ]]; then
      paths+=("$path")
    fi
  done
  if [[ ${#paths[@]} -eq 0 ]]; then
    return 1
  fi
  grep -REn "$pattern" "${paths[@]}"
}

if [[ -d internal/run ]]; then
  echo "PROHIBITED: internal/run must not be introduced without a Run ADR" >&2
  exit 1
fi

if [[ -d internal/capability ]]; then
  echo "PROHIBITED: internal/capability must not be introduced; use internal/router" >&2
  exit 1
fi

if search '^[[:space:]]*-[[:space:]]+(\[[ xX]\][[:space:]]+)?Create(/Modify)?:[[:space:]]*`?internal/(run|capability)([^[:alnum:]_]|$)|RunDrawer[[:space:]]+首发|DomainPack Registry[[:space:]]+首发' docs >/tmp/run_quality_guard_docs.txt 2>/dev/null; then
  cat /tmp/run_quality_guard_docs.txt >&2
  echo "PROHIBITED: docs must not plan first-class parallel Run/Capability systems" >&2
  exit 1
fi

if search 'Reason:[[:space:]]*string\(raw\)' internal/master >/tmp/run_quality_guard_raw_reason.txt 2>/dev/null; then
  cat /tmp/run_quality_guard_raw_reason.txt >&2
  echo "PROHIBITED: quality journal decisions must not persist raw quality JSON" >&2
  exit 1
fi

if ! search 'func writeAdminQualityJSON|RedactSecrets\(' internal/api internal/security >/tmp/run_quality_guard_redaction.txt 2>/dev/null; then
  echo "PROHIBITED: admin quality responses must keep an explicit redaction boundary" >&2
  exit 1
fi
