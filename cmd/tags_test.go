package cmd

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/monody0007/tslink/internal/config"
	"github.com/monody0007/tslink/internal/registry"
)

// helpers to save/restore function variables

func setTagsMocks(t *testing.T) {
	t.Helper()
	origReadTags := tagsReadTagsFn
	origDeleteTag := tagsDeleteTagFn
	origRegPath := tagsRegistryPathFn
	origLoadReg := tagsLoadRegistryFn
	origAddReg := tagsAddRegistryFn
	origEnsureDir := tagsEnsureDirFn
	origLoadGlobal := tagsLoadGlobalFn
	origSaveGlobal := tagsSaveGlobalFn
	origGetDefault := tagsGetDefaultFn
	t.Cleanup(func() {
		tagsReadTagsFn = origReadTags
		tagsDeleteTagFn = origDeleteTag
		tagsRegistryPathFn = origRegPath
		tagsLoadRegistryFn = origLoadReg
		tagsAddRegistryFn = origAddReg
		tagsEnsureDirFn = origEnsureDir
		tagsLoadGlobalFn = origLoadGlobal
		tagsSaveGlobalFn = origSaveGlobal
		tagsGetDefaultFn = origGetDefault
	})
}

func mockRegistryWithServices(services []registry.Service) {
	tagsLoadRegistryFn = func(path string) (*registry.Registry, error) {
		return &registry.Registry{Services: services}, nil
	}
}

func mockDefaults() {
	tagsRegistryPathFn = func() (string, error) { return "/tmp/test-reg.json", nil }
	tagsEnsureDirFn = func() error { return nil }
	tagsGetDefaultFn = func() string { return "tag:tsmain" }
}

// --- tags list ---

func TestTagsList_Empty(t *testing.T) {
	setTagsMocks(t)
	mockDefaults()
	mockRegistryWithServices(nil)

	var buf bytes.Buffer
	err := tagsListRun(&buf)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(buf.String(), "No services registered") {
		t.Errorf("expected 'No services registered', got: %s", buf.String())
	}
}

func TestTagsList_WithServices(t *testing.T) {
	setTagsMocks(t)
	mockDefaults()
	mockRegistryWithServices([]registry.Service{
		{Name: "myapp", Tags: []string{"tag:tsmain"}},
		{Name: "dashboard", Tags: []string{"tag:tsmain", "tag:shared"}},
		{Name: "notags"},
	})

	var buf bytes.Buffer
	err := tagsListRun(&buf)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	out := buf.String()
	if !strings.Contains(out, "SERVICE") || !strings.Contains(out, "TAGS") {
		t.Errorf("missing header, got: %s", out)
	}
	if !strings.Contains(out, "myapp") || !strings.Contains(out, "tag:tsmain") {
		t.Errorf("missing myapp row, got: %s", out)
	}
	if !strings.Contains(out, "dashboard") || !strings.Contains(out, "tag:tsmain, tag:shared") {
		t.Errorf("missing dashboard row, got: %s", out)
	}
	if !strings.Contains(out, "(none)") {
		t.Errorf("missing (none) for notags service, got: %s", out)
	}
}

func TestTagsList_RegistryPathError(t *testing.T) {
	setTagsMocks(t)
	tagsRegistryPathFn = func() (string, error) { return "", fmt.Errorf("path error") }

	err := tagsListRun(&bytes.Buffer{})
	if err == nil || !strings.Contains(err.Error(), "path error") {
		t.Errorf("expected path error, got: %v", err)
	}
}

func TestTagsList_LoadRegistryError(t *testing.T) {
	setTagsMocks(t)
	mockDefaults()
	tagsLoadRegistryFn = func(path string) (*registry.Registry, error) {
		return nil, fmt.Errorf("load error")
	}

	err := tagsListRun(&bytes.Buffer{})
	if err == nil || !strings.Contains(err.Error(), "load error") {
		t.Errorf("expected load error, got: %v", err)
	}
}

// --- tags pull ---

