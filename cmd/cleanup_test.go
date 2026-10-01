package cmd

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/anydoor7/tslink/internal/config"
	"github.com/anydoor7/tslink/internal/lifecycle"
	"github.com/anydoor7/tslink/internal/output"
	"github.com/anydoor7/tslink/internal/registry"
	tsruntime "github.com/anydoor7/tslink/internal/runtime"
	"github.com/anydoor7/tslink/internal/testenv"
	"github.com/spf13/cobra"
)

type cleanupExactLookupFake struct {
	nodeID  string
	matches int
	err     error
}

func (f cleanupExactLookupFake) Find(context.Context, string) (string, int, error) {
	if f.matches != 1 {
		return "", f.matches, f.err
	}
	return f.nodeID, f.matches, f.err
}

type adoptOwnedNodeContractFake struct {
	ledger tsruntime.OwnershipLedger
}

func (f *adoptOwnedNodeContractFake) Adopt(_ string, serviceName, nodeID string, recordedAt time.Time, retire bool) error {
	next, err := contractAdoptedLedger(f.ledger, serviceName, nodeID, recordedAt, retire, true)
	if err != nil {
		return err
	}
	f.ledger = next
	return nil
}

type previewAdoptOwnedNodeContractFake struct {
	ledger tsruntime.OwnershipLedger
}

func (f previewAdoptOwnedNodeContractFake) Preview(_ string, serviceName, nodeID string, recordedAt time.Time, retire bool) (tsruntime.OwnershipLedger, error) {
	return contractAdoptedLedger(f.ledger, serviceName, nodeID, recordedAt, retire, false)
}

func contractAdoptedLedger(ledger tsruntime.OwnershipLedger, serviceName, nodeID string, recordedAt time.Time, retire, sortForWrite bool) (tsruntime.OwnershipLedger, error) {
	if strings.TrimSpace(serviceName) == "" || strings.TrimSpace(nodeID) == "" {
		return tsruntime.OwnershipLedger{}, errors.New("invalid contract adoption")
	}
	recordedAt = recordedAt.UTC()
	var retiredAt *time.Time
	if retire {
		retiredAt = &recordedAt
	}
	ledger.SchemaVersion = tsruntime.OwnershipSchemaVersion
	ledger.Nodes = append(append([]tsruntime.OwnedNode(nil), ledger.Nodes...), tsruntime.OwnedNode{
		ServiceName: serviceName,
		NodeID:      nodeID,
		RecordedAt:  recordedAt,
		RetiredAt:   retiredAt,
	})
	if sortForWrite {
		sort.Slice(ledger.Nodes, func(i, j int) bool {
			if ledger.Nodes[i].ServiceName == ledger.Nodes[j].ServiceName {
				return ledger.Nodes[i].NodeID < ledger.Nodes[j].NodeID
			}
			return ledger.Nodes[i].ServiceName < ledger.Nodes[j].ServiceName
		})
	}
	return ledger, nil
}

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
		return lifecycle.Result{DryRun: options.DryRun, ExpiredFunnels: []string{"public"}, DevicesWouldDelete: []string{"orphan"}, ACLAction: lifecycle.ACLSkipped}, nil
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
	oldFind, oldAdopt, oldPreview := cleanupFindExactDeviceNodeIDFn, cleanupAdoptOwnedNodeFn, cleanupPreviewAdoptOwnedNodeFn
	t.Cleanup(func() {
		cleanupReconcileFn, cleanupNowFn = oldReconcile, oldNow
		cleanupFindExactDeviceNodeIDFn, cleanupAdoptOwnedNodeFn, cleanupPreviewAdoptOwnedNodeFn = oldFind, oldAdopt, oldPreview
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
			fake := cleanupExactLookupFake{nodeID: "node-last-duplicate", matches: matches}
			cleanupFindExactDeviceNodeIDFn = fake.Find
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
			failure := output.NewFailureForError("cleanup", err)
			data, ok := failure.Error.Data.(cleanupAdoptionErrorData)
			if !ok || data.Matches != matches {
				t.Fatalf("error data = %#v, want structured matches=%d", failure.Error.Data, matches)
			}
		})
	}
}

