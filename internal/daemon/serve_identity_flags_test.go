package daemon

import (
	"os"
	"path/filepath"
	"runtime/debug"
	"strings"
	"testing"
)

func TestServeIdentityLeadingGlobalFlag(t *testing.T) {
	oldArgs, oldInfo := processArguments, readExecutableBuildInfo
	t.Cleanup(func() { processArguments, readExecutableBuildInfo = oldArgs, oldInfo })
	readExecutableBuildInfo = func(string) (*debug.BuildInfo, error) {
		return &debug.BuildInfo{Main: debug.Module{Path: processProductID}}, nil
	}
	path := filepath.Join(t.TempDir(), "tslink.pid")
	if err := WritePID(path); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name string
		args []string
	}{
		{"canonical_control", []string{"tslink", "serve", "--json=false"}},
		{"leading_global_flag", []string{"tslink", "--json=false", "serve"}},
		{"leading_true_flag", []string{"tslink", "--json", "serve"}},
		{"repeated_global_flag", []string{"tslink", "--json=true", "--json=false", "serve"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			processArguments = func(int) ([]string, error) { return tc.args, nil }
			err := verifyProcessIdentity(path, os.Getpid())
			running := IsRunning(path)
			if err != nil || !running {
				t.Fatalf("valid foreground serve argv rejected: running=%t err=%v", running, err)
			}
		})
	}
}

func TestServeIdentityRejectsFlagsAndValuesThatAreNotCommands(t *testing.T) {
	oldArgs, oldInfo := processArguments, readExecutableBuildInfo
	t.Cleanup(func() { processArguments, readExecutableBuildInfo = oldArgs, oldInfo })
	readExecutableBuildInfo = func(string) (*debug.BuildInfo, error) {
		return &debug.BuildInfo{Main: debug.Module{Path: processProductID}}, nil
	}
	path := filepath.Join(t.TempDir(), "tslink.pid")
	if err := WritePID(path); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{
		{"tslink", "--json=serve"},
		{"tslink", "--json=invalid", "serve"},
		{"tslink", "--unknown", "serve"},
		{"tslink", "status", "serve"},
		{"tslink", "--json=false", "status", "serve"},
		{"tslink", "--json=false"},
	} {
		t.Run(strings.Join(args[1:], "_"), func(t *testing.T) {
			processArguments = func(int) ([]string, error) { return args, nil }
			if err := verifyProcessIdentity(path, os.Getpid()); err == nil {
				t.Fatalf("foreign argv accepted: %q", args)
			}
			if IsRunning(path) {
				t.Fatalf("foreign argv appears running: %q", args)
			}
		})
	}
}
