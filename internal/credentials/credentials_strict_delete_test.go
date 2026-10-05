package credentials

import (
	"errors"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/zalando/go-keyring"
)

// These paths decide whether TSLink may claim a credential is gone. Reporting a
// residual credential as absent is the failure that matters: logout and the
// login credential swap both treat "absent" as proof.

func skipIfRootCannotBeDeniedAccess(t *testing.T) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("POSIX directory permissions do not gate stat on Windows")
	}
	if os.Geteuid() == 0 {
		t.Skip("running as root bypasses directory permission denial")
	}
}

func TestInspectCredentialStrictReportsCredentialsThatArePresent(t *testing.T) {
	setup(t)
	if err := keyring.Set(keychainService, keychainAPIKey, "tskey-api-<test-only-fake-inspect>"); err != nil {
		t.Fatalf("keyring.Set() error = %v", err)
	}
	if err := os.WriteFile(clientSecretPath(t), []byte("tskey-client-<testonly_fake>-<testonly_inspect>\n"), 0o600); err != nil {
		t.Fatalf("WriteFile(client secret) error = %v", err)
	}

	status, err := InspectStoredCredentialsStrict()
	if err != nil {
		t.Fatalf("InspectStoredCredentialsStrict() error = %v", err)
	}
	if !status.APIKey.Keyring.Present {
		t.Fatal("keyring-stored API key reported absent; a residual credential would be invisible to cleanup")
	}
	if status.APIKey.File.Present {
		t.Fatal("API key file reported present although only the keyring holds it")
	}
	if !status.ClientSecret.File.Present {
		t.Fatal("file-stored client secret reported absent; a residual credential would be invisible to cleanup")
	}
	if !status.AnyPresent() {
		t.Fatal("AnyPresent() = false with two stored credentials")
	}
	if !status.APIKey.Keyring.Enabled || !status.APIKey.File.Enabled {
		t.Fatalf("status = %+v, want both stores reported enabled", status)
	}
}

func TestInspectCredentialStrictTreatsBlankStoredValuesAsAbsent(t *testing.T) {
	setup(t)
	if err := keyring.Set(keychainService, keychainAPIKey, "   "); err != nil {
		t.Fatalf("keyring.Set() error = %v", err)
	}
	if err := os.WriteFile(apiKeyPath(t), []byte("  \n\t"), 0o600); err != nil {
		t.Fatalf("WriteFile(api key) error = %v", err)
	}

	status, err := inspectCredentialStrict("API key", keychainAPIKey, apiKeyPathFunc)
	if err != nil {
		t.Fatalf("inspectCredentialStrict() error = %v", err)
	}
	if status.Keyring.Present || status.File.Present {
		t.Fatalf("status = %+v, want whitespace-only values treated as absent", status)
	}
}

func TestInspectCredentialStrictFailsClosedWhenKeyringIsDisabled(t *testing.T) {
	setup(t)
	if err := os.WriteFile(apiKeyPath(t), []byte("tskey-api-<test-only-fake-file>\n"), 0o600); err != nil {
		t.Fatalf("WriteFile(api key) error = %v", err)
	}
	oldEnabled := keyringEnabledFunc
	t.Cleanup(func() { keyringEnabledFunc = oldEnabled })
	keyringEnabledFunc = func() bool { return false }

	status, err := inspectCredentialStrict("API key", keychainAPIKey, apiKeyPathFunc)
	if err == nil {
		t.Fatal("inspectCredentialStrict() error = nil; a disabled keyring cannot prove absence")
	}
	if !strings.Contains(err.Error(), "keyring disabled") || !strings.Contains(err.Error(), "cannot prove") {
		t.Fatalf("inspectCredentialStrict() error = %v, want disabled-keyring residual-risk wording", err)
	}
	if status.Keyring.Enabled {
		t.Fatalf("status = %+v, want Keyring.Enabled false", status)
	}
	if !status.File.Present {
		t.Fatal("file credential was not inspected after the keyring was reported disabled")
	}
	if strings.Contains(err.Error(), "tskey-") {
		t.Fatalf("inspectCredentialStrict() leaked credential-looking material: %v", err)
	}
}

func TestInspectCredentialStrictSurfacesPathResolutionFailure(t *testing.T) {
	setup(t)
	failing := func() (string, error) { return "", errors.New("synthetic path failure") }

	status, err := inspectCredentialStrict("API key", keychainAPIKey, failing)
	if err == nil {
		t.Fatal("inspectCredentialStrict() error = nil, want the path failure surfaced")
	}
	if !strings.Contains(err.Error(), "API key file path") {
		t.Fatalf("inspectCredentialStrict() error = %v, want file-path context", err)
	}
	if status.File.Present {
		t.Fatalf("status = %+v, want File.Present false when the path is unknown", status)
	}
}

