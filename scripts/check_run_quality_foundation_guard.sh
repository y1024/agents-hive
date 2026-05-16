#!/usr/bin/env bash
set -euo pipefail

ROOT="${REPO_ROOT:-$(cd "$(dirname "$0")/.." && pwd)}"
cd "$ROOT"

if [[ -d internal/run ]]; then
  echo "PROHIBITED: internal/run must not be introduced without a Run ADR" >&2
  exit 1
fi

if [[ -d internal/capability ]]; then
  echo "PROHIBITED: internal/capability must not be introduced; use internal/router" >&2
  exit 1
fi

if rg -n '^[[:space:]]*-[[:space:]]+(\[[ xX]\][[:space:]]+)?Create(/Modify)?:[[:space:]]*`?internal/(run|capability)\b|RunDrawer[[:space:]]+首发|DomainPack Registry[[:space:]]+首发' docs >/tmp/run_quality_guard_docs.txt 2>/dev/null; then
  cat /tmp/run_quality_guard_docs.txt >&2
  echo "PROHIBITED: docs must not plan first-class parallel Run/Capability systems" >&2
  exit 1
fi

if rg -n 'Reason:\s*string\(raw\)' internal/master >/tmp/run_quality_guard_raw_reason.txt 2>/dev/null; then
  cat /tmp/run_quality_guard_raw_reason.txt >&2
  echo "PROHIBITED: quality journal decisions must not persist raw quality JSON" >&2
  exit 1
fi

if ! rg -n 'func writeAdminQualityJSON|RedactSecrets\(' internal/api internal/security >/tmp/run_quality_guard_redaction.txt 2>/dev/null; then
  echo "PROHIBITED: admin quality responses must keep an explicit redaction boundary" >&2
  exit 1
fi
