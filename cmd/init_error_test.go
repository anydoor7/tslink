package cmd

import (
	"bytes"
	"fmt"
	"strings"
	"testing"

	"github.com/monody0007/tslink/internal/output"
)

// --- stop command: pidPathFn error ---

func TestStopCmd_PIDPathError(t *testing.T) {
	old := pidPathFn
	pidPathFn = func() (string, error) { return "", fmt.Errorf("injected pidpath error") }
	defer func() { pidPathFn = old }()

	stopCmd, _, err := rootCmd.Find([]string{"stop"})
	if err != nil {
		t.Fatalf("find stop command: %v", err)
	}

	var buf bytes.Buffer
	stopCmd.SetOut(&buf)

	err = stopCmd.RunE(stopCmd, nil)
	if err == nil {
		t.Fatal("expected error from pidPathFn")
	}
	if !strings.Contains(err.Error(), "injected pidpath error") {
		t.Errorf("unexpected error: %v", err)
	}
}

// --- list command: registryPathFn error ---

func TestListCmd_RegistryPathError(t *testing.T) {
	old := registryPathFn
	registryPathFn = func() (string, error) { return "", fmt.Errorf("injected regpath error") }
	defer func() { registryPathFn = old }()

	listCmd, _, err := rootCmd.Find([]string{"list"})
	if err != nil {
		t.Fatalf("find list command: %v", err)
	}

	var buf bytes.Buffer
	listCmd.SetOut(&buf)

	err = listCmd.RunE(listCmd, nil)
	if err == nil {
		t.Fatal("expected error from registryPathFn")
	}
	if !strings.Contains(err.Error(), "injected regpath error") {
		t.Errorf("unexpected error: %v", err)
	}
}

// --- remove command: ensureDirFn error ---

func TestRemoveCmd_EnsureDirError(t *testing.T) {
	old := ensureDirFn
	ensureDirFn = func() error { return fmt.Errorf("injected ensuredir error") }
	defer func() { ensureDirFn = old }()

	removeCmd, _, err := rootCmd.Find([]string{"remove"})
	if err != nil {
		t.Fatalf("find remove command: %v", err)
	}

	var buf bytes.Buffer
	removeCmd.SetOut(&buf)
	removeCmd.SetErr(&buf)

	err = removeCmd.RunE(removeCmd, []string{"myapp"})
	if err == nil {
		t.Fatal("expected error from ensureDirFn")
	}
	if !strings.Contains(err.Error(), "injected ensuredir error") {
		t.Errorf("unexpected error: %v", err)
	}
}

// --- remove command: registryPathFn error ---

func TestRemoveCmd_RegistryPathError(t *testing.T) {
	oldEnsure := ensureDirFn
	ensureDirFn = func() error { return nil }
	defer func() { ensureDirFn = oldEnsure }()

	oldReg := registryPathFn
	registryPathFn = func() (string, error) { return "", fmt.Errorf("injected regpath error") }
	defer func() { registryPathFn = oldReg }()

	removeCmd, _, err := rootCmd.Find([]string{"remove"})
	if err != nil {
		t.Fatalf("find remove command: %v", err)
	}

	var buf bytes.Buffer
	removeCmd.SetOut(&buf)
	removeCmd.SetErr(&buf)

	err = removeCmd.RunE(removeCmd, []string{"myapp"})
	if err == nil {
		t.Fatal("expected error from registryPathFn")
	}
	if !strings.Contains(err.Error(), "injected regpath error") {
		t.Errorf("unexpected error: %v", err)
	}
}

// --- add command: ensureDirFn error ---

func TestAddCmd_EnsureDirError(t *testing.T) {
	old := ensureDirFn
	ensureDirFn = func() error { return fmt.Errorf("injected ensuredir error") }
	defer func() { ensureDirFn = old }()

	err := runAddCmd(t, []string{"myapp"}, map[string]string{"proxy": "localhost:3000"})
	if err == nil {
		t.Fatal("expected error from ensureDirFn")
	}
	if !strings.Contains(err.Error(), "injected ensuredir error") {
		t.Errorf("unexpected error: %v", err)
	}
}

// --- add command: registryPathFn error ---

