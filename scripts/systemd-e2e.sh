#!/usr/bin/env bash
# Real-systemd end-to-end check for `tslink install`, with a mutation control.
#
# Runs on the host that has the OrbStack CLI (`orb`); everything that touches
# systemd runs inside a disposable OrbStack Linux VM. It exercises the Round
# C-2 defect: `install` used to report success while the freshly restarted
# unit was already in systemd's restart loop.
#
# Why a script and not a CI gate: GitHub's ubuntu-latest has no reliable
# `systemctl --user` session, and a permanently red job is worse than no job.
#
# Why a mutation control: a happy-path-only run stays green forever and proves
# nothing. From the SAME tree this script also builds a binary whose verify is
# blind (the pre-fix shape) and REQUIRES the e2e to fail against it, on the
# exact sentinel line the test emits for "install claimed success while the
# unit is crash-looping". A failure for any other reason is INCONCLUSIVE, for
# the fixed run as well as for the controls.
#
# Usage:
#   scripts/systemd-e2e.sh                # VM defaults to tslink-cold2
#   TSLINK_E2E_VM=<name> scripts/systemd-e2e.sh
#   TSLINK_E2E_KEEP=1 ...                 # keep host work dir + VM dir + logs
#   TSLINK_E2E_PREFIX_BAD_BIN=/abs/in/vm  # optional: a pre-fix bad build already
#                                         # inside the VM; the e2e must fail on it too
#
# Exit codes: 0 verdict OK; 1 verdict FAILED or a build/transfer step failed
# (see the last lines); 2 preflight (environment not ready, nothing was built);
# 3 the working tree changed underneath the build.
#
# One run per VM at a time: tslink.service is a per-user singleton, so two
# concurrent runs on the same VM would either trip the test's refuse gate or
# interleave on the same unit.
#
# The working tree is never modified: mutants are built with `go build -overlay`.
set -euo pipefail

VM="${TSLINK_E2E_VM:-tslink-cold2}"
KEEP="${TSLINK_E2E_KEEP:-0}"
PREFIX_BAD_BIN="${TSLINK_E2E_PREFIX_BAD_BIN:-}"
SENTINEL='E2E_SENTINEL: install claimed success while the unit is crash-looping'
TEST_NAME='TestSystemdInstallE2E'
TEST_FILE='cmd/install_linux_e2e_test.go'

REPO="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"

# --- 0. preflight (exit 2, nothing built) -----------------------------------
for tool in orb go python3; do
  command -v "$tool" >/dev/null 2>&1 || { echo "preflight: $tool not found on PATH" >&2; exit 2; }
done
# The sentinel is matched as a substring of the test log; a drift between this
# file and the Go constant would turn every control into INCONCLUSIVE.
grep -Fq "$SENTINEL" "$REPO/$TEST_FILE" || { echo "preflight: sentinel text not found in $TEST_FILE (drift?)" >&2; exit 2; }
# Liveness is "can it run a command", not a parse of `orb list` (whose column
# layout differs between TTY and pipe).
if ! orb -m "$VM" true >/dev/null 2>&1; then
  echo "preflight: VM '${VM}' is unreachable (see: orb list)" >&2; exit 2
fi
unit_state="$(orb -m "$VM" systemctl --user show tslink.service --property=ActiveState 2>/dev/null || true)"
case "$unit_state" in
  ActiveState=active|ActiveState=activating|ActiveState=reloading|ActiveState=deactivating)
    echo "preflight: tslink.service is ${unit_state#ActiveState=} on '${VM}'; the e2e would refuse. Stop it first (systemctl --user stop tslink.service) if this VM is disposable" >&2
    exit 2 ;;
esac

vm_arch="$(orb -m "$VM" uname -m)"
case "$vm_arch" in
  aarch64|arm64) GOARCH=arm64 ;;
  x86_64|amd64)  GOARCH=amd64 ;;
  *) echo "preflight: unsupported VM arch: $vm_arch" >&2; exit 2 ;;
esac
VMUSER="$(orb -m "$VM" id -un)"
VMDIR="/home/${VMUSER}/tslink-systemd-e2e.$$"

