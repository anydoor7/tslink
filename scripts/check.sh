#!/usr/bin/env bash
# check.sh runs the portable steps of the release gate, so a contributor runs
# locally what CI runs, on any platform:
#
#   build, vet (this platform, then linux, darwin and windows), gofmt,
#   go mod tidy, staticcheck when it is installed (the three platforms, as CI),
#   the CLI manifest and third-party notice checks, and the test suite.
#
# Usage: scripts/check.sh [go test flags...]
#   scripts/check.sh              # go test -count=1 ./...
#   scripts/check.sh -race        # go test -count=1 -race ./...
#
# CI steps that need a particular runner are not here: the macOS keychain and
# launchd tests run where they apply, the three-platform manifest mark proof
# (tools/check-manifest-platforms) compares manifests built on each platform,
# and the release packaging runs GoReleaser.
set -euo pipefail

cd "$(dirname "$0")/.."

step() {
	printf '\n==> %s\n' "$*"
}

step "go build ./..."
go build ./...

step "go vet ./..."
go vet ./...
for goos in linux darwin windows; do
	step "GOOS=${goos} go vet ./..."
	GOOS="${goos}" go vet ./...
done

step "gofmt -l ."
unformatted="$(gofmt -l .)"
if [ -n "${unformatted}" ]; then
	printf 'gofmt needs to be run on:\n%s\n' "${unformatted}" >&2
	exit 1
fi

step "go mod tidy -diff"
go mod tidy -diff

staticcheck="$(command -v staticcheck || true)"
if [ -z "${staticcheck}" ] && [ -x "$(go env GOPATH)/bin/staticcheck" ]; then
	staticcheck="$(go env GOPATH)/bin/staticcheck"
fi
if [ -n "${staticcheck}" ]; then
	for goos in linux darwin windows; do
		step "GOOS=${goos} staticcheck ./..."
		GOOS="${goos}" "${staticcheck}" ./...
	done
else
	step "staticcheck: not installed, skipped (go install honnef.co/go/tools/cmd/staticcheck@latest)"
fi

step "go run ./tools/gen-manifest -check"
go run ./tools/gen-manifest -check

step "go run ./tools/gen-notices -check"
go run ./tools/gen-notices -check

step "go test -count=1 $* ./..."
go test -count=1 "$@" ./...

printf '\ncheck.sh: all steps passed\n'
