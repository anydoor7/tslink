package cmd

import (
	"bytes"
	"context"
	"fmt"
	"os"
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
				if matches > 1 {
					return "node-last-duplicate", matches, nil
				}
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
		return lifecycle.Result{DryRun: options.DryRun, DevicesDeleted: []string{"legacy"}, ACLAction: lifecycle.ACLNotRequested}, nil
	}
	if err := command.Flags().Set("adopt", "legacy"); err != nil {
		t.Fatal(err)
	}
	if err := command.Flags().Set("force", "true"); err != nil {
		t.Fatal(err)
	}
	if err := command.Flags().Set("dry-run", "false"); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	command.SetOut(&out)
	command.SetErr(&out)
	if err := command.RunE(command, nil); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "adopted exact TSLink-tagged hostname match") || !strings.Contains(out.String(), "devices_deleted=1") {
		t.Fatalf("output = %q", out.String())
	}
	if strings.Index(out.String(), "adopted exact TSLink-tagged") > strings.Index(out.String(), "cleanup applied") {
		t.Fatalf("output order = %q, want adoption before cleanup result", out.String())
	}
}

func TestCleanupAdoptDryRunPreviewsWithoutChangingLedgerBytes(t *testing.T) {
	command := withCleanupAdoptionSeams(t)
	ownershipPath, err := config.NodeOwnershipPath()
	if err != nil {
		t.Fatal(err)
	}
	seedTime := time.Date(2029, 1, 1, 0, 0, 0, 0, time.UTC)
	if err := tsruntime.RecordOwnedNode(ownershipPath, "existing", "node-existing", seedTime); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(ownershipPath)
	if err != nil {
		t.Fatal(err)
	}
	cleanupFindExactDeviceNodeIDFn = func(context.Context, string) (string, int, error) {
		return "node-preview", 1, nil
	}
	cleanupAdoptOwnedNodeFn = func(string, string, string, time.Time) error {
		t.Fatal("dry-run adoption must not write ownership proof")
		return nil
	}
	cleanupReconcileFn = func(_ context.Context, options lifecycle.Options) (lifecycle.Result, error) {
		if !options.DryRun {
			t.Fatal("default cleanup must remain dry-run")
		}
		return lifecycle.Result{DryRun: true, ACLAction: lifecycle.ACLNotRequested}, nil
	}
	_ = command.Flags().Set("adopt", "legacy")
	_ = command.Flags().Set("force", "true")
	var out bytes.Buffer
	command.SetOut(&out)
	command.SetErr(&out)
	if err := command.RunE(command, nil); err != nil {
		t.Fatal(err)
	}
	after, err := os.ReadFile(ownershipPath)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(before, after) {
		t.Fatalf("ledger bytes changed during dry-run:\nbefore=%s\nafter=%s", before, after)
	}
	wantPreview := "→ adoption preview (not written): service=legacy matches=1\n"
	if !strings.Contains(out.String(), wantPreview) || strings.Index(out.String(), "adoption preview") > strings.Index(out.String(), "cleanup dry-run") {
		t.Fatalf("stdout = %q, want explicit not-written preview before cleanup result", out.String())
	}
}

func TestCleanupAdoptDryRunJSONMarksProofNotWritten(t *testing.T) {
	command := withCleanupAdoptionSeams(t)
	cleanupFindExactDeviceNodeIDFn = func(context.Context, string) (string, int, error) {
		return "node-preview", 1, nil
	}
	cleanupAdoptOwnedNodeFn = func(string, string, string, time.Time) error {
		t.Fatal("dry-run adoption must not write ownership proof")
		return nil
	}
	cleanupReconcileFn = func(_ context.Context, options lifecycle.Options) (lifecycle.Result, error) {
		return lifecycle.Result{DryRun: options.DryRun, ACLAction: lifecycle.ACLNotRequested}, nil
	}
	_ = command.Flags().Set("adopt", "legacy")
	_ = command.Flags().Set("force", "true")
	setRootJSONFlag(t, true)
	raw := captureStdout(t, func() {
		if err := command.RunE(command, nil); err != nil {
			t.Fatal(err)
		}
	})
	data := dataMap(t, raw)
	adoption, ok := data["adoption"].(map[string]any)
	if !ok || adoption["service_name"] != "legacy" || adoption["matches"] != float64(1) || adoption["written"] != false {
		t.Fatalf("adoption = %#v, want explicit not-written preview", data["adoption"])
	}
	if _, written := data["devices_adopted"]; written {
		t.Fatalf("devices_adopted must be omitted in preview: %#v", data)
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
	if err := command.Flags().Set("dry-run", "false"); err != nil {
		t.Fatal(err)
	}
	err = command.RunE(command, nil)
	if err == nil || !strings.Contains(err.Error(), "name is already bound") {
		t.Fatalf("error = %v, want name conflict", err)
	}
}