# Host work dir must live on a path the VM sees under the SAME name.
# OrbStack shares the macOS filesystem; /tmp inside the VM is the VM's own, so
# on macOS use /private/tmp explicitly. Note that macOS prunes /private/tmp
# entries older than a few days: kept evidence is not permanent.
if [ "$(uname -s)" = Darwin ]; then
  WORK="$(mktemp -d /private/tmp/tslink-systemd-e2e.XXXXXX)"
else
  WORK="$(mktemp -d /tmp/tslink-systemd-e2e.XXXXXX)"
fi
status=1
cleanup() {
  if [ "$KEEP" = 1 ] || [ "$status" -ne 0 ]; then
    echo "kept host work dir: $WORK"
    echo "kept VM dir:        $VM:$VMDIR"
  else
    rm -rf -- "$WORK"
    orb -m "$VM" rm -rf -- "$VMDIR" >/dev/null 2>&1 || true
  fi
}
trap cleanup EXIT

# --- 1. mutants (overlay sources) ------------------------------------------
cat > "$WORK/badserve.go" <<'GO'
package cmd

import "os"

// e2e mutant: `serve` dies at once, so systemd puts the unit in its restart loop.
func init() {
	if len(os.Args) > 1 && os.Args[1] == "serve" {
		os.Exit(93)
	}
}
GO

# blind verify = the pre-fix shape: report "running" without observing systemd.
# The overlay JSON is emitted by json.dumps so odd characters in paths survive.
python3 - "$REPO" "$WORK" <<'PY'
import json, os, sys
repo, work = sys.argv[1], sys.argv[2]
src_path = os.path.join(repo, "cmd", "install_linux.go")
src = open(src_path, encoding="utf-8").read()
anchor = "func verifySystemdServiceRunning() (bool, error) {\n"
n = src.count(anchor)
if n != 1:
    sys.exit(f"blind mutant: anchor found {n} times, want 1 (install_linux.go changed shape?)")
blind = src.replace(anchor, anchor + "\tif os.Getenv(\"TSLINK_E2E_BLIND_VERIFY\") == \"1\" {\n\t\treturn false, nil // e2e mutant: blind verify\n\t}\n")
open(os.path.join(work, "install_linux_blind.go"), "w", encoding="utf-8").write(blind)
bad_added = os.path.join(repo, "cmd", "zz_e2e_badserve.go")
json.dump({"Replace": {bad_added: os.path.join(work, "badserve.go")}},
          open(os.path.join(work, "ov_bad.json"), "w"))
json.dump({"Replace": {bad_added: os.path.join(work, "badserve.go"),
                       src_path: os.path.join(work, "install_linux_blind.go")}},
          open(os.path.join(work, "ov_blind.json"), "w"))
PY

# --- 2. build from this tree -------------------------------------------------
before="$(shasum -a 256 "$REPO/cmd/install_linux.go" "$REPO/cmd/serve.go")"
build() { ( cd "$REPO" && GOOS=linux GOARCH="$GOARCH" CGO_ENABLED=0 go build -trimpath "$@" ); }
echo "== building linux/$GOARCH: good, bad, blind, cmd.test"
build -o "$WORK/tslink-good" .
build -overlay "$WORK/ov_bad.json"   -o "$WORK/tslink-bad" .
build -overlay "$WORK/ov_blind.json" -o "$WORK/tslink-blind" .
( cd "$REPO" && GOOS=linux GOARCH="$GOARCH" CGO_ENABLED=0 go test -c -o "$WORK/cmd.test" ./cmd )
after="$(shasum -a 256 "$REPO/cmd/install_linux.go" "$REPO/cmd/serve.go")"
[ "$before" = "$after" ] || { echo "working tree changed during build; refusing to continue" >&2; exit 3; }

# --- 3. ship into the VM's own disk -----------------------------------------
orb -m "$VM" mkdir -p "$VMDIR"
for f in tslink-good tslink-bad tslink-blind cmd.test; do
  orb -m "$VM" cp "$WORK/$f" "$VMDIR/$f"
  orb -m "$VM" chmod 0755 "$VMDIR/$f"
