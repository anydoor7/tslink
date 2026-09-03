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
# unit is crash-looping". A failure for any other reason is inconclusive.
#
# Usage:
#   scripts/systemd-e2e.sh                # VM defaults to tslink-cold2
#   TSLINK_E2E_VM=<name> scripts/systemd-e2e.sh
#   TSLINK_E2E_KEEP=1 ...                 # keep host work dir + VM dir + logs
#   TSLINK_E2E_PREFIX_BAD_BIN=/abs/in/vm  # optional: a pre-fix bad build already
#                                         # inside the VM; the e2e must fail on it too
#
# The working tree is never modified: mutants are built with `go build -overlay`.
set -euo pipefail

VM="${TSLINK_E2E_VM:-tslink-cold2}"
KEEP="${TSLINK_E2E_KEEP:-0}"
PREFIX_BAD_BIN="${TSLINK_E2E_PREFIX_BAD_BIN:-}"
SENTINEL='E2E_SENTINEL: install claimed success while the unit is crash-looping'
TEST_NAME='TestSystemdInstallE2E'

REPO="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
command -v orb >/dev/null 2>&1 || { echo "orb (OrbStack CLI) not found on PATH" >&2; exit 2; }
command -v go >/dev/null 2>&1 || { echo "go not found on PATH" >&2; exit 2; }
# Liveness is "can it run a command", not a parse of `orb list` (whose column
# layout differs between TTY and pipe).
if ! orb -m "$VM" true >/dev/null 2>&1; then
  echo "VM '${VM}' is unreachable (see: orb list)" >&2; exit 2
fi

vm_arch="$(orb -m "$VM" uname -m)"
case "$vm_arch" in
  aarch64|arm64) GOARCH=arm64 ;;
  x86_64|amd64)  GOARCH=amd64 ;;
  *) echo "unsupported VM arch: $vm_arch" >&2; exit 2 ;;
esac

# Host work dir must live on a path the VM sees under the SAME name.
# OrbStack shares the macOS filesystem; /tmp inside the VM is the VM's own, so
# on macOS use /private/tmp explicitly.
if [ "$(uname -s)" = Darwin ]; then
  WORK="$(mktemp -d /private/tmp/tslink-systemd-e2e.XXXXXX)"
else
  WORK="$(mktemp -d /tmp/tslink-systemd-e2e.XXXXXX)"
fi
VMUSER="$(orb -m "$VM" id -un)"
VMDIR="/home/${VMUSER}/tslink-systemd-e2e.$$"
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
python3 - "$REPO/cmd/install_linux.go" "$WORK/install_linux_blind.go" <<'PY'
import sys
src = open(sys.argv[1], encoding="utf-8").read()
anchor = "func verifySystemdServiceRunning() (bool, error) {\n"
n = src.count(anchor)
if n != 1:
    sys.exit(f"blind mutant: anchor found {n} times, want 1 (install_linux.go changed shape?)")
src = src.replace(anchor, anchor + "\tif os.Getenv(\"TSLINK_E2E_BLIND_VERIFY\") == \"1\" {\n\t\treturn false, nil // e2e mutant: blind verify\n\t}\n")
open(sys.argv[2], "w", encoding="utf-8").write(src)
PY

cat > "$WORK/ov_bad.json" <<JSON
{"Replace":{"$REPO/cmd/zz_e2e_badserve.go":"$WORK/badserve.go"}}
JSON
cat > "$WORK/ov_blind.json" <<JSON
{"Replace":{"$REPO/cmd/zz_e2e_badserve.go":"$WORK/badserve.go","$REPO/cmd/install_linux.go":"$WORK/install_linux_blind.go"}}
JSON

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
  local name="$1" label="$2" rc="$3" want="$4" res sent passed
  sent="$(sentinel_in "$label")"; passed="$(passed_in "$label")"
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