func TestTagsPull_WithTags(t *testing.T) {
	setTagsMocks(t)
	mockDefaults()
	tagsReadTagsFn = func(ctx context.Context) ([]string, error) {
		return []string{"tag:tsmain", "tag:shared"}, nil
	}

	var buf bytes.Buffer
	err := tagsPullRun(context.Background(), &buf)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	out := buf.String()
	if !strings.Contains(out, "REMOTE TAGS (tailnet ACL)") {
		t.Errorf("missing header, got: %s", out)
	}
	if !strings.Contains(out, "tag:tsmain") || !strings.Contains(out, "(default)") {
		t.Errorf("missing default tag marker, got: %s", out)
	}
	if !strings.Contains(out, "tag:shared") {
		t.Errorf("missing tag:shared, got: %s", out)
	}
	// tag:shared should NOT have (default)
	lines := strings.Split(out, "\n")
	for _, line := range lines {
		if strings.Contains(line, "tag:shared") && strings.Contains(line, "(default)") {
			t.Errorf("tag:shared should not be marked as default")
		}
	}
}

func TestTagsPull_Empty(t *testing.T) {
	setTagsMocks(t)
	mockDefaults()
	tagsReadTagsFn = func(ctx context.Context) ([]string, error) {
		return nil, nil
	}

	var buf bytes.Buffer
	err := tagsPullRun(context.Background(), &buf)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(buf.String(), "No tags found in tailnet ACL") {
		t.Errorf("expected no tags message, got: %s", buf.String())
	}
}

func TestTagsPull_Error(t *testing.T) {
	setTagsMocks(t)
	tagsReadTagsFn = func(ctx context.Context) ([]string, error) {
		return nil, fmt.Errorf("api error")
	}

	err := tagsPullRun(context.Background(), &bytes.Buffer{})
	if err == nil || !strings.Contains(err.Error(), "api error") {
		t.Errorf("expected api error, got: %v", err)
	}
}

// --- tags add ---

func TestTagsAdd_Success(t *testing.T) {
	setTagsMocks(t)
	mockDefaults()
	mockRegistryWithServices([]registry.Service{
		{Name: "myapp", Tags: []string{"tag:tsmain"}},
	})
	var savedSvc registry.Service
	tagsAddRegistryFn = func(path string, svc registry.Service) error {
		savedSvc = svc
		return nil
	}

	var buf bytes.Buffer
	err := tagsAddRun(&buf, "myapp", "tag:shared")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(buf.String(), "Added tag:shared to myapp") {
		t.Errorf("unexpected output: %s", buf.String())
	}
	if len(savedSvc.Tags) != 2 || savedSvc.Tags[1] != "tag:shared" {
		t.Errorf("expected tags [tag:tsmain, tag:shared], got: %v", savedSvc.Tags)
	}
}

func TestTagsAdd_Duplicate(t *testing.T) {
	setTagsMocks(t)
	mockDefaults()
	mockRegistryWithServices([]registry.Service{
		{Name: "myapp", Tags: []string{"tag:tsmain", "tag:shared"}},
	})

	var buf bytes.Buffer
	err := tagsAddRun(&buf, "myapp", "tag:shared")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(buf.String(), "tag:shared already on myapp") {
		t.Errorf("expected already-on message, got: %s", buf.String())
	}
}

func TestTagsAdd_ServiceNotFound(t *testing.T) {
	setTagsMocks(t)
	mockDefaults()
	mockRegistryWithServices(nil)

	err := tagsAddRun(&bytes.Buffer{}, "nosvc", "tag:test")
	if err == nil || !strings.Contains(err.Error(), "service not found: nosvc") {
		t.Errorf("expected service not found, got: %v", err)
	}
}

func TestTagsAdd_InvalidPrefix(t *testing.T) {
	setTagsMocks(t)

	err := tagsAddRun(&bytes.Buffer{}, "myapp", "notag")
	if err == nil || !strings.Contains(err.Error(), "tag must start with") {
		t.Errorf("expected prefix error, got: %v", err)
	}
}

func TestTagsAdd_EnsureDirError(t *testing.T) {
	setTagsMocks(t)
	tagsEnsureDirFn = func() error { return fmt.Errorf("dir error") }

	err := tagsAddRun(&bytes.Buffer{}, "myapp", "tag:test")
	if err == nil || !strings.Contains(err.Error(), "dir error") {
		t.Errorf("expected dir error, got: %v", err)
	}
}

func TestTagsAdd_RegistryPathError(t *testing.T) {
	setTagsMocks(t)
	tagsEnsureDirFn = func() error { return nil }
	tagsRegistryPathFn = func() (string, error) { return "", fmt.Errorf("path error") }

	err := tagsAddRun(&bytes.Buffer{}, "myapp", "tag:test")
	if err == nil || !strings.Contains(err.Error(), "path error") {
		t.Errorf("expected path error, got: %v", err)
	}
}

