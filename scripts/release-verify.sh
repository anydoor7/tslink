#!/usr/bin/env bash
#
# release-verify.sh — local release readiness checklist. MAINTAINER-ONLY.
#
# The Phase 3 gates are external and can never pass locally, so a nonzero exit
# from this script is its designed outcome, not a broken checkout.
#
# This script is intentionally read-only. It verifies local toolchain,
# configuration, and generated snapshot assets, then names the external gates
# that still require hosted/scratch readback before the first public release.
# Unknown external gates make the script exit nonzero, so it cannot be used as a
# fake publish proof.
#
# Usage:
#   scripts/release-verify.sh                       # dry-run gate report (default)
#   scripts/release-verify.sh --dist dist           # verify a local snapshot's assets
#   scripts/release-verify.sh --cleanup --tag vX     # plan partial-publication cleanup (dry-run)

set -euo pipefail

CLEANUP=0
DIST_DIR="dist"
TAG=""

log()   { printf '%s\n' "$*"; }
gate_pass=(); gate_fail=(); gate_unknown=()
pass()    { gate_pass+=("$1");    log "  [PASS]    $1"; }
fail()    { gate_fail+=("$1");    log "  [BLOCKED] $1"; }
unknown() { gate_unknown+=("$1"); log "  [UNKNOWN] $1 (external readback required)"; }

while [ $# -gt 0 ]; do
  case "$1" in
    --cleanup) CLEANUP=1 ;;
    --dist) DIST_DIR="${2:-dist}"; shift ;;
    --tag) TAG="${2:-}"; shift ;;
    -h|--help) grep '^#' "$0" | sed 's/^# \{0,1\}//'; exit 0 ;;
    *) log "unknown argument: $1"; exit 2 ;;
  esac
  shift
done

log "READ-ONLY mode: local gate report; no external mutation will occur."
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
  # Every archive must carry the licence files and project documents.
  required_license=(LICENSE NOTICE THIRD_PARTY_NOTICES.md)
  bundled_docs=(COMMERCIAL.md COMMERCIAL_zh.md)
  # With no archives the loop checks nothing: bash >= 4.4 would report a
  # vacuous pass, and bash 3.2 dies on the empty array under set -u.
  if [ "${#archives[@]}" -gt 0 ]; then
    docs_ok=1
    for a in "${archives[@]}"; do
      case "$a" in
        *.tar.gz) listing="$(tar tzf "$a")" ;;
        *.zip)    listing="$(unzip -Z1 "$a" 2>/dev/null || true)" ;;
      esac
      for f in "${required_license[@]}" "${bundled_docs[@]}"; do
        printf '%s\n' "$listing" | awk -F/ -v file="$f" '$NF == file { found=1 } END { exit !found }' || { docs_ok=0; log "    missing $f in $(basename "$a")"; }
      done
    done
    [ "$docs_ok" -eq 1 ] && pass "licence files and project documents present in every archive" || fail "a licence file or project document is missing from some archive"
  else
    fail "no archives to check for licence files and project documents"
  fi
  # Sidecars for signing/SBOM must exist before we can verify signatures.
  sboms=("$DIST_DIR"/*.sbom.json)
  [ "${#sboms[@]}" -ge 1 ] && pass "SBOM sidecars present (${#sboms[@]})" || unknown "SBOM sidecars absent (snapshot ran with --skip=sbom?)"
else
  fail "dist dir '$DIST_DIR' not found (run: goreleaser release --snapshot --clean --skip=publish,sign)"
fi
log ""

# --- Phase 3: external gates (never satisfiable in dry-run) ------------------
log "Phase 3 — external publish/sign/attest/install gates"
unknown "scratch publish readback in a disposable repo"
unknown "Homebrew tap initialized, protected, and writable by release token"
unknown "cosign signing identity + signature verification"
unknown "GitHub build-provenance attestation verifies for every digest"
unknown "download released assets + checksum/SBOM + tamper-negative"
unknown "native install/uninstall on macOS/Linux/Windows (Gatekeeper/quarantine)"
unknown "partial-publication cleanup verified on injected mid-publish failure"
log ""

# --- Phase 4: cleanup planning (dry-run) ------------------------------------
if [ "$CLEANUP" -eq 1 ]; then
  log "Phase 4 — partial-publication cleanup plan for tag '${TAG:-<unset>}'"
  if [ -z "$TAG" ]; then fail "cleanup requires --tag"; else
    log "  would (external scratch run): delete draft release, remove uploaded assets,"
    log "  revert tap commit, and confirm no orphaned signatures/attestations for $TAG"
    unknown "cleanup execution in an external disposable repository"
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
