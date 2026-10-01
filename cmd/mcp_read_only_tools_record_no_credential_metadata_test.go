package cmd

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/anydoor7/tslink/internal/config"
	"github.com/anydoor7/tslink/internal/credentials"
	"github.com/anydoor7/tslink/internal/inspect"
	"github.com/anydoor7/tslink/internal/registry"
	"github.com/anydoor7/tslink/internal/tailapi"
	"github.com/zalando/go-keyring"
)

// readOnlyFixtureAPIKey is the fake API key these tests store. It exists only
// in the test keyring.
const readOnlyFixtureAPIKey = "tskey-api-FAKE-read-only-fixture"

// storeCredentialWithoutMetadata empties the test keyring, stores one fake API
// key in it, and points TSLINK_CONFIG_DIR at a directory that does not exist
// yet: no credential-meta.json and no credentials.lock, the state in which a
// status read backfills the key's metadata. Storing the key takes
// credentials.lock, so the key is stored under another config directory. The
// keyring is emptied again when the test ends, so no later test finds the key.
func storeCredentialWithoutMetadata(t *testing.T) string {
	t.Helper()
	keyring.MockInit()
	t.Cleanup(keyring.MockInit)
	t.Setenv(config.ConfigDirEnv, t.TempDir())
	if err := credentials.SetAPIKey(readOnlyFixtureAPIKey); err != nil {
		t.Fatal(err)
	}
	configDir := filepath.Join(t.TempDir(), "config")
	t.Setenv(config.ConfigDirEnv, configDir)
	return configDir
}