func TestTagsAdd_LoadRegistryError(t *testing.T) {
	setTagsMocks(t)
	mockDefaults()
	tagsLoadRegistryFn = func(path string) (*registry.Registry, error) {
		return nil, fmt.Errorf("load error")
	}

	err := tagsAddRun(&bytes.Buffer{}, "myapp", "tag:test")
	if err == nil || !strings.Contains(err.Error(), "load error") {
		t.Errorf("expected load error, got: %v", err)
	}
}

func TestTagsAdd_SaveError(t *testing.T) {
	setTagsMocks(t)
	mockDefaults()
	mockRegistryWithServices([]registry.Service{
		{Name: "myapp", Tags: []string{"tag:tsmain"}},
	})
	tagsAddRegistryFn = func(path string, svc registry.Service) error {
		return fmt.Errorf("save error")
	}

	err := tagsAddRun(&bytes.Buffer{}, "myapp", "tag:new")
	if err == nil || !strings.Contains(err.Error(), "save error") {
		t.Errorf("expected save error, got: %v", err)
	}
}

// --- tags set ---

func TestTagsSet_Success(t *testing.T) {
	setTagsMocks(t)
	mockDefaults()
	mockRegistryWithServices([]registry.Service{
		{Name: "myapp", Tags: []string{"tag:tsmain", "tag:old"}},
	})
	var savedSvc registry.Service
	tagsAddRegistryFn = func(path string, svc registry.Service) error {
		savedSvc = svc
		return nil
	}

	var buf bytes.Buffer
	err := tagsSetRun(&buf, "myapp", "tag:shared")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(buf.String(), "Set myapp tags to [tag:shared]") {
		t.Errorf("unexpected output: %s", buf.String())
	}
	if len(savedSvc.Tags) != 1 || savedSvc.Tags[0] != "tag:shared" {
		t.Errorf("expected tags [tag:shared], got: %v", savedSvc.Tags)
	}
}

func TestTagsSet_ServiceNotFound(t *testing.T) {
	setTagsMocks(t)
	mockDefaults()
	mockRegistryWithServices(nil)

	err := tagsSetRun(&bytes.Buffer{}, "nosvc", "tag:test")
	if err == nil || !strings.Contains(err.Error(), "service not found: nosvc") {
		t.Errorf("expected service not found, got: %v", err)
	}
}

func TestTagsSet_InvalidPrefix(t *testing.T) {
	setTagsMocks(t)

	err := tagsSetRun(&bytes.Buffer{}, "myapp", "notag")
	if err == nil || !strings.Contains(err.Error(), "tag must start with") {
		t.Errorf("expected prefix error, got: %v", err)
	}
}

func TestTagsSet_EnsureDirError(t *testing.T) {
	setTagsMocks(t)
	tagsEnsureDirFn = func() error { return fmt.Errorf("dir error") }

	err := tagsSetRun(&bytes.Buffer{}, "myapp", "tag:test")
	if err == nil || !strings.Contains(err.Error(), "dir error") {
		t.Errorf("expected dir error, got: %v", err)
	}
}

func TestTagsSet_RegistryPathError(t *testing.T) {
	setTagsMocks(t)
	tagsEnsureDirFn = func() error { return nil }
	tagsRegistryPathFn = func() (string, error) { return "", fmt.Errorf("path error") }

	err := tagsSetRun(&bytes.Buffer{}, "myapp", "tag:test")
	if err == nil || !strings.Contains(err.Error(), "path error") {
		t.Errorf("expected path error, got: %v", err)
	}
}

func TestTagsSet_LoadRegistryError(t *testing.T) {
	setTagsMocks(t)
	mockDefaults()
	tagsLoadRegistryFn = func(path string) (*registry.Registry, error) {
		return nil, fmt.Errorf("load error")
	}

	err := tagsSetRun(&bytes.Buffer{}, "myapp", "tag:test")
	if err == nil || !strings.Contains(err.Error(), "load error") {
		t.Errorf("expected load error, got: %v", err)
	}
}

func TestTagsSet_SaveError(t *testing.T) {
	setTagsMocks(t)
	mockDefaults()
	mockRegistryWithServices([]registry.Service{
		{Name: "myapp", Tags: []string{"tag:tsmain"}},
	})
	tagsAddRegistryFn = func(path string, svc registry.Service) error {
		return fmt.Errorf("save error")
	}

	err := tagsSetRun(&bytes.Buffer{}, "myapp", "tag:new")
	if err == nil || !strings.Contains(err.Error(), "save error") {
		t.Errorf("expected save error, got: %v", err)
	}
}

