package cmd

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/anydoor7/tslink/internal/config"
	"github.com/anydoor7/tslink/internal/credentials"
	"github.com/anydoor7/tslink/internal/server"
)

// The daemon refuses a minted key for a node on another control server unless
// serve declares the stored credential a key the user supplied. The server
// type must accept that declaration.
var _ userSuppliedAuthKeySetter = (*server.Server)(nil)

type mockServerRecordingUserSuppliedKey struct {
	mockServer
	userSupplied []bool
}

func (m *mockServerRecordingUserSuppliedKey) SetUserSuppliedAuthKey(userSupplied bool) {
	m.userSupplied = append(m.userSupplied, userSupplied)
}

// serve passes the stored credential's kind to the daemon: only the legacy
// authkey file is a key the user supplied.
func TestServeDeclaresOnlyTheLegacyAuthKeyFileAsUserSupplied(t *testing.T) {
	for _, tc := range []struct {
		name         string
		userSupplied bool
	}{
		{name: "minting credential", userSupplied: false},
		{name: "legacy authkey file", userSupplied: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			mockServeDefaults(t, dir)
			old := serveUserSuppliedAuthKeyFn
			t.Cleanup(func() { serveUserSuppliedAuthKeyFn = old })
			serveUserSuppliedAuthKeyFn = func() (bool, error) { return tc.userSupplied, nil }
			mock := &mockServerRecordingUserSuppliedKey{}
			serveNewServerFn = func(string, string) (serverRunner, error) { return mock, nil }

			cmd := findServeCmd(t)
			if err := cmd.RunE(cmd, nil); err != nil {
				t.Fatalf("RunE() error = %v", err)
			}
			if len(mock.userSupplied) != 1 || mock.userSupplied[0] != tc.userSupplied {
				t.Fatalf("SetUserSuppliedAuthKey calls = %v, want [%v]", mock.userSupplied, tc.userSupplied)
			}
		})
	}
}

// The default answer follows credentials.GetAuthKey's order against the real
// credential stores of an isolated config directory (the mock keyring and the
// credential files there).
func TestServeUserSuppliedAuthKeyDefaultFollowsCredentialOrder(t *testing.T) {
	for _, tc := range []struct {
		name  string
		setup func(t *testing.T, dir string)
		want  bool
	}{
		{name: "legacy authkey file only", want: true, setup: func(t *testing.T, dir string) {
			if err := os.WriteFile(filepath.Join(dir, "authkey"), []byte("tskey-auth-<testonly_USER>-<testonly_SUPPLIED>"), 0o600); err != nil {
				t.Fatal(err)
			}
		}},
		{name: "API access token beside the legacy file", want: false, setup: func(t *testing.T, dir string) {
			if err := os.WriteFile(filepath.Join(dir, "authkey"), []byte("tskey-auth-<testonly_USER>-<testonly_SUPPLIED>"), 0o600); err != nil {
				t.Fatal(err)
			}
			if err := credentials.SetAPIKey("tskey-api-<test-only-TEST-PLACEHOLDER>"); err != nil {
				t.Fatal(err)
			}
		}},
		{name: "OAuth client secret", want: false, setup: func(t *testing.T, dir string) {
			if err := credentials.SaveClientSecret("tskey-client-<testonly_TEST>-<testonly_PLACEHOLDER>"); err != nil {
				t.Fatal(err)
			}
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			mockServeDefaults(t, dir)
			cfgDir, err := config.Dir()
			if err != nil {
				t.Fatal(err)
			}
			if err := config.EnsureDir(); err != nil {
				t.Fatal(err)
			}
			tc.setup(t, cfgDir)
			got, err := serveUserSuppliedAuthKeyFn()
			if err != nil || got != tc.want {
				t.Fatalf("serveUserSuppliedAuthKeyFn() = %v, %v; want %v", got, err, tc.want)
			}
		})
	}
}
