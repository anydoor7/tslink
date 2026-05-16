package cmd

import (
	"os"
	"strings"
	"testing"

	"github.com/monody0007/tslink/internal/registry"
)

func TestAddFunnel_WithProxy_Persisted(t *testing.T) {
	dir := t.TempDir()
	regPath := dir + "/registry.json"

	// Directly add a service with funnel=true to registry
	svc := registry.Service{
		Name:   "funnel-test",
		Type:   registry.TypeProxy,
		Target: "http://localhost:3000",
		Funnel: true,
	}
	if _, err := registry.Add(regPath, svc); err != nil {
		t.Fatalf("registry.Add: %v", err)
	}

	// Verify funnel field is persisted via API handler
	h := &apiHandler{regPath: regPath, pidPath: dir + "/tslink.pid"}
	resp := sendRequest(t, h, APIRequest{Action: "list"})
	if !resp.OK {
		t.Fatalf("expected ok, got error: %s", resp.Error)
	}
	if len(resp.Services) != 1 {
		t.Fatalf("expected 1 service, got %d", len(resp.Services))
	}
	if !resp.Services[0].Funnel {
		t.Error("expected funnel=true in listed service")
	}
}

func TestAddFunnel_WithProxy_NotSet(t *testing.T) {
	dir := t.TempDir()
	regPath := dir + "/registry.json"

	// Add service without funnel
	svc := registry.Service{
		Name:   "no-funnel",
		Type:   registry.TypeProxy,
		Target: "http://localhost:3000",
	}
	if _, err := registry.Add(regPath, svc); err != nil {
		t.Fatalf("registry.Add: %v", err)
	}

	h := &apiHandler{regPath: regPath, pidPath: dir + "/tslink.pid"}
	resp := sendRequest(t, h, APIRequest{Action: "list"})
	if !resp.OK {
		t.Fatalf("expected ok, got error: %s", resp.Error)
	}
	if resp.Services[0].Funnel {
		t.Error("expected funnel=false when not set")
	}
}

func TestAddFunnel_WithDir_Error(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("HOME", dir)

	configDir := dir + "/.config/tslink"
	if err := os.MkdirAll(configDir, 0o700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	shareDir := dir + "/share"
	if err := os.MkdirAll(shareDir, 0o700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	// Find the add command from rootCmd
	addCmd, _, err := rootCmd.Find([]string{"add"})
	if err != nil {
		t.Fatalf("find add command: %v", err)
	}

	// Set flags directly
	addCmd.Flags().Set("dir", shareDir)
	addCmd.Flags().Set("proxy", "")
	addCmd.Flags().Set("tcp", "")
	addCmd.Flags().Set("funnel", "true")
	defer func() {
		addCmd.Flags().Set("dir", "")
		addCmd.Flags().Set("funnel", "false")
	}()

	err = addCmd.RunE(addCmd, []string{"docs"})
	if err == nil {
		t.Fatal("expected error when using --funnel with --dir")
	}
	if !strings.Contains(err.Error(), "--funnel can only be used with --proxy") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestAddFunnel_WithTCP_Error(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("HOME", dir)

	configDir := dir + "/.config/tslink"
	if err := os.MkdirAll(configDir, 0o700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	// Find the add command from rootCmd
	addCmd, _, err := rootCmd.Find([]string{"add"})
	if err != nil {
		t.Fatalf("find add command: %v", err)
	}

	// Set flags directly
	addCmd.Flags().Set("tcp", "localhost:5432")
	addCmd.Flags().Set("proxy", "")
	addCmd.Flags().Set("dir", "")
	addCmd.Flags().Set("funnel", "true")
	defer func() {
		addCmd.Flags().Set("tcp", "")
		addCmd.Flags().Set("funnel", "false")
	}()

	err = addCmd.RunE(addCmd, []string{"mydb"})
	if err == nil {
		t.Fatal("expected error when using --funnel with --tcp")
	}
	if !strings.Contains(err.Error(), "--funnel can only be used with --proxy") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestBuildService_TCPRejectsAllow(t *testing.T) {
	_, err := buildService(AddParams{
		Name:  "mydb",
		TCP:   "localhost:5432",
		Allow: "alice@example.com",
	})
	if err == nil {
		t.Fatal("expected error when using --allow with --tcp")
	}
	if !strings.Contains(err.Error(), "--allow is not supported for --tcp") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestBuildService_RejectsInvalidTags(t *testing.T) {
	_, err := buildService(AddParams{
		Name:  "app",
		Proxy: "localhost:3000",
		Tags:  "tag:",
	})
	if err == nil {
		t.Fatal("expected invalid tag error")
	}
}

func TestBuildService_RejectsInvalidAllowTag(t *testing.T) {
	_, err := buildService(AddParams{
		Name:  "app",
		Proxy: "localhost:3000",
		Allow: "tag:",
	})
	if err == nil {
		t.Fatal("expected invalid allow tag error")
	}
}