// --- tags set-default ---

func TestTagsSetDefault_Success(t *testing.T) {
	setTagsMocks(t)
	t.Setenv("HOME", t.TempDir())

	var savedCfg config.GlobalConfig
	tagsLoadGlobalFn = func() (config.GlobalConfig, error) {
		return config.GlobalConfig{}, nil
	}
	tagsSaveGlobalFn = func(cfg config.GlobalConfig) error {
		savedCfg = cfg
		return nil
	}

	var buf bytes.Buffer
	err := tagsSetDefaultRun(&buf, "tag:myteam")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(buf.String(), "Default tag set to tag:myteam") {
		t.Errorf("unexpected output: %s", buf.String())
	}
	if savedCfg.DefaultTag != "tag:myteam" {
		t.Errorf("expected default tag tag:myteam, got: %s", savedCfg.DefaultTag)
	}
}

func TestTagsSetDefault_InvalidPrefix(t *testing.T) {
	setTagsMocks(t)

	err := tagsSetDefaultRun(&bytes.Buffer{}, "notag")
	if err == nil || !strings.Contains(err.Error(), "tag must start with") {
		t.Errorf("expected prefix error, got: %v", err)
	}
}

func TestTagsSetDefault_LoadError(t *testing.T) {
	setTagsMocks(t)
	tagsLoadGlobalFn = func() (config.GlobalConfig, error) {
		return config.GlobalConfig{}, fmt.Errorf("load error")
	}

	err := tagsSetDefaultRun(&bytes.Buffer{}, "tag:test")
	if err == nil || !strings.Contains(err.Error(), "load error") {
		t.Errorf("expected load error, got: %v", err)
	}
}

func TestTagsSetDefault_SaveError(t *testing.T) {
	setTagsMocks(t)
	tagsLoadGlobalFn = func() (config.GlobalConfig, error) {
		return config.GlobalConfig{}, nil
	}
	tagsSaveGlobalFn = func(cfg config.GlobalConfig) error {
		return fmt.Errorf("save error")
	}

	err := tagsSetDefaultRun(&bytes.Buffer{}, "tag:test")
	if err == nil || !strings.Contains(err.Error(), "save error") {
		t.Errorf("expected save error, got: %v", err)
	}
}

// --- tags delete-remote ---

func TestTagsDeleteRemote_Success(t *testing.T) {
	setTagsMocks(t)
	mockDefaults()
	mockRegistryWithServices([]registry.Service{
		{Name: "myapp", Tags: []string{"tag:tsmain"}},
	})
	deleted := false
	tagsDeleteTagFn = func(ctx context.Context, tag string) error {
		deleted = true
		return nil
	}

	var buf bytes.Buffer
	err := tagsDeleteRemoteRun(context.Background(), &buf, "tag:shared")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !deleted {
		t.Error("expected DeleteTag to be called")
	}
	if !strings.Contains(buf.String(), "Deleted tag:shared from tailnet ACL") {
		t.Errorf("unexpected output: %s", buf.String())
	}
}

func TestTagsDeleteRemote_TagInUse(t *testing.T) {
	setTagsMocks(t)
	mockDefaults()
	mockRegistryWithServices([]registry.Service{
		{Name: "myapp", Tags: []string{"tag:shared"}},
		{Name: "dashboard", Tags: []string{"tag:shared", "tag:tsmain"}},
	})

	err := tagsDeleteRemoteRun(context.Background(), &bytes.Buffer{}, "tag:shared")
	if err == nil {
		t.Fatal("expected error for tag in use")
	}
	if !strings.Contains(err.Error(), "in use by services") {
		t.Errorf("expected in-use error, got: %v", err)
	}
	if !strings.Contains(err.Error(), "myapp") || !strings.Contains(err.Error(), "dashboard") {
		t.Errorf("expected service names in error, got: %v", err)
	}
}

func TestTagsDeleteRemote_DefaultTag(t *testing.T) {
	setTagsMocks(t)
	mockDefaults()

	err := tagsDeleteRemoteRun(context.Background(), &bytes.Buffer{}, "tag:tsmain")
	if err == nil {
		t.Fatal("expected error for default tag")
	}
	if !strings.Contains(err.Error(), "cannot delete default tag") {
		t.Errorf("expected default-tag error, got: %v", err)
	}
}

