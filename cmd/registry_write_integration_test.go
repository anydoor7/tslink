package cmd

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/anydoor7/tslink/internal/config"
	"github.com/anydoor7/tslink/internal/registry"
	tsruntime "github.com/anydoor7/tslink/internal/runtime"
	"github.com/anydoor7/tslink/internal/tailapi"
	"github.com/anydoor7/tslink/internal/testenv"
	tailscale "tailscale.com/client/tailscale/v2"
)

func useRealRegistryCommandDependencies(t *testing.T) {
	t.Helper()
	oldEnsureDir := ensureDirFn
	oldRegistryPath := registryPathFn
	oldTagsEnsureDir := tagsEnsureDirFn
	oldTagsRegistryPath := tagsRegistryPathFn
	oldTagsLoadRegistry := tagsLoadRegistryFn
	oldTagsMutate := tagsMutateServiceFn
	oldDeleteDevices := deleteDevicesFn
	t.Cleanup(func() {
		ensureDirFn = oldEnsureDir
		registryPathFn = oldRegistryPath
		tagsEnsureDirFn = oldTagsEnsureDir
		tagsRegistryPathFn = oldTagsRegistryPath
		tagsLoadRegistryFn = oldTagsLoadRegistry
		tagsMutateServiceFn = oldTagsMutate
		deleteDevicesFn = oldDeleteDevices
	})
	ensureDirFn = config.EnsureDir
	registryPathFn = config.RegistryPath
	tagsEnsureDirFn = config.EnsureDir
	tagsRegistryPathFn = config.RegistryPath
	tagsLoadRegistryFn = registry.Load
	tagsMutateServiceFn = registry.MutateService
	deleteDevicesFn = tailapi.DeleteDevicesForService
}

func readRegistryBytes(t *testing.T, configDir string) ([]byte, registry.Registry) {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(configDir, "registry.json"))
	if err != nil {
		t.Fatalf("read registry.json: %v", err)
	}
	var reg registry.Registry
	if err := json.Unmarshal(data, &reg); err != nil {
		t.Fatalf("decode registry.json bytes %q: %v", data, err)
	}
	return data, reg
}

func TestCmdRegistryIntegrationAddPersistsRegistryJSON(t *testing.T) {
	resetRootJSONFlag(t)
	configDir := t.TempDir()
	t.Setenv(config.ConfigDirEnv, configDir)
	t.Setenv("TSLINK_DISABLE_KEYRING", "1")
	useRealRegistryCommandDependencies(t)

	if err := runAddCmd(t, []string{"integration-add"}, map[string]string{
		"proxy": "localhost:3100",
		"tags":  "tag:integration",
	}); err != nil {
		t.Fatalf("add command error = %v", err)
	}
	data, reg := readRegistryBytes(t, configDir)
	if !bytes.Contains(data, []byte(`"name": "integration-add"`)) || !bytes.Contains(data, []byte(`"tags": [`)) {
		t.Fatalf("registry.json bytes do not contain persisted add result: %s", data)
	}
	if len(reg.Services) != 1 || reg.Services[0].Name != "integration-add" || reg.Services[0].Target != "http://localhost:3100" || len(reg.Services[0].Tags) != 1 || reg.Services[0].Tags[0] != "tag:integration" || reg.Services[0].CreatedAt.IsZero() {
		t.Fatalf("registry after add = %+v", reg)
	}
}

func TestCmdRegistryIntegrationTagsSetPersistsRegistryJSON(t *testing.T) {
	resetRootJSONFlag(t)
	configDir := t.TempDir()
	t.Setenv(config.ConfigDirEnv, configDir)
	t.Setenv("TSLINK_DISABLE_KEYRING", "1")
	useRealRegistryCommandDependencies(t)
	regPath := filepath.Join(configDir, "registry.json")
	createdAt := time.Date(2030, 1, 1, 0, 0, 0, 0, time.UTC)
	if _, err := registry.Add(regPath, registry.Service{
		Name:      "integration-tags",
		Type:      registry.TypeProxy,
		Target:    "http://localhost:3200",
		Tags:      []string{"tag:old", "tag:shared"},
		CreatedAt: createdAt,
	}); err != nil {
		t.Fatal(err)
	}

	command, _, err := rootCmd.Find([]string{"tags", "set"})
	if err != nil {
		t.Fatal(err)
	}
	var stdout bytes.Buffer
	command.SetOut(&stdout)
	t.Cleanup(func() { command.SetOut(nil) })
	if err := command.RunE(command, []string{"integration-tags", "tag:replacement"}); err != nil {
		t.Fatalf("tags set command error = %v", err)
	}
	data, reg := readRegistryBytes(t, configDir)
	if bytes.Contains(data, []byte("tag:old")) || !bytes.Contains(data, []byte("tag:replacement")) {
		t.Fatalf("registry.json bytes did not persist replacement tag: %s", data)
	}
	if len(reg.Services) != 1 || len(reg.Services[0].Tags) != 1 || reg.Services[0].Tags[0] != "tag:replacement" || !reg.Services[0].CreatedAt.Equal(createdAt) {
		t.Fatalf("registry after tags set = %+v", reg)
	}
}

func TestCmdRegistryIntegrationRemovePersistsRegistryJSON(t *testing.T) {
	resetRootJSONFlag(t)
	configDir := t.TempDir()
	t.Setenv(config.ConfigDirEnv, configDir)
	t.Setenv("TSLINK_DISABLE_KEYRING", "1")
	useRealRegistryCommandDependencies(t)
	fake := testenv.NewStatefulTailnet(t)
	t.Setenv(tailapi.APIBaseURLEnv, fake.URL())
	t.Setenv("TSLINK_API_KEY", "test-placeholder")
	fake.SetDevices([]tailscale.Device{{NodeID: "node-remove", Hostname: "integration-remove", Tags: []string{"tag:tsmain"}}})

	regPath := filepath.Join(configDir, "registry.json")
	ownershipPath := filepath.Join(configDir, "node-ownership.json")
	if _, err := registry.Add(regPath, registry.Service{
		Name:   "integration-remove",
		Type:   registry.TypeProxy,
		Target: "http://localhost:3300",
		Tags:   []string{"tag:tsmain"},
	}); err != nil {
		t.Fatal(err)
	}
	if err := tsruntime.RecordOwnedNode(ownershipPath, "integration-remove", "node-remove", time.Now().UTC()); err != nil {
		t.Fatal(err)
	}

	command, _, err := rootCmd.Find([]string{"remove"})
	if err != nil {
		t.Fatal(err)
	}
	resetCommandLocalFlags(t, command)
	var stdout, stderr bytes.Buffer
	command.SetOut(&stdout)
	command.SetErr(&stderr)
	t.Cleanup(func() {
		command.SetOut(nil)
		command.SetErr(nil)
	})
	if err := command.RunE(command, []string{"integration-remove"}); err != nil {
		t.Fatalf("remove command error = %v", err)
	}
	data, reg := readRegistryBytes(t, configDir)
	if len(reg.Services) != 0 || !bytes.Contains(data, []byte(`"services": []`)) {
		t.Fatalf("registry after remove = %+v, bytes=%s", reg, data)
	}
	if devices := fake.Devices(); len(devices) != 0 {
		t.Fatalf("stateful tailnet devices after remove = %+v, want exact owned device deleted", devices)
	}
	ledger, err := tsruntime.LoadOwnership(ownershipPath)
	if err != nil || len(ledger.Nodes) != 0 {
		t.Fatalf("ownership ledger after confirmed delete = %+v, err=%v", ledger, err)
	}
}
