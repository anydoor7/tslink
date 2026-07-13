#!/usr/bin/env bash
#
# release-verify.sh — scratch/draft release verification playbook.
#
# This script proves the publish/tap/sign/attest/download/native-install path in
# a DISPOSABLE GitHub repo/tap BEFORE any public release. It defaults to
# dry-run/read-only and fails closed: any missing precondition (token, tap,
# signing identity, attestation, downloaded-artifact verification, native
# install) is reported as a BLOCKING gap and the script exits nonzero. It never
# performs external mutation unless invoked with --execute AND a scratch target,
# and it refuses to run --execute against the production repo/tap.
#
# Usage:
#   scripts/release-verify.sh                       # dry-run gate report (default)
#   scripts/release-verify.sh --dist dist           # verify a local snapshot's assets
#   scripts/release-verify.sh --cleanup --tag vX     # plan partial-publication cleanup (dry-run)
#   SCRATCH_REPO=owner/scratch SCRATCH_TAP=owner/scratch-tap \
#     scripts/release-verify.sh --execute --tag vX   # scratch-only, opt-in mutation
#
# Use dry-run mode for local validation.

set -euo pipefail

DRY_RUN=1
CLEANUP=0
DIST_DIR="dist"
TAG=""
PROD_REPO="monody0007/tslink"
PROD_TAP="monody0007/homebrew-tap"

log()   { printf '%s\n' "$*"; }
gate_pass=(); gate_fail=(); gate_unknown=()
pass()    { gate_pass+=("$1");    log "  [PASS]    $1"; }
fail()    { gate_fail+=("$1");    log "  [BLOCKED] $1"; }
unknown() { gate_unknown+=("$1"); log "  [UNKNOWN] $1 (external readback required)"; }

while [ $# -gt 0 ]; do
  case "$1" in
    --execute) DRY_RUN=0 ;;
    --cleanup) CLEANUP=1 ;;
    --dist) DIST_DIR="${2:-dist}"; shift ;;
    --tag) TAG="${2:-}"; shift ;;
    -h|--help) grep '^#' "$0" | sed 's/^# \{0,1\}//'; exit 0 ;;
    *) log "unknown argument: $1"; exit 2 ;;
  esac
  shift
done

# --- Refuse external mutation against production (fail closed) ---------------
if [ "$DRY_RUN" -eq 0 ]; then
  scratch_repo="${SCRATCH_REPO:-}"
  scratch_tap="${SCRATCH_TAP:-}"
  if [ -z "$scratch_repo" ] || [ -z "$scratch_tap" ]; then
    log "FATAL: --execute requires SCRATCH_REPO and SCRATCH_TAP (disposable targets)."
    exit 3
  fi
  if [ "$scratch_repo" = "$PROD_REPO" ] || [ "$scratch_tap" = "$PROD_TAP" ]; then
    log "FATAL: refusing to --execute against the production repo/tap."
    exit 3
  fi
  log "EXECUTE mode against scratch: repo=$scratch_repo tap=$scratch_tap"
else
  log "DRY-RUN mode: read-only gate report; no external mutation will occur."
fi
log ""

# --- Phase 1: local toolchain + config gates --------------------------------
log "Phase 1 — local preconditions"
for tool in go goreleaser cosign syft gh; do
  if command -v "$tool" >/dev/null 2>&1; then pass "tool present: $tool"; else fail "tool missing: $tool"; fi
done
if goreleaser check >/dev/null 2>&1; then pass "goreleaser config valid (no deprecated keys)"; else fail "goreleaser config invalid/deprecated"; fi
log ""

# --- Phase 2: local snapshot asset gates ------------------------------------
log "Phase 2 — snapshot asset completeness ($DIST_DIR)"
if [ -d "$DIST_DIR" ]; then
  shopt -s nullglob
  archives=("$DIST_DIR"/*.tar.gz "$DIST_DIR"/*.zip)
  packages=("$DIST_DIR"/*.deb "$DIST_DIR"/*.rpm)
  [ "${#archives[@]}" -ge 6 ] && pass "archives present (${#archives[@]})" || fail "expected >=6 archives, found ${#archives[@]}"
  [ "${#packages[@]}" -ge 2 ] && pass "linux packages present (${#packages[@]})" || fail "expected deb+rpm, found ${#packages[@]}"
  [ -f "$DIST_DIR/checksums.txt" ] && pass "checksums.txt present" || fail "checksums.txt missing"
  # Every archive must carry the legal payload.
  legal_ok=1
  for a in "${archives[@]}"; do
    case "$a" in
      *.tar.gz) listing="$(tar tzf "$a")" ;;
      *.zip)    listing="$(unzip -Z1 "$a" 2>/dev/null || true)" ;;
    esac
    for f in LICENSE NOTICE THIRD_PARTY_NOTICES.md; do
      printf '%s\n' "$listing" | grep -q "$f" || { legal_ok=0; log "    missing $f in $(basename "$a")"; }
    done
  done
  [ "$legal_ok" -eq 1 ] && pass "LICENSE/NOTICE/THIRD_PARTY in every archive" || fail "legal payload missing in some archive"
  # Sidecars for signing/SBOM must exist before we can verify signatures.
  sboms=("$DIST_DIR"/*.sbom.json)
  [ "${#sboms[@]}" -ge 1 ] && pass "SBOM sidecars present (${#sboms[@]})" || unknown "SBOM sidecars absent (snapshot ran with --skip=sbom?)"
else
  fail "dist dir '$DIST_DIR' not found (run: goreleaser release --snapshot --clean --skip=publish,sign)"
fi
log ""

# --- Phase 3: external gates (never satisfiable in dry-run) ------------------
log "Phase 3 — external publish/sign/attest/install gates"
if [ "$DRY_RUN" -eq 1 ]; then
  unknown "publish to scratch repo (needs --execute + SCRATCH_REPO)"
  unknown "homebrew tap initialized + protected"
  unknown "cosign signing identity + signature verification"
  unknown "GitHub build-provenance attestation verifies for every digest"
  unknown "download released assets + checksum/SBOM + tamper-negative"
  unknown "native install/uninstall on macOS/Linux/Windows (Gatekeeper/quarantine)"
  unknown "partial-publication cleanup verified on injected mid-publish failure"
else
  # Execute-mode steps would run here against the SCRATCH targets only. They are
  # intentionally not implemented here; external mutation requires a separate target.
  fail "execute-mode external steps are unavailable in this build"
fi
log ""

# --- Phase 4: cleanup planning (dry-run) ------------------------------------
if [ "$CLEANUP" -eq 1 ]; then
  log "Phase 4 — partial-publication cleanup plan for tag '${TAG:-<unset>}'"
  if [ -z "$TAG" ]; then fail "cleanup requires --tag"; else
    log "  would (scratch-only, on --execute): delete draft release, remove uploaded assets,"
    log "  revert tap commit, and confirm no orphaned signatures/attestations for $TAG"
    unknown "cleanup execution (scratch-only, requires --execute)"
  fi
  log ""
fi

# --- Summary: fail closed ----------------------------------------------------
log "Summary: ${#gate_pass[@]} pass, ${#gate_fail[@]} blocked, ${#gate_unknown[@]} unknown(external)"
if [ "${#gate_fail[@]}" -gt 0 ]; then
  log "RESULT: NOT VERIFIED (blocking gaps present)."
  exit 1
fi
if [ "${#gate_unknown[@]}" -gt 0 ]; then
  log "RESULT: LOCAL CHECKS PASS; external readback still required (fail-closed: not verified)."
  exit 1
fi
log "RESULT: VERIFIED."