func TestTagsDeleteRemote_InvalidPrefix(t *testing.T) {
	setTagsMocks(t)

	err := tagsDeleteRemoteRun(context.Background(), &bytes.Buffer{}, "notag")
	if err == nil || !strings.Contains(err.Error(), "tag must start with") {
		t.Errorf("expected prefix error, got: %v", err)
	}
}

func TestTagsDeleteRemote_DeleteError(t *testing.T) {
	setTagsMocks(t)
	mockDefaults()
	mockRegistryWithServices(nil)
	tagsDeleteTagFn = func(ctx context.Context, tag string) error {
		return fmt.Errorf("tag %q not found in tailnet ACL", tag)
	}

	err := tagsDeleteRemoteRun(context.Background(), &bytes.Buffer{}, "tag:gone")
	if err == nil || !strings.Contains(err.Error(), "not found") {
		t.Errorf("expected not-found error, got: %v", err)
	}
}

func TestTagsDeleteRemote_RegistryPathError(t *testing.T) {
	setTagsMocks(t)
	tagsGetDefaultFn = func() string { return "tag:tsmain" }
	tagsRegistryPathFn = func() (string, error) { return "", fmt.Errorf("path error") }

	err := tagsDeleteRemoteRun(context.Background(), &bytes.Buffer{}, "tag:other")
	if err == nil || !strings.Contains(err.Error(), "path error") {
		t.Errorf("expected path error, got: %v", err)
	}
}

func TestTagsDeleteRemote_LoadRegistryError(t *testing.T) {
	setTagsMocks(t)
	mockDefaults()
	tagsLoadRegistryFn = func(path string) (*registry.Registry, error) {
		return nil, fmt.Errorf("load error")
	}

	err := tagsDeleteRemoteRun(context.Background(), &bytes.Buffer{}, "tag:other")
	if err == nil || !strings.Contains(err.Error(), "load error") {
		t.Errorf("expected load error, got: %v", err)
	}
}

// --- Integration: test via cobra command tree ---

func runTagsCmd(t *testing.T, args ...string) (string, error) {
	t.Helper()
	cmd, _, err := rootCmd.Find(append([]string{"tags"}, args...))
	if err != nil {
		t.Fatalf("find tags command: %v", err)
	}
	var buf bytes.Buffer
	cmd.SetOut(&buf)
	cmd.SetErr(&buf)

	// Build the full args for rootCmd execution
	rootCmd.SetArgs(append([]string{"tags"}, args...))
	execErr := rootCmd.Execute()
	return buf.String(), execErr
}

func TestTagsCmd_ListIntegration(t *testing.T) {
	setTagsMocks(t)

	tmpDir := t.TempDir()
	t.Setenv("HOME", tmpDir)

	regPath := filepath.Join(tmpDir, ".config", "tslink", "registry.json")
	os.MkdirAll(filepath.Dir(regPath), 0o700)
	os.WriteFile(regPath, []byte(`{"services":[{"name":"app1","type":"proxy","tags":["tag:tsmain"]}]}`), 0o600)

	// Use real functions for integration
	tagsRegistryPathFn = func() (string, error) { return regPath, nil }

	var buf bytes.Buffer
	err := tagsListRun(&buf)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(buf.String(), "app1") || !strings.Contains(buf.String(), "tag:tsmain") {
		t.Errorf("unexpected output: %s", buf.String())
	}
}

// --- validateTagPrefix ---

func TestValidateTagPrefix(t *testing.T) {
	tests := []struct {
		tag     string
		wantErr bool
	}{
		{"tag:valid", false},
		{"tag:", false},
		{"notag", true},
		{"", true},
		{"Tag:case", true},
	}
	for _, tt := range tests {
		err := validateTagPrefix(tt.tag)
		if (err != nil) != tt.wantErr {
			t.Errorf("validateTagPrefix(%q) error=%v, wantErr=%v", tt.tag, err, tt.wantErr)
		}
	}
}

// --- findService ---

func TestFindService(t *testing.T) {
	reg := &registry.Registry{
		Services: []registry.Service{
			{Name: "a"}, {Name: "b"}, {Name: "c"},
		},
	}
	idx, err := findService(reg, "b")
	if err != nil || idx != 1 {
		t.Errorf("expected idx=1, got idx=%d, err=%v", idx, err)
	}

	_, err = findService(reg, "missing")
	if err == nil || !strings.Contains(err.Error(), "service not found") {
		t.Errorf("expected not found error, got: %v", err)
	}
}