func TestCleanupAdoptWritesLedgerBeforeNormalExactCleanup(t *testing.T) {
	command := withCleanupAdoptionSeams(t)
	regPath, err := config.RegistryPath()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(regPath, []byte(`{"schema_version":1,"services":[]}`), 0o600); err != nil {
		t.Fatal(err)
	}
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
		if len(ledger.Nodes) != 1 || ledger.Nodes[0].ServiceName != "legacy" || ledger.Nodes[0].NodeID != "node-adopted" || ledger.Nodes[0].RetiredAt == nil || !ledger.Nodes[0].RetiredAt.Equal(now) {
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

func TestCleanupAdoptRegisteredServiceRecordsActiveProof(t *testing.T) {
	command := withCleanupAdoptionSeams(t)
	now := time.Date(2030, 1, 1, 12, 0, 0, 0, time.UTC)
	cleanupNowFn = func() time.Time { return now }
	regPath, err := config.RegistryPath()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := registry.Add(regPath, registry.Service{Name: "repaired", Type: registry.TypeProxy, Target: "http://localhost:3000"}); err != nil {
		t.Fatal(err)
	}
	cleanupFindExactDeviceNodeIDFn = func(context.Context, string) (string, int, error) {
		return "node-repaired", 1, nil
	}
	cleanupReconcileFn = func(_ context.Context, options lifecycle.Options) (lifecycle.Result, error) {
		ledger, err := tsruntime.LoadOwnership(options.OwnershipPath)
		if err != nil {
			t.Fatal(err)
		}
		if len(ledger.Nodes) != 1 || ledger.Nodes[0].ServiceName != "repaired" || ledger.Nodes[0].RetiredAt != nil {
			t.Fatalf("ledger = %+v, want active proof without retired_at", ledger)
		}
		return lifecycle.Result{ACLAction: lifecycle.ACLNotRequested}, nil
	}
	_ = command.Flags().Set("adopt", "repaired")
	_ = command.Flags().Set("force", "true")
	_ = command.Flags().Set("dry-run", "false")
	if err := command.RunE(command, nil); err != nil {
		t.Fatal(err)
	}
}

func TestCleanupAdoptUntrustedRegistryRecordsActiveProofWithoutRetirement(t *testing.T) {
	for _, state := range []string{"missing", "empty"} {
		t.Run(state, func(t *testing.T) {
			command := withCleanupAdoptionSeams(t)
			now := time.Date(2030, 1, 1, 12, 0, 0, 0, time.UTC)
			cleanupNowFn = func() time.Time { return now }
			regPath, err := config.RegistryPath()
			if err != nil {
				t.Fatal(err)
			}
			ownershipPath, err := config.NodeOwnershipPath()
			if err != nil {
				t.Fatal(err)
			}
			if _, err := registry.Add(regPath, registry.Service{Name: "repaired", Type: registry.TypeProxy, Target: "http://localhost:3000"}); err != nil {
				t.Fatal(err)
			}
			if state == "missing" {
				if err := os.Remove(regPath); err != nil {
					t.Fatal(err)
				}
			} else if err := os.WriteFile(regPath, []byte(" \n"), 0o600); err != nil {
				t.Fatal(err)
			}

			cleanupFindExactDeviceNodeIDFn = func(context.Context, string) (string, int, error) {
				return "node-repaired", 1, nil
			}
			gotRetire := true
			cleanupAdoptOwnedNodeFn = func(path, serviceName, nodeID string, recordedAt time.Time, retire bool) error {
				gotRetire = retire
				return tsruntime.AdoptOwnedNode(path, serviceName, nodeID, recordedAt, retire)
			}
			cleanupReconcileFn = func(_ context.Context, options lifecycle.Options) (lifecycle.Result, error) {
				return lifecycle.Result{DryRun: options.DryRun, ACLAction: lifecycle.ACLNotRequested}, nil
			}
			_ = command.Flags().Set("adopt", "repaired")
			_ = command.Flags().Set("force", "true")
			_ = command.Flags().Set("dry-run", "false")
			var stderr bytes.Buffer
			command.SetErr(&stderr)
			if err := command.RunE(command, nil); err != nil {
				t.Fatal(err)
			}
			if gotRetire {
				t.Fatal("untrusted registry authorized retired_at during adoption")
			}
			ledger, err := tsruntime.LoadOwnership(ownershipPath)
			if err != nil {
				t.Fatal(err)
			}
			if len(ledger.Nodes) != 1 || ledger.Nodes[0].ServiceName != "repaired" || ledger.Nodes[0].RetiredAt != nil {
				t.Fatalf("ledger = %+v, want active proof without retired_at", ledger)
			}
			warning := stderr.String()
			if !strings.Contains(warning, "registry.json is "+state) || !strings.Contains(warning, "retired_at was not set") || !strings.Contains(warning, "repair registry.json") {
				t.Fatalf("warning = %q, want state-specific repair guidance", warning)
			}
		})
	}
}

func TestCleanupAdoptPreviewAndApplyUseSameRetireDecision(t *testing.T) {
	command := withCleanupAdoptionSeams(t)
	regPath, err := config.RegistryPath()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := registry.Add(regPath, registry.Service{Name: "registered", Type: registry.TypeProxy, Target: "http://localhost:3000"}); err != nil {
		t.Fatal(err)
	}
	lookupFake := cleanupExactLookupFake{nodeID: "node-registered", matches: 1}
	cleanupFindExactDeviceNodeIDFn = lookupFake.Find
	var previewRetire, applyRetire bool
	previewFake := previewAdoptOwnedNodeContractFake{}
	applyFake := &adoptOwnedNodeContractFake{}
	cleanupPreviewAdoptOwnedNodeFn = func(_, _, _ string, _ time.Time, boolRetire bool) (tsruntime.OwnershipLedger, error) {
		previewRetire = boolRetire
		return previewFake.Preview("", "registered", "node-registered", time.Time{}, boolRetire)
	}
	cleanupAdoptOwnedNodeFn = func(_, _, _ string, _ time.Time, boolRetire bool) error {
		applyRetire = boolRetire
		return applyFake.Adopt("", "registered", "node-registered", time.Time{}, boolRetire)
	}
	cleanupReconcileFn = func(_ context.Context, options lifecycle.Options) (lifecycle.Result, error) {
		return lifecycle.Result{DryRun: options.DryRun, ACLAction: lifecycle.ACLNotRequested}, nil
	}
	_ = command.Flags().Set("adopt", "registered")
	_ = command.Flags().Set("force", "true")
	if err := command.RunE(command, nil); err != nil {
		t.Fatal(err)
	}
	_ = command.Flags().Set("dry-run", "false")
	if err := command.RunE(command, nil); err != nil {
		t.Fatal(err)
	}
	if previewRetire != applyRetire || previewRetire {
		t.Fatalf("retire preview=%t apply=%t, want identical false decision for registered service", previewRetire, applyRetire)
	}
}

func TestCleanupAdoptRegistryLookupIsExactAcrossMultipleServices(t *testing.T) {
	for _, tc := range []struct {
		name       string
		registry   string
		wantRetire bool
	}{
		{
			name:       "later_exact_match_among_similar_names",
			registry:   `{"schema_version":1,"services":[{"name":"repair","type":"proxy","target":"http://localhost:3000"},{"name":"repaired-old","type":"proxy","target":"http://localhost:3001"},{"name":"repaired","type":"proxy","target":"http://localhost:3002"}]}`,
			wantRetire: false,
		},
		{
			name:       "case_variant_is_not_an_exact_match",
			registry:   `{"schema_version":1,"services":[{"name":"Repaired","type":"proxy","target":"http://localhost:3000"},{"name":"repaired-old","type":"proxy","target":"http://localhost:3001"}]}`,
			wantRetire: true,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			command := withCleanupAdoptionSeams(t)
			regPath, err := config.RegistryPath()
			if err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(regPath, []byte(tc.registry), 0o600); err != nil {
				t.Fatal(err)
			}
			cleanupFindExactDeviceNodeIDFn = func(context.Context, string) (string, int, error) {
				return "node-repaired", 1, nil
			}
			gotRetire := !tc.wantRetire
			cleanupPreviewAdoptOwnedNodeFn = func(_, _, _ string, _ time.Time, boolRetire bool) (tsruntime.OwnershipLedger, error) {
				gotRetire = boolRetire
				return tsruntime.OwnershipLedger{SchemaVersion: tsruntime.OwnershipSchemaVersion, Nodes: []tsruntime.OwnedNode{}}, nil
			}
			cleanupReconcileFn = func(_ context.Context, options lifecycle.Options) (lifecycle.Result, error) {
				return lifecycle.Result{DryRun: options.DryRun, ACLAction: lifecycle.ACLNotRequested}, nil
			}
			_ = command.Flags().Set("adopt", "repaired")
			_ = command.Flags().Set("force", "true")
			if err := command.RunE(command, nil); err != nil {
				t.Fatal(err)
			}
			if gotRetire != tc.wantRetire {
				t.Fatalf("retire = %t, want %t", gotRetire, tc.wantRetire)
			}
		})
	}
}

func TestCleanupAdoptDryRunPreviewsWithoutChangingLedgerBytes(t *testing.T) {
	command := withCleanupAdoptionSeams(t)
	regPath, err := config.RegistryPath()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(regPath, []byte(`{"schema_version":1,"services":[]}`), 0o600); err != nil {
		t.Fatal(err)
	}
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
	cleanupAdoptOwnedNodeFn = func(string, string, string, time.Time, bool) error {
		t.Fatal("dry-run adoption must not write ownership proof")
		return nil
	}
	cleanupReconcileFn = func(_ context.Context, options lifecycle.Options) (lifecycle.Result, error) {
		if !options.DryRun {
			t.Fatal("default cleanup must remain dry-run")
		}
		if options.OwnershipOverride == nil || len(options.OwnershipOverride.Nodes) != 2 {
			t.Fatalf("ownership override = %+v, want existing plus in-memory adopted proof", options.OwnershipOverride)
		}
		preview := options.OwnershipOverride.Nodes[1]
		if preview.ServiceName != "legacy" || preview.NodeID != "node-preview" || preview.RetiredAt == nil {
			t.Fatalf("preview ownership = %+v, want reviewed retired adoption", preview)
		}
		return lifecycle.Result{DryRun: true, DevicesWouldDelete: []string{"legacy"}, ACLAction: lifecycle.ACLNotRequested}, nil
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
	if !strings.Contains(out.String(), wantPreview) || !strings.Contains(out.String(), "would delete owned devices: legacy") || strings.Index(out.String(), "adoption preview") > strings.Index(out.String(), "cleanup dry-run") {
		t.Fatalf("stdout = %q, want explicit not-written preview and apply-equivalent deletion list", out.String())
	}
}

func TestCleanupAdoptDryRunJSONMarksProofNotWritten(t *testing.T) {
	command := withCleanupAdoptionSeams(t)
	cleanupFindExactDeviceNodeIDFn = func(context.Context, string) (string, int, error) {
		return "node-preview", 1, nil
	}
	cleanupAdoptOwnedNodeFn = func(string, string, string, time.Time, bool) error {
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

func TestCleanupReconcileFailureKeepsAdoptionWarningInError(t *testing.T) {
	command := withCleanupAdoptionSeams(t)
	now := time.Date(2030, 1, 1, 12, 0, 0, 0, time.UTC)
	cleanupNowFn = func() time.Time { return now }
	regPath, err := config.RegistryPath()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := registry.Add(regPath, registry.Service{Name: "repaired", Type: registry.TypeProxy, Target: "http://localhost:3000"}); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(regPath); err != nil {
		t.Fatal(err)
	}
	cleanupFindExactDeviceNodeIDFn = func(context.Context, string) (string, int, error) {
		return "node-repaired", 1, nil
	}
	cleanupAdoptOwnedNodeFn = func(path, serviceName, nodeID string, recordedAt time.Time, retire bool) error {
		return tsruntime.AdoptOwnedNode(path, serviceName, nodeID, recordedAt, retire)
	}
	cleanupReconcileFn = func(context.Context, lifecycle.Options) (lifecycle.Result, error) {
		return lifecycle.Result{}, errors.New("reconcile boom")
	}
	_ = command.Flags().Set("adopt", "repaired")
	_ = command.Flags().Set("force", "true")
	_ = command.Flags().Set("dry-run", "false")
	var stderr bytes.Buffer
	command.SetErr(&stderr)
	err = command.RunE(command, nil)
	if err == nil {
		t.Fatal("cleanup returned nil error, want reconcile failure")
	}
	message := err.Error()
	if !strings.Contains(message, "reconcile boom") {
		t.Fatalf("error = %q, want the reconcile failure preserved", message)
	}
	if !strings.Contains(message, "registry.json is missing") || !strings.Contains(message, "retired_at was not set") {
		t.Fatalf("error = %q, want the adoption warning carried alongside the reconcile failure", message)
	}
}
