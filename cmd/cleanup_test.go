package cmd

import (
	"bytes"
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/monody0007/tslink/internal/config"
	"github.com/monody0007/tslink/internal/lifecycle"
	tsruntime "github.com/monody0007/tslink/internal/runtime"
	"github.com/monody0007/tslink/internal/testenv"
	"github.com/spf13/cobra"
)

func TestCleanupCommandDefaultsToDryRunAndPassesManageACLOptIn(t *testing.T) {
	testenv.SetHome(t, t.TempDir())
	command, _, err := rootCmd.Find([]string{"cleanup"})
	if err != nil {
		t.Fatal(err)
	}
	if flag := command.Flags().Lookup("dry-run"); flag == nil || flag.DefValue != "true" {
		t.Fatalf("dry-run flag = %+v, want default true", flag)
	}
	oldReconcile, oldNow := cleanupReconcileFn, cleanupNowFn
	defer func() {
		cleanupReconcileFn, cleanupNowFn = oldReconcile, oldNow
		_ = command.Flags().Set("dry-run", "true")
		_ = command.Flags().Set("manage-acl", "false")
	}()
	now := time.Date(2026, 8, 31, 12, 0, 0, 0, time.UTC)
	cleanupNowFn = func() time.Time { return now }
	var got lifecycle.Options
	cleanupReconcileFn = func(_ context.Context, options lifecycle.Options) (lifecycle.Result, error) {
		got = options
		return lifecycle.Result{DryRun: options.DryRun, ExpiredFunnels: []string{"public"}, DevicesWouldDelete: []string{"orphan"}, ACLAction: lifecycle.ACLWouldDelete}, nil
	}
	if err := command.Flags().Set("manage-acl", "true"); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	command.SetOut(&out)
	command.SetErr(&out)
	if err := command.RunE(command, nil); err != nil {
		t.Fatal(err)
	}
	if !got.DryRun || !got.ManageACL || !got.CheckUnusedACL || !got.Now.Equal(now) {
		t.Fatalf("options = %+v", got)
	}
	if !strings.Contains(out.String(), "cleanup dry-run") || !strings.Contains(out.String(), "would delete owned devices: orphan") {
		t.Fatalf("output = %q", out.String())
	}
}

func withCleanupAdoptionSeams(t *testing.T) *cobra.Command {
	t.Helper()
	testenv.SetHome(t, t.TempDir())
	if err := config.EnsureDir(); err != nil {
		t.Fatal(err)
	}
	command, _, err := rootCmd.Find([]string{"cleanup"})
	if err != nil {
		t.Fatal(err)
	}
	oldReconcile, oldNow := cleanupReconcileFn, cleanupNowFn
	oldFind, oldAdopt := cleanupFindExactDeviceNodeIDFn, cleanupAdoptOwnedNodeFn
	t.Cleanup(func() {
		cleanupReconcileFn, cleanupNowFn = oldReconcile, oldNow
		cleanupFindExactDeviceNodeIDFn, cleanupAdoptOwnedNodeFn = oldFind, oldAdopt
		_ = command.Flags().Set("adopt", "")
		_ = command.Flags().Set("force", "false")
		_ = command.Flags().Set("dry-run", "true")
		_ = command.Flags().Set("manage-acl", "false")
	})
	return command
}

func TestCleanupAdoptRequiresForceBeforeRemoteLookup(t *testing.T) {
	command := withCleanupAdoptionSeams(t)
	cleanupFindExactDeviceNodeIDFn = func(context.Context, string) (string, int, error) {
		t.Fatal("remote lookup called without --force")
		return "", 0, nil
	}
	if err := command.Flags().Set("adopt", "legacy"); err != nil {
		t.Fatal(err)
	}
	err := command.RunE(command, nil)
	if err == nil || !strings.Contains(err.Error(), "--adopt requires --force") {
		t.Fatalf("error = %v, want force gate", err)
	}
}

func TestCleanupAdoptRejectsZeroAndMultipleExactMatches(t *testing.T) {
	for _, matches := range []int{0, 2} {
		t.Run(fmt.Sprintf("matches_%d", matches), func(t *testing.T) {
			command := withCleanupAdoptionSeams(t)
			cleanupFindExactDeviceNodeIDFn = func(context.Context, string) (string, int, error) {
				return "", matches, nil
			}
			if err := command.Flags().Set("adopt", "legacy"); err != nil {
				t.Fatal(err)
			}
			if err := command.Flags().Set("force", "true"); err != nil {
				t.Fatal(err)
			}
			err := command.RunE(command, nil)
			if err == nil || !strings.Contains(err.Error(), fmt.Sprintf("matched %d", matches)) {
				t.Fatalf("error = %v, want exact match count %d", err, matches)
			}
		})
	}
}

func TestCleanupAdoptWritesLedgerBeforeNormalExactCleanup(t *testing.T) {
	command := withCleanupAdoptionSeams(t)
	now := time.Date(2030, 1, 1, 12, 0, 0, 0, time.UTC)
	cleanupNowFn = func() time.Time { return now }
	cleanupFindExactDeviceNodeIDFn = func(_ context.Context, hostname string) (string, int, error) {
		if hostname != "legacy" {
			t.Fatalf("hostname = %q", hostname)
		}
		return "node-adopted", 1, nil
	}
	cleanupReconcileFn = func(_ context.Context, options lifecycle.Options) (lifecycle.Result, error) {
		ledger, err := tsruntime.LoadOwnership(options.OwnershipPath)
		if err != nil {
			t.Fatal(err)
		}
		if len(ledger.Nodes) != 1 || ledger.Nodes[0].ServiceName != "legacy" || ledger.Nodes[0].NodeID != "node-adopted" {
			t.Fatalf("ledger at normal cleanup = %+v", ledger)
		}
		return lifecycle.Result{DryRun: options.DryRun, DevicesWouldDelete: []string{"legacy"}}, nil
	}
	if err := command.Flags().Set("adopt", "legacy"); err != nil {
		t.Fatal(err)
	}
	if err := command.Flags().Set("force", "true"); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	command.SetOut(&out)
	command.SetErr(&out)
	if err := command.RunE(command, nil); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "adopted exact hostname match") || !strings.Contains(out.String(), "would delete owned devices: legacy") {
		t.Fatalf("output = %q", out.String())
	}
}

func TestCleanupAdoptRejectsExistingNameBoundToDifferentNode(t *testing.T) {
	command := withCleanupAdoptionSeams(t)
	ownershipPath, err := config.NodeOwnershipPath()
	if err != nil {
		t.Fatal(err)
	}
	if err := tsruntime.RecordOwnedNode(ownershipPath, "legacy", "node-existing", time.Now()); err != nil {
		t.Fatal(err)
	}
	cleanupFindExactDeviceNodeIDFn = func(context.Context, string) (string, int, error) {
		return "node-different", 1, nil
	}
	if err := command.Flags().Set("adopt", "legacy"); err != nil {
		t.Fatal(err)
	}
	if err := command.Flags().Set("force", "true"); err != nil {
		t.Fatal(err)
	}
	err = command.RunE(command, nil)
	if err == nil || !strings.Contains(err.Error(), "name is already bound") {
		t.Fatalf("error = %v, want name conflict", err)
	}
}