func TestInspectCredentialStrictReportsUnreadableFileAsErrorNotAbsence(t *testing.T) {
	setup(t)
	// A directory (or anything non-regular) at the credential path must not be
	// silently read as "no credential stored".
	if err := os.MkdirAll(apiKeyPath(t), 0o700); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}

	status, err := inspectCredentialStrict("API key", keychainAPIKey, apiKeyPathFunc)
	if err == nil {
		t.Fatal("inspectCredentialStrict() error = nil for an unreadable credential file")
	}
	if !strings.Contains(err.Error(), "API key file unreadable") {
		t.Fatalf("inspectCredentialStrict() error = %v, want file-unreadable context", err)
	}
	if status.File.Present {
		t.Fatalf("status = %+v, want File.Present false for an unreadable file", status)
	}
}

func TestDeleteCredentialFilePathStrictReportsAResidualCredentialFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "apikey")
	// A non-empty directory cannot be removed, so the readback must catch that
	// something is still sitting at the credential path.
	if err := os.MkdirAll(filepath.Join(path, "occupied"), 0o700); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}

	err := deleteCredentialFilePathStrict("API key", path)
	if err == nil {
		t.Fatal("deleteCredentialFilePathStrict() error = nil while the credential path still exists")
	}
	if !strings.Contains(err.Error(), "API key file delete") {
		t.Fatalf("error = %v, want the delete failure reported", err)
	}
	if !strings.Contains(err.Error(), "still present after cleanup") {
		t.Fatalf("error = %v, want the readback to prove the credential is still present", err)
	}
}