done

# --- 4. run ------------------------------------------------------------------
run_e2e() { # label, bad-bin-in-vm, extra env...
  local label="$1" bad="$2"; shift 2
  local log="$WORK/$label.log"
  set +e
  orb -m "$VM" env ${@+"$@"} TSLINK_SYSTEMD_E2E=1 \
    TSLINK_SYSTEMD_E2E_GOOD_BIN="$VMDIR/tslink-good" \
    TSLINK_SYSTEMD_E2E_BAD_BIN="$bad" \
    "$VMDIR/cmd.test" -test.run "^${TEST_NAME}\$" -test.v -test.count=1 >"$log" 2>&1
  local rc=$?
  set -e
  echo "$rc"
}
sentinel_in() { grep -Fq "$SENTINEL" "$WORK/$1.log" && echo yes || echo no; }
passed_in()   { grep -Eq "^--- PASS: ${TEST_NAME} " "$WORK/$1.log" && echo yes || echo no; }
# The test refuses hosts that look like real installations (running unit,
# foreign unit file, stored credential) and fails closed on a missing user
# manager; those are environment outcomes, not verdicts about the code.
refused_in()  { grep -Eq 'refusing:|no reachable systemd user manager|must be an absolute path' "$WORK/$1.log" && echo yes || echo no; }

echo "== run 1: fixed tree, bad serve            (expect: PASS)"
rc_fixed="$(run_e2e fixed "$VMDIR/tslink-bad")"
echo "== run 2: blind verify, bad serve          (expect: FAIL on sentinel)"
rc_blind="$(run_e2e blind "$VMDIR/tslink-blind" TSLINK_E2E_BLIND_VERIFY=1)"
rc_prefix=""
if [ -n "$PREFIX_BAD_BIN" ]; then
  echo "== run 3: pre-fix bad build $PREFIX_BAD_BIN (expect: FAIL on sentinel)"
  rc_prefix="$(run_e2e prefix "$PREFIX_BAD_BIN")"
fi

# --- 5. verdict --------------------------------------------------------------
ok=1
verdict() { # name label rc want
  local name="$1" label="$2" rc="$3" want="$4" res sent passed refused
  sent="$(sentinel_in "$label")"; passed="$(passed_in "$label")"; refused="$(refused_in "$label")"
  if [ "$refused" = yes ]; then
    res="INCONCLUSIVE (environment: the e2e refused to run, see log)"; ok=0
  else
    case "$want" in
      pass)
        if [ "$rc" -eq 0 ] && [ "$passed" = yes ]; then res=PASS
        elif [ "$rc" -eq 0 ]; then res="INCONCLUSIVE (rc 0 but no '--- PASS' line: skipped?)"; ok=0
        else res=FAIL; ok=0; fi ;;
      fail_on_sentinel)
        if [ "$rc" -ne 0 ] && [ "$sent" = yes ]; then res="PASS (e2e failed on the sentinel, as required)"
        elif [ "$rc" -ne 0 ]; then res="INCONCLUSIVE (e2e failed, but not on the sentinel)"; ok=0
        else res="FAIL (e2e passed against a blind verify: it cannot see the defect)"; ok=0; fi ;;
    esac
  fi
  printf '%-36s rc=%-3s sentinel=%-3s -> %s\n' "$name" "$rc" "$sent" "$res"
}
echo; echo "== verdict (VM=$VM linux/$GOARCH)"
verdict "fixed tree + bad serve"              fixed  "$rc_fixed" pass
verdict "blind verify + bad serve (control)"  blind  "$rc_blind" fail_on_sentinel
if [ -n "$PREFIX_BAD_BIN" ]; then
  verdict "pre-fix bad build (control)"      prefix "$rc_prefix" fail_on_sentinel
fi
echo "logs: $WORK/*.log"
if [ "$ok" -eq 1 ]; then status=0; echo "RESULT: OK"; else status=1; echo "RESULT: FAILED"; fi
exit "$status"
