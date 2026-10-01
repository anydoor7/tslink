package main

import (
	"bytes"
	"os"
	"os/exec"
	"strings"
	"testing"
)

// TestBinaryDoesNotLinkPrometheus pins the removal of the metrics layer. It
// wrapped every service handler outside --allow, kept one series per
// client-chosen HTTP method forever, had no reader, and was about a quarter of
// the stripped binary. The access log is the single status recorder; nothing in
// the tslink binary may pull the Prometheus client back in, on any platform the
// release builds.
//
// The listing is checked for two packages it must contain, so a go list that
// silently printed nothing cannot pass as "no Prometheus".
func TestBinaryDoesNotLinkPrometheus(t *testing.T) {
	for _, goos := range []string{"darwin", "linux", "windows"} {
		t.Run(goos, func(t *testing.T) {
			cmd := exec.Command("go", "list", "-deps", ".")
			cmd.Env = append(os.Environ(), "GOOS="+goos, "GOFLAGS=-mod=readonly", "GOPROXY=off")
			var stderr bytes.Buffer
			cmd.Stderr = &stderr
			out, err := cmd.Output()
			if err != nil {
				t.Fatalf("go list -deps . (GOOS=%s): %v\n%s", goos, err, stderr.String())
			}
			deps := strings.Fields(string(out))
			for _, want := range []string{"github.com/anydoor7/tslink", "tailscale.com/tsnet"} {
				if !containsDep(deps, want) {
					t.Fatalf("go list -deps . (GOOS=%s) does not list %s; the listing cannot vouch for anything (%d lines)", goos, want, len(deps))
				}
			}
			var linked []string
			for _, dep := range deps {
				if strings.HasPrefix(dep, "github.com/prometheus/") {
					linked = append(linked, dep)
				}
			}
			if len(linked) > 0 {
				t.Fatalf("the tslink binary (GOOS=%s) links %d Prometheus packages, want none: %v", goos, len(linked), linked)
			}
		})
	}
}

func containsDep(deps []string, want string) bool {
	for _, dep := range deps {
		if dep == want {
			return true
		}
	}
	return false
}