func TestDeleteCredentialFilePathStrictReportsAnUnreadableReadback(t *testing.T) {
	skipIfRootCannotBeDeniedAccess(t)
	dir := filepath.Join(t.TempDir(), "locked")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	path := filepath.Join(dir, "apikey")
	if err := os.WriteFile(path, []byte("tskey-api-<test-only-fake-residual>\n"), 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	if err := os.Chmod(dir, 0o000); err != nil {
		t.Fatalf("Chmod() error = %v", err)
	}
	t.Cleanup(func() { _ = os.Chmod(dir, 0o700) })

	err := deleteCredentialFilePathStrict("API key", path)
	if err == nil {
		t.Fatal("deleteCredentialFilePathStrict() error = nil although deletion could not be proved")
	}
	if !strings.Contains(err.Error(), "API key file readback") {
		t.Fatalf("error = %v, want the unreadable readback reported", err)
	}
	if strings.Contains(err.Error(), "tskey-") {
		t.Fatalf("deleteCredentialFilePathStrict() leaked credential-looking material: %v", err)
	}
}

func TestDeleteCredentialFilePathStrictSucceedsForAnAbsentOrRemovableFile(t *testing.T) {
	dir := t.TempDir()

	absent := filepath.Join(dir, "absent")
	if err := deleteCredentialFilePathStrict("API key", absent); err != nil {
		t.Fatalf("deleteCredentialFilePathStrict(absent) error = %v, want nil", err)
	}

	present := filepath.Join(dir, "apikey")
	if err := os.WriteFile(present, []byte("tskey-api-<test-only-fake-removable>\n"), 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	if err := deleteCredentialFilePathStrict("API key", present); err != nil {
		t.Fatalf("deleteCredentialFilePathStrict(present) error = %v, want nil", err)
	}
	if _, statErr := os.Stat(present); !os.IsNotExist(statErr) {
		t.Fatalf("credential file survived deletion, stat err = %v", statErr)
	}
}

type checkedDeleteCase struct {
	name        string
	del         func() error
	keychainKey string
	path        func(*testing.T) string
	setPathFunc func(func() (string, error)) func()
}

func checkedDeleteCases() []checkedDeleteCase {
	return []checkedDeleteCase{
		{
			name:        "api key",
			del:         DeleteAPIKeyChecked,
			keychainKey: keychainAPIKey,
			path:        apiKeyPath,
			setPathFunc: func(fn func() (string, error)) func() {
				old := apiKeyPathFunc
				apiKeyPathFunc = fn
				return func() { apiKeyPathFunc = old }
			},
		},
		{
			name:        "client secret",
			del:         DeleteClientSecretChecked,
			keychainKey: keychainClientSecret,
			path:        clientSecretPath,
			setPathFunc: func(fn func() (string, error)) func() {
				old := clientSecretPathFunc
				clientSecretPathFunc = fn
				return func() { clientSecretPathFunc = old }
			},
		},
	}
}

func TestCheckedCredentialDeleteRemovesBothStoresAndReportsSuccess(t *testing.T) {
	for _, tc := range checkedDeleteCases() {
		t.Run(tc.name, func(t *testing.T) {
			setup(t)
			if err := keyring.Set(keychainService, tc.keychainKey, "tskey-fake-stored"); err != nil {
				t.Fatalf("keyring.Set() error = %v", err)
			}
			if err := os.WriteFile(tc.path(t), []byte("tskey-fake-stored\n"), 0o600); err != nil {
				t.Fatalf("WriteFile() error = %v", err)
			}

			if err := tc.del(); err != nil {
				t.Fatalf("checked delete error = %v, want nil", err)
			}
			if _, err := keyring.Get(keychainService, tc.keychainKey); !errors.Is(err, keyring.ErrNotFound) {
				t.Fatalf("keyring.Get() error = %v, want ErrNotFound after checked delete", err)
			}
			if _, err := os.Stat(tc.path(t)); !os.IsNotExist(err) {
				t.Fatalf("credential file survived checked delete, stat err = %v", err)
			}
		})
	}
}

func TestCheckedCredentialDeleteSurfacesKeyringDeleteFailure(t *testing.T) {
	for _, tc := range checkedDeleteCases() {
		t.Run(tc.name, func(t *testing.T) {
			setup(t)
			stubKeyring(t, nil, nil, func(string, string) error {
				return errors.New("synthetic keyring delete failure")
			})

			err := tc.del()
			if err == nil {
				t.Fatal("checked delete error = nil while the keyring copy could not be removed")
			}
			if !strings.Contains(err.Error(), "synthetic keyring delete failure") {
				t.Fatalf("checked delete error = %v, want the keyring failure surfaced", err)
			}
		})
	}
}

func TestCheckedCredentialDeleteIgnoresKeyringNotFound(t *testing.T) {
	for _, tc := range checkedDeleteCases() {
		t.Run(tc.name, func(t *testing.T) {
			setup(t)
			stubKeyring(t, nil, nil, func(string, string) error { return keyring.ErrNotFound })

			if err := tc.del(); err != nil {
				t.Fatalf("checked delete error = %v, want nil when nothing is stored", err)
			}
		})
	}
}

func TestCheckedCredentialDeleteSurfacesUnremovableCredentialFile(t *testing.T) {
	for _, tc := range checkedDeleteCases() {
		t.Run(tc.name, func(t *testing.T) {
			setup(t)
			if err := os.MkdirAll(filepath.Join(tc.path(t), "occupied"), 0o700); err != nil {
				t.Fatalf("MkdirAll() error = %v", err)
			}

			err := tc.del()
			if err == nil {
				t.Fatal("checked delete error = nil although the credential path could not be removed")
			}
			if _, statErr := os.Stat(tc.path(t)); statErr != nil {
				t.Fatalf("fixture disappeared: %v", statErr)
			}
		})
	}
}

func TestCheckedCredentialDeleteSurfacesPathResolutionFailure(t *testing.T) {
	for _, tc := range checkedDeleteCases() {
		t.Run(tc.name, func(t *testing.T) {
			setup(t)
			restore := tc.setPathFunc(func() (string, error) {
				return "", errors.New("synthetic credential path failure")
			})
			t.Cleanup(restore)

			err := tc.del()
			if err == nil {
				t.Fatal("checked delete error = nil although the credential path could not be resolved")
			}
			if !strings.Contains(err.Error(), "synthetic credential path failure") {
				t.Fatalf("checked delete error = %v, want the path failure surfaced", err)
			}
		})
	}
}

func TestClientSecretAuthKeyDerivesFromTheCandidateNotTheStoredCredential(t *testing.T) {
	setup(t)
	const stored = "tskey-client-<testonly_fake>-<testonly_STORED>-<testonly_MUST>-<testonly_NOT>-<testonly_BE>-<testonly_USED>"
	const candidate = "tskey-client-<testonly_fake>-<testonly_CANDIDATE>"
	if err := SaveClientSecret(stored); err != nil {
		t.Fatalf("SaveClientSecret() error = %v", err)
	}

	got, err := ClientSecretAuthKey(candidate, AuthKeyOptions{Tags: []string{"tag:tsmain"}})
	if err != nil {
		t.Fatalf("ClientSecretAuthKey() error = %v", err)
	}
	base, rawQuery, found := strings.Cut(got, "?")
	if !found {
		t.Fatalf("ClientSecretAuthKey() = %q, want auth attributes appended", got)
	}
	if base != candidate {
		t.Fatalf("auth key base = %q, want the candidate secret %q", base, candidate)
	}
	if strings.Contains(got, stored) {
		t.Fatal("ClientSecretAuthKey() derived from the persisted credential instead of the candidate")
	}
	values, err := url.ParseQuery(rawQuery)
	if err != nil {
		t.Fatalf("ParseQuery(%q) error = %v", rawQuery, err)
	}
	if values.Get("preauthorized") != "true" {
		t.Fatalf("preauthorized = %q, want true", values.Get("preauthorized"))
	}

	// The stored credential must survive a pre-commit validation attempt.
	after, err := GetClientSecret()
	if err != nil {
		t.Fatalf("GetClientSecret() error = %v", err)
	}
	if after != stored {
		t.Fatalf("stored client secret changed during ClientSecretAuthKey(); got a different value")
	}
}

func TestClientSecretAuthKeySetsEphemeralFromOptions(t *testing.T) {
	for _, ephemeral := range []bool{true, false} {
		got, err := ClientSecretAuthKey("tskey-client-<testonly_fake>-<testonly_candidate>", AuthKeyOptions{
			Tags:      []string{"tag:tsmain"},
			Ephemeral: ephemeral,
		})
		if err != nil {
			t.Fatalf("ClientSecretAuthKey() error = %v", err)
		}
		_, rawQuery, _ := strings.Cut(got, "?")
		values, err := url.ParseQuery(rawQuery)
		if err != nil {
			t.Fatalf("ParseQuery(%q) error = %v", rawQuery, err)
		}
		want := "false"
		if ephemeral {
			want = "true"
		}
		if values.Get("ephemeral") != want {
			t.Fatalf("ephemeral = %q, want %q", values.Get("ephemeral"), want)
		}
	}
}

func TestClientSecretAuthKeyOverridesCallerSuppliedAuthAttributes(t *testing.T) {
	// A candidate that already carries attributes must not be able to smuggle
	// ephemeral=true or preauthorized=false past the derivation.
	got, err := ClientSecretAuthKey(
		"tskey-client-<testonly_fake>-<testonly_candidate>?ephemeral=true&preauthorized=false&keep=me",
		AuthKeyOptions{Tags: []string{"tag:tsmain"}, Ephemeral: false},
	)
	if err != nil {
		t.Fatalf("ClientSecretAuthKey() error = %v", err)
	}
	base, rawQuery, _ := strings.Cut(got, "?")
	if base != "tskey-client-<testonly_fake>-<testonly_candidate>" {
		t.Fatalf("auth key base = %q, want the secret without its query", base)
	}
	values, err := url.ParseQuery(rawQuery)
	if err != nil {
		t.Fatalf("ParseQuery(%q) error = %v", rawQuery, err)
	}
	if values.Get("ephemeral") != "false" {
		t.Fatalf("ephemeral = %q, want the option value to win", values.Get("ephemeral"))
	}
	if values.Get("preauthorized") != "true" {
		t.Fatalf("preauthorized = %q, want true regardless of caller input", values.Get("preauthorized"))
	}
	if values.Get("keep") != "me" {
		t.Fatalf("keep = %q, want unrelated attributes preserved", values.Get("keep"))
	}
}

func TestClientSecretAuthKeyRefusesWithoutTags(t *testing.T) {
	got, err := ClientSecretAuthKey("tskey-client-<testonly_fake>-<testonly_candidate>", AuthKeyOptions{})
	if err == nil {
		t.Fatalf("ClientSecretAuthKey() error = nil, want a tag requirement; got %q", got)
	}
	if !strings.Contains(err.Error(), "requires service tags") {
		t.Fatalf("ClientSecretAuthKey() error = %v, want the tag requirement", err)
	}
	if got != "" {
		t.Fatalf("ClientSecretAuthKey() = %q, want empty on refusal", got)
	}
}

func TestClientSecretAuthKeyRefusesAMalformedAttributeQuery(t *testing.T) {
	got, err := ClientSecretAuthKey("tskey-client-<testonly_fake>-<testonly_candidate>?%zz=1", AuthKeyOptions{Tags: []string{"tag:tsmain"}})
	if err == nil {
		t.Fatalf("ClientSecretAuthKey() error = nil, want a parse refusal; got %q", got)
	}
	if !strings.Contains(err.Error(), "parse client secret auth attributes") {
		t.Fatalf("ClientSecretAuthKey() error = %v, want the parse failure context", err)
	}
	if got != "" {
		t.Fatalf("ClientSecretAuthKey() = %q, want empty on refusal", got)
	}
}