// readOnlyCredentialFixture is storeCredentialWithoutMetadata plus a registry
// holding one service and a log file, with every reach beyond this machine
// stubbed: the local tailscaled read of doctor and, now that a key is stored,
// the Tailscale API read of invite_list. It returns the config directory and
// every root the tools could write under.
func readOnlyCredentialFixture(t *testing.T) (string, sharePaths, []string) {
	t.Helper()
	stubDoctorTailscaleSSH(t, false, nil)
	restoreShareSeams(t)
	configDir := storeCredentialWithoutMetadata(t)
	paths := mcpSharePaths(t)
	if _, err := registry.Add(paths.Registry, registry.Service{Name: "web", Type: registry.TypeProxy, Target: "http://localhost:3000"}); err != nil {
		t.Fatal(err)
	}
	oldInviteList := inviteListFn
	t.Cleanup(func() { inviteListFn = oldInviteList })
	inviteListFn = func(context.Context, []tailapi.DeviceTarget) (tailapi.InviteList, error) {
		return tailapi.InviteList{}, nil
	}
	logDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(logDir, "tslink.err.log"), []byte("time=2026-09-29T12:00:00Z level=INFO msg=started\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	oldLogDir := logsLogDirFn
	t.Cleanup(func() { logsLogDirFn = oldLogDir })
	logsLogDirFn = func() (string, error) { return logDir, nil }
	return configDir, paths, []string{configDir, filepath.Dir(paths.Registry), logDir}
}

// assertTreeUnchanged fails for every file created, changed or removed under
// the roots between the two digests.
func assertTreeUnchanged(t *testing.T, what string, before, after map[string]string) {
	t.Helper()
	for path, value := range after {
		if before[path] != value {
			t.Errorf("%s changed %s", what, path)
		}
	}
	for path := range before {
		if _, kept := after[path]; !kept {
			t.Errorf("%s removed %s", what, path)
		}
	}
}

// TestMCPReadOnlyToolsDescribeAStoredCredentialWithoutRecordingIt is B7-1
// (SHUF-1). list, status, url and doctor declare readOnlyHint, yet they read
// status through the credential inventory that records a metadata backfill:
// while a stored credential had no metadata, the first of them to run wrote
// credential-meta.json and created credentials.lock. Each read-only tool, and
// the event stream built from list and status, now runs against its own store
// in exactly that state and must leave every root as it found it. Where the
// result reports the credential, it must still see it.
func TestMCPReadOnlyToolsDescribeAStoredCredentialWithoutRecordingIt(t *testing.T) {
	arguments := map[string]string{"url": `{"name":"web"}`, "access_explain": `{"service":"web"}`, "template_plan": `{"name":"local-web"}`}
	readOnly := 0
	for _, definition := range mcpToolDefinitions {
		if hints := mcpToolHints[definition.Name]; hints == nil || !hints.ReadOnlyHint {
			continue
		}
		readOnly++
		t.Run(definition.Name, func(t *testing.T) {
			_, paths, roots := readOnlyCredentialFixture(t)
			args, ok := arguments[definition.Name]
			if !ok {
				args = mcpToolMinimalArguments[definition.Name]
			}
			before := configTreeDigest(t, roots...)
			call := `{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"` + definition.Name + `","arguments":` + args + `}}`
			frame := mcpFrameByID(t, decodeMCPResponses(t, runMCPSession(t, initializedMCPInput(call), defaultMCPActions(paths, io.Discard))), float64(2))
			result, _ := frame["result"].(map[string]any)
			if result == nil {
				t.Fatalf("no tool result: %v", frame)
			}
			assertTreeUnchanged(t, "read-only tool "+definition.Name, before, configTreeDigest(t, roots...))
			structured, _ := result["structuredContent"].(map[string]any)
			switch definition.Name {
			case "status":
				if structured["credential_stored"] != true {
					t.Fatalf("control: status does not see the stored key: %v", result)
				}
			case "doctor":
				if !doctorResultHasFinding(structured, inspect.WarningCodeCredentialMetaBackfilled) {
					t.Fatalf("control: doctor did not classify the stored key: %v", result)
				}
			}
		})
	}
	if readOnly != 10 {
		t.Fatalf("%d tools are marked read-only, want the 10 that only read", readOnly)
	}
	t.Run("event stream", func(t *testing.T) {
		_, paths, roots := readOnlyCredentialFixture(t)
		before := configTreeDigest(t, roots...)
		state, err := mcpEventsSnapshotFn(defaultMCPActions(paths, io.Discard))(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		assertTreeUnchanged(t, "event stream snapshot", before, configTreeDigest(t, roots...))
		if event, _ := state.(mcpEventState); !event.Status.CredentialStored {
			t.Fatalf("control: the event stream does not see the stored key: %+v", state)
		}
	})
}

func doctorResultHasFinding(structured map[string]any, code string) bool {
	findings, _ := structured["findings"].([]any)
	for _, raw := range findings {
		if finding, _ := raw.(map[string]any); finding["code"] == code {
			return true
		}
	}
	return false
}

// TestStatusReportingCommandsStillRecordTheBackfill is the other half of B7-1
// and the control for the test above: the same store, read the way tslink
// status, tslink status --urls and tslink doctor read it, records the backfill
// and creates credentials.lock, as README's config-directory section says. So
// the read-only tools leave the config directory alone because they read it
// differently, not because the fixture gives them nothing to record.
func TestStatusReportingCommandsStillRecordTheBackfill(t *testing.T) {
	for _, command := range []struct {
		name string
		read func(sharePaths) error
	}{
		{"tslink status", func(p sharePaths) error {
			_, err := getPollableStatus(p.PID, p.Registry, p.Snapshot, p.AuthHandoff)
			return err
		}},
		{"tslink status --urls", func(p sharePaths) error {
			_, err := getStatusURLsWithAuth(p.PID, p.Registry, p.Snapshot, p.AuthHandoff)
			return err
		}},
		{"tslink doctor", func(p sharePaths) error {
			buildDoctorResult(doctorOptions{RegistryPath: p.Registry, PIDPath: p.PID, RuntimeSnapshotPath: p.Snapshot, AuthHandoffPath: p.AuthHandoff})
			return nil
		}},
	} {
		t.Run(command.name, func(t *testing.T) {
			configDir, paths, _ := readOnlyCredentialFixture(t)
			if err := command.read(paths); err != nil {
				t.Fatal(err)
			}
			if _, err := os.Stat(filepath.Join(configDir, "credentials.lock")); err != nil {
				t.Fatalf("credentials.lock after %s: %v", command.name, err)
			}
			doc, err := credentials.LoadMetadata()
			if err != nil {
				t.Fatal(err)
			}
			if meta, ok := doc.Slots[credentials.SlotAPIKey]; !ok || meta.Fingerprint != credentials.Fingerprint(readOnlyFixtureAPIKey) {
				t.Fatalf("credential-meta.json after %s = %+v, want the stored key's backfill", command.name, doc.Slots)
			}
		})
	}
}
