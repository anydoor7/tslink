package cmd

import (
	"bytes"
	"context"
	"io"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/anydoor7/tslink/internal/config"
	"github.com/anydoor7/tslink/internal/filelock"
	"github.com/anydoor7/tslink/internal/output"
	"github.com/anydoor7/tslink/internal/registry"
	"github.com/anydoor7/tslink/internal/testenv"
)

func writeGlobalConfigFixture(t *testing.T, raw string) string {
	t.Helper()
	testenv.SetHome(t, t.TempDir())
	path, err := config.ConfigPath()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(raw), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

// TestAddAndShareFailClosedOnAnUnreadableConfig is A1's run1: with a trailing
// comma or a default_tags typo in config.json, add used to persist
// tag:tsmain into registry.json.
func TestAddAndShareFailClosedOnAnUnreadableConfig(t *testing.T) {
	for name, raw := range map[string]string{
		"trailing comma": `{"default_tag":"tag:web",}`,
		"key typo":       `{"default_tags":"tag:web"}`,
	} {
		t.Run(name, func(t *testing.T) {
			writeGlobalConfigFixture(t, raw)
			regPath := stubAddWritePaths(t)
			seed := `{"schema_version":1,"services":[]}` + "\n"
			if err := os.WriteFile(regPath, []byte(seed), 0o600); err != nil {
				t.Fatal(err)
			}

			err := runAddCmd(t, []string{"api"}, map[string]string{"proxy": "localhost:3000"})
			if code, _ := registry.ErrorCode(err); code != registry.CodeConfigLoadFailed || output.ExitCode(err) != output.ExitUsage {
				t.Fatalf("add error = %v (exit %d), want %s with exit %d", err, output.ExitCode(err), registry.CodeConfigLoadFailed, output.ExitUsage)
			}
			_, regPathShare := shareMCPWireActions(t)
			_, err = executeShare(context.Background(), sharePaths{Registry: regPathShare}, shareRequest{Target: "3000", Ephemeral: true}, time.Second, io.Discard)
			if code, _ := registry.ErrorCode(err); code != registry.CodeConfigLoadFailed {
				t.Fatalf("share error = %v, want %s", err, registry.CodeConfigLoadFailed)
			}

			if data, _ := os.ReadFile(regPath); string(data) != seed {
				t.Fatalf("registry.json = %s, want it unchanged", data)
			}
			if _, err := os.Stat(regPathShare); !os.IsNotExist(err) {
				t.Fatalf("share wrote %s: %v", regPathShare, err)
			}
		})
	}
}

func TestAddUsesTheConfiguredDefaultTag(t *testing.T) {
	writeGlobalConfigFixture(t, `{"default_tag":"tag:web"}`)
	regPath := stubAddWritePaths(t)
	if err := runAddCmd(t, []string{"api"}, map[string]string{"proxy": "localhost:3000"}); err != nil {
		t.Fatal(err)
	}
	reg, err := registry.Load(regPath)
	if err != nil || len(reg.Services) != 1 || len(reg.Services[0].Tags) != 1 || reg.Services[0].Tags[0] != "tag:web" {
		t.Fatalf("registry = %+v, %v; want api with tag:web", reg, err)
	}
}

// TestConfigWritersRefuseToDropUnknownKeys: config set used to rewrite
// config.json from the typed struct and drop keys it did not know.
func TestConfigWritersRefuseToDropUnknownKeys(t *testing.T) {
	raw := `{"default_tag":"tag:web","future_key":{"x":1}}`
	path := writeGlobalConfigFixture(t, raw)
	setTagsMocks(t)
	if err := configSet("control-url", "https://headscale.example.com", io.Discard, false); err == nil {
		t.Fatal("config set on a config.json with an unknown key succeeded")
	} else if code, _ := registry.ErrorCode(err); code != registry.CodeConfigLoadFailed {
		t.Fatalf("config set error = %v, want %s", err, registry.CodeConfigLoadFailed)
	}
	if err := tagsSetDefaultRun(io.Discard, "tag:other", false); err == nil {
		t.Fatal("tags set-default on a config.json with an unknown key succeeded")
	}
	if data, _ := os.ReadFile(path); string(data) != raw {
		t.Fatalf("config.json = %s, want it untouched", data)
	}
}

// TestConfigWritersTakeTheConfigLock pins that both writers serialize on
// config.json.lock: while the test holds it, neither can finish, and once it
// is released both values are kept.
func TestConfigWritersTakeTheConfigLock(t *testing.T) {
	path := writeGlobalConfigFixture(t, `{}`)
	setTagsMocks(t)
	lockFile, err := os.OpenFile(path+".lock", os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	defer lockFile.Close()
	if err := filelock.Lock(lockFile); err != nil {
		t.Fatal(err)
	}

	done := make(chan error, 2)
	go func() { done <- configSet("control-url", "https://headscale.example.com", io.Discard, false) }()
	go func() { done <- tagsSetDefaultRun(&bytes.Buffer{}, "tag:web", false) }()
	select {
	case err := <-done:
		t.Fatalf("a writer finished while config.json.lock was held: %v", err)
	case <-time.After(300 * time.Millisecond):
	}
	if err := filelock.Unlock(lockFile); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		select {
		case err := <-done:
			if err != nil {
				t.Fatalf("writer failed after the lock was released: %v", err)
			}
		case <-time.After(10 * time.Second):
			t.Fatal("writer did not finish after the lock was released")
		}
	}
	cfg, err := config.LoadGlobalConfig()
	if err != nil || cfg.ControlURL != "https://headscale.example.com" || cfg.DefaultTag != "tag:web" {
		t.Fatalf("config = %+v, %v; want both writers' values", cfg, err)
	}
}

// TestLoginFailsClosedBeforeStoringOnAnUnreadableConfig: login resolves the
// default tag it plans ACL writes and the client-secret validation node for
// before any credential is verified or stored.
func TestLoginFailsClosedBeforeStoringOnAnUnreadableConfig(t *testing.T) {
	dir := setupLoginTest(t)
	resetLoginFlags(t)
	t.Cleanup(func() { resetLoginFlags(t) })
	if err := os.WriteFile(filepath.Join(dir, ".config", "tslink", "config.json"), []byte(`{"default_tags":"tag:web"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	oldVerify, oldActivate := loginVerifyAPIKeyFn, loginActivateClientSecretFn
	t.Cleanup(func() { loginVerifyAPIKeyFn, loginActivateClientSecretFn = oldVerify, oldActivate })
	reached := 0
	loginVerifyAPIKeyFn = func(context.Context, string) error { reached++; return nil }
	loginActivateClientSecretFn = func(context.Context, string) error { reached++; return nil }

	if err := loginWithAPIKey(loginCmd, "tskey-api-<test-only-synthetic>"); registryCode(err) != registry.CodeConfigLoadFailed {
		t.Fatalf("login --api-key error = %v, want %s", err, registry.CodeConfigLoadFailed)
	}
	if err := loginWithClientSecret(loginCmd, "tskey-client-synthetic"); registryCode(err) != registry.CodeConfigLoadFailed {
		t.Fatalf("login --client-secret error = %v, want %s", err, registry.CodeConfigLoadFailed)
	}
	if reached != 0 {
		t.Fatalf("login verified %d candidate credentials before refusing the config", reached)
	}
}

func registryCode(err error) string {
	code, _ := registry.ErrorCode(err)
	return code
}