func TestAddCmd_RegistryPathError(t *testing.T) {
	oldEnsure := ensureDirFn
	ensureDirFn = func() error { return nil }
	defer func() { ensureDirFn = oldEnsure }()

	oldReg := registryPathFn
	registryPathFn = func() (string, error) { return "", fmt.Errorf("injected regpath error") }
	defer func() { registryPathFn = oldReg }()

	err := runAddCmd(t, []string{"myapp"}, map[string]string{"proxy": "localhost:3000"})
	if err == nil {
		t.Fatal("expected error from registryPathFn")
	}
	if !strings.Contains(err.Error(), "injected regpath error") {
		t.Errorf("unexpected error: %v", err)
	}
}

// --- api command: ensureDirFn error ---

func TestAPICmd_EnsureDirError(t *testing.T) {
	old := ensureDirFn
	ensureDirFn = func() error { return fmt.Errorf("injected ensuredir error") }
	defer func() { ensureDirFn = old }()

	apiCmd, _, err := rootCmd.Find([]string{"api"})
	if err != nil {
		t.Fatalf("find api command: %v", err)
	}

	var buf bytes.Buffer
	apiCmd.SetOut(&buf)

	err = apiCmd.RunE(apiCmd, nil)
	if err == nil {
		t.Fatal("expected error from ensureDirFn")
	}
	if !output.IsSilent(err) || output.ExitCode(err) != output.ExitError {
		t.Errorf("unexpected error: %v", err)
	}
	result := parseResult(t, buf.String())
	if result.OK || result.Error == nil || !strings.Contains(result.Error.Message, "injected ensuredir error") {
		t.Fatalf("api failure envelope = %+v", result)
	}
}

// --- api command: registryPathFn error ---

func TestAPICmd_RegistryPathError(t *testing.T) {
	oldEnsure := ensureDirFn
	ensureDirFn = func() error { return nil }
	defer func() { ensureDirFn = oldEnsure }()

	oldReg := registryPathFn
	registryPathFn = func() (string, error) { return "", fmt.Errorf("injected regpath error") }
	defer func() { registryPathFn = oldReg }()

	apiCmd, _, err := rootCmd.Find([]string{"api"})
	if err != nil {
		t.Fatalf("find api command: %v", err)
	}

	var buf bytes.Buffer
	apiCmd.SetOut(&buf)

	err = apiCmd.RunE(apiCmd, nil)
	if err == nil {
		t.Fatal("expected error from registryPathFn")
	}
	if !output.IsSilent(err) || output.ExitCode(err) != output.ExitError {
		t.Errorf("unexpected error: %v", err)
	}
	result := parseResult(t, buf.String())
	if result.OK || result.Error == nil || !strings.Contains(result.Error.Message, "injected regpath error") {
		t.Fatalf("api failure envelope = %+v", result)
	}
}

// --- api command: pidPathFn error ---

func TestAPICmd_PIDPathError(t *testing.T) {
	oldEnsure := ensureDirFn
	ensureDirFn = func() error { return nil }
	defer func() { ensureDirFn = oldEnsure }()

	oldReg := registryPathFn
	registryPathFn = func() (string, error) { return "/tmp/test-reg.json", nil }
	defer func() { registryPathFn = oldReg }()

	oldPid := pidPathFn
	pidPathFn = func() (string, error) { return "", fmt.Errorf("injected pidpath error") }
	defer func() { pidPathFn = oldPid }()

	apiCmd, _, err := rootCmd.Find([]string{"api"})
	if err != nil {
		t.Fatalf("find api command: %v", err)
	}

	var buf bytes.Buffer
	apiCmd.SetOut(&buf)

	err = apiCmd.RunE(apiCmd, nil)
	if err == nil {
		t.Fatal("expected error from pidPathFn")
	}
	if !output.IsSilent(err) || output.ExitCode(err) != output.ExitError {
		t.Errorf("unexpected error: %v", err)
	}
	result := parseResult(t, buf.String())
	if result.OK || result.Error == nil || !strings.Contains(result.Error.Message, "injected pidpath error") {
		t.Fatalf("api failure envelope = %+v", result)
	}
}
