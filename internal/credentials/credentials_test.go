package credentials

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/anydoor7/tslink/internal/config"
	"github.com/anydoor7/tslink/internal/testenv"
	"github.com/zalando/go-keyring"
	tailscale "tailscale.com/client/tailscale/v2"
)

func setup(t *testing.T) {
	t.Helper()
	keyring.MockInit()
	testenv.SetHome(t, t.TempDir())
	isolateMutationLock(t)
	if err := config.EnsureDir(); err != nil {
		t.Fatalf("EnsureDir() error = %v", err)
	}
}

func isolateMutationLock(t *testing.T) {
	t.Helper()
	oldLockPath := credentialMutationLockPathFunc
	lockPath := filepath.Join(t.TempDir(), "credentials.lock")
	credentialMutationLockPathFunc = func() (string, error) {
		return lockPath, nil
	}
	t.Cleanup(func() { credentialMutationLockPathFunc = oldLockPath })
}

func setInvalidConfigHome(t *testing.T) {
	t.Helper()
	home := filepath.Join(t.TempDir(), "not-a-directory")
	if err := os.WriteFile(home, []byte("block config directory creation"), 0o600); err != nil {
		t.Fatalf("WriteFile(invalid home) error = %v", err)
	}
	testenv.SetHome(t, home)
	isolateMutationLock(t)
}

func assertWindowsFileFallbackDisabled(t *testing.T, backend CredentialBackend, err error, path string) bool {
	t.Helper()
	if runtime.GOOS != "windows" {
		return false
	}
	if err == nil {
		t.Fatal("credential write error = nil, want Windows file-fallback refusal")
	}
	if backend != "" {
		t.Fatalf("credential backend = %q, want empty after Windows file-fallback refusal", backend)
	}
	if !strings.Contains(err.Error(), "file credential fallback is disabled on Windows") ||
		!strings.Contains(err.Error(), "Credential Manager") {
		t.Fatalf("credential write error = %v, want Windows Credential Manager remediation", err)
	}
	if _, statErr := os.Stat(path); !os.IsNotExist(statErr) {
		t.Fatalf("file fallback exists despite Windows policy: %v", statErr)
	}
	return true
}

func stubKeyring(t *testing.T,
	get func(string, string) (string, error),
	set func(string, string, string) error,
	delete func(string, string) error,
) {
	t.Helper()
	oldGet, oldSet, oldDelete := keyringGetFunc, keyringSetFunc, keyringDeleteFunc
	t.Cleanup(func() {
		keyringGetFunc, keyringSetFunc, keyringDeleteFunc = oldGet, oldSet, oldDelete
	})
	if get != nil {
		keyringGetFunc = get
	}
	if set != nil {
		keyringSetFunc = set
	}
	if delete != nil {
		keyringDeleteFunc = delete
	}
}

func stubUnavailableKeyringWrites(t *testing.T) {
	t.Helper()
	stubKeyring(t, nil,
		func(string, string, string) error { return errors.New("keyring write unavailable") },
		func(string, string) error { return keyring.ErrNotFound },
	)
}

func mockKeyringWithError(t *testing.T, err error) {
	t.Helper()
	keyring.MockInitWithError(err)
	t.Cleanup(keyring.MockInit)
}

func apiKeyPath(t *testing.T) string {
	t.Helper()
	path, err := config.APIKeyPath()
	if err != nil {
		t.Fatalf("APIKeyPath() error = %v", err)
	}
	return path
}

func authKeyPath(t *testing.T) string {
	t.Helper()
	path, err := config.AuthKeyPath()
	if err != nil {
		t.Fatalf("AuthKeyPath() error = %v", err)
	}
	return path
}

func rejectingAPIClientFactory(t *testing.T) (TailscaleClientFactory, <-chan string) {
	t.Helper()
	requests := make(chan string, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests <- r.Method + " " + r.URL.Path
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusServiceUnavailable)
		_, _ = w.Write([]byte(`{"status":503,"message":"synthetic API rejection"}`))
	}))
	t.Cleanup(server.Close)
	baseURL, err := url.Parse(server.URL)
	if err != nil {
		t.Fatalf("parse httptest URL: %v", err)
	}
	return func() (*tailscale.Client, error) {
		return &tailscale.Client{
			Tailnet: "-",
			APIKey:  "tskey-api-placeholder",
			BaseURL: baseURL,
			HTTP:    server.Client(),
		}, nil
	}, requests
}

func assertCreateAuthKeyRequest(t *testing.T, requests <-chan string) {
	t.Helper()
	select {
	case got := <-requests:
		if want := "POST /api/v2/tailnet/-/keys"; got != want {
			t.Fatalf("auth-key request = %q, want %q", got, want)
		}
	default:
		t.Fatal("httptest server received no auth-key request")
	}
}

func TestSetAPIKey_Keychain(t *testing.T) {
	setup(t)

	path := apiKeyPath(t)
	if err := os.WriteFile(path, []byte("legacy-key\n"), 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	backend, err := SetAPIKeyWithBackend("keychain-key")
	if err != nil {
		t.Fatalf("SetAPIKeyWithBackend() error = %v", err)
	}
	if backend != CredentialBackendKeyring {
		t.Fatalf("backend = %q, want %q", backend, CredentialBackendKeyring)
	}

	got, err := GetAPIKey()
	if err != nil {
		t.Fatalf("GetAPIKey() error = %v", err)
	}
	if got != "keychain-key" {
		t.Fatalf("GetAPIKey() = %q, want %q", got, "keychain-key")
	}

	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("expected %q to be removed, stat err = %v", path, err)
	}
}

func TestSetAPIKey_FileFallback(t *testing.T) {
	setup(t)
	stubUnavailableKeyringWrites(t)

	path := apiKeyPath(t)
	backend, err := SetAPIKeyWithBackend("file-key")
	if assertWindowsFileFallbackDisabled(t, backend, err, path) {
		return
	}
	if err != nil {
		t.Fatalf("SetAPIKeyWithBackend() error = %v", err)
	}
	if backend != CredentialBackendFile {
		t.Fatalf("backend = %q, want %q", backend, CredentialBackendFile)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile() error = %v", err)
	}
	if got := string(data); got != "file-key" {
		t.Fatalf("file contents = %q, want %q", got, "file-key")
	}

	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("Stat() error = %v", err)
	}
	if got := info.Mode().Perm(); got != 0o600 {
		t.Fatalf("file perms = %o, want %o", got, 0o600)
	}
}

func TestSetAPIKey_FileFallbackDeletesStaleReadableKeyringValue(t *testing.T) {
	setup(t)

	deleted := false
	stubKeyring(t,
		func(string, string) (string, error) {
			if deleted {
				return "", keyring.ErrNotFound
			}
			return "tskey-api-stale-synthetic", nil
		},
		func(string, string, string) error { return errors.New("keyring write failed") },
		func(string, string) error {
			deleted = true
			return nil
		},
	)

	backend, err := SetAPIKeyWithBackend("tskey-api-replacement-synthetic")
	if assertWindowsFileFallbackDisabled(t, backend, err, apiKeyPath(t)) {
		if deleted {
			t.Fatal("Windows policy deleted stale keyring material before refusing file fallback")
		}
		return
	}
	if err != nil {
		t.Fatalf("SetAPIKeyWithBackend() error = %v", err)
	}
	if backend != CredentialBackendFile {
		t.Fatalf("backend = %q, want %q", backend, CredentialBackendFile)
	}
	if !deleted {
		t.Fatal("stale keyring value was not deleted before file fallback")
	}
	got, err := GetAPIKey()
	if err != nil {
		t.Fatalf("GetAPIKey() error = %v", err)
	}
	if got != "tskey-api-replacement-synthetic" {
		t.Fatal("GetAPIKey() returned stale keyring material after file fallback")
	}
}

func TestSetAPIKey_FileFallbackRefusesWhenStaleKeyringDeleteFails(t *testing.T) {
	setup(t)
	stubKeyring(t,
		func(string, string) (string, error) { return "tskey-api-stale-synthetic", nil },
		func(string, string, string) error { return errors.New("keyring write failed") },
		func(string, string) error { return errors.New("keyring delete failed") },
	)

	backend, err := SetAPIKeyWithBackend("tskey-api-replacement-synthetic")
	if assertWindowsFileFallbackDisabled(t, backend, err, apiKeyPath(t)) {
		return
	}
	if err == nil {
		t.Fatal("SetAPIKeyWithBackend() error = nil, want fail-closed delete error")
	}
	if backend != "" {
		t.Fatalf("backend = %q, want empty on failure", backend)
	}
	if !strings.Contains(err.Error(), "refusing file fallback") {
		t.Fatalf("error = %v, want explicit fallback refusal", err)
	}
	if _, statErr := os.Stat(apiKeyPath(t)); !os.IsNotExist(statErr) {
		t.Fatalf("file fallback exists despite stale keyring delete failure: %v", statErr)
	}
}

func TestCredentialFallbackAvailabilityMatrix(t *testing.T) {
	type credentialVariant struct {
		name           string
		keyringKey     string
		oldValue       string
		newValue       string
		reportsBackend bool
		set            func(string) (CredentialBackend, error)
		get            func() (string, error)
		path           func() (string, error)
		stubPath       func(*testing.T, func() (string, error))
	}

	variants := []credentialVariant{
		{
			name:           "SetAPIKeyWithBackend",
			keyringKey:     keychainAPIKey,
			oldValue:       "tskey-api-matrix-old",
			newValue:       "tskey-api-matrix-new",
			reportsBackend: true,
			set:            SetAPIKeyWithBackend,
			get:            GetAPIKey,
			path:           func() (string, error) { return apiKeyPathFunc() },
			stubPath: func(t *testing.T, fn func() (string, error)) {
				old := apiKeyPathFunc
				apiKeyPathFunc = fn
				t.Cleanup(func() { apiKeyPathFunc = old })
			},
		},
		{
			name:       "SetAPIKey",
			keyringKey: keychainAPIKey,
			oldValue:   "tskey-api-wrapper-matrix-old",
			newValue:   "tskey-api-wrapper-matrix-new",
			set: func(value string) (CredentialBackend, error) {
				return "", SetAPIKey(value)
			},
			get:  GetAPIKey,
			path: func() (string, error) { return apiKeyPathFunc() },
			stubPath: func(t *testing.T, fn func() (string, error)) {
				old := apiKeyPathFunc
				apiKeyPathFunc = fn
				t.Cleanup(func() { apiKeyPathFunc = old })
			},
		},
		{
			name:           "SaveClientSecretWithBackend",
			keyringKey:     keychainClientSecret,
			oldValue:       "tskey-client-matrix-old",
			newValue:       "tskey-client-matrix-new",
			reportsBackend: true,
			set:            SaveClientSecretWithBackend,
			get:            GetClientSecret,
			path:           func() (string, error) { return clientSecretPathFunc() },
			stubPath: func(t *testing.T, fn func() (string, error)) {
				old := clientSecretPathFunc
				clientSecretPathFunc = fn
				t.Cleanup(func() { clientSecretPathFunc = old })
			},
		},
		{
			name:       "SaveClientSecret",
			keyringKey: keychainClientSecret,
			oldValue:   "tskey-client-wrapper-matrix-old",
			newValue:   "tskey-client-wrapper-matrix-new",
			set: func(value string) (CredentialBackend, error) {
				return "", SaveClientSecret(value)
			},
			get:  GetClientSecret,
			path: func() (string, error) { return clientSecretPathFunc() },
			stubPath: func(t *testing.T, fn func() (string, error)) {
				old := clientSecretPathFunc
				clientSecretPathFunc = fn
				t.Cleanup(func() { clientSecretPathFunc = old })
			},
		},
	}

	type credentialScenario struct {
		name         string
		keyringWrite bool
		configure    func(*testing.T, credentialVariant)
		wantError    bool
		wantErrorHas []string
		wantValue    string
		wantBackend  CredentialBackend
		wantKeyring  bool
		wantFile     bool
	}

	fileFallbackExpectation := credentialScenario{
		name:        "keyring_write_fails_file_write_succeeds",
		wantValue:   "new",
		wantBackend: CredentialBackendFile,
		wantFile:    true,
	}
	if runtime.GOOS == "windows" {
		fileFallbackExpectation.wantError = true
		fileFallbackExpectation.wantErrorHas = []string{
			"file credential fallback is disabled on Windows",
			"Credential Manager",
		}
		fileFallbackExpectation.wantValue = "old"
		fileFallbackExpectation.wantBackend = ""
		fileFallbackExpectation.wantKeyring = true
		fileFallbackExpectation.wantFile = false
	}

	scenarios := []credentialScenario{
		{
			name:        "keyring_write_fails_fallback_disabled",
			wantError:   true,
			wantValue:   "old",
			wantKeyring: true,
			configure: func(t *testing.T, _ credentialVariant) {
				old := fileCredentialFallbackEnabledFunc
				fileCredentialFallbackEnabledFunc = func() bool { return false }
				t.Cleanup(func() { fileCredentialFallbackEnabledFunc = old })
			},
		},
		{
			name:        "keyring_write_fails_path_unresolvable",
			wantError:   true,
			wantValue:   "old",
			wantKeyring: true,
			configure: func(t *testing.T, variant credentialVariant) {
				variant.stubPath(t, func() (string, error) {
					return "", errors.New("synthetic credential path failure")
				})
			},
		},
		{
			name:        "keyring_write_fails_file_write_fails",
			wantError:   true,
			wantValue:   "old",
			wantKeyring: true,
			configure: func(t *testing.T, _ credentialVariant) {
				old := credentialFileWriteFunc
				credentialFileWriteFunc = func(string, []byte) error {
					return errors.New("synthetic credential file write failure")
				}
				t.Cleanup(func() { credentialFileWriteFunc = old })
			},
		},
		fileFallbackExpectation,
		{
			name:         "keyring_write_succeeds",
			keyringWrite: true,
			wantValue:    "new",
			wantBackend:  CredentialBackendKeyring,
			wantKeyring:  true,
		},
		{
			name:        "keyring_delete_reports_success_but_readback_finds_old",
			wantError:   true,
			wantValue:   "old",
			wantKeyring: true,
			configure: func(t *testing.T, _ credentialVariant) {
				old := keyringDeleteFunc
				keyringDeleteFunc = func(string, string) error { return nil }
				t.Cleanup(func() { keyringDeleteFunc = old })
			},
		},
	}

	for _, variant := range variants {
		variant := variant
		for _, scenario := range scenarios {
			scenario := scenario
			t.Run(variant.name+"/"+scenario.name, func(t *testing.T) {
				setup(t)

				path, err := variant.path()
				if err != nil {
					t.Fatalf("resolve fixture path: %v", err)
				}
				if err := keyring.Set(keychainService, variant.keyringKey, variant.oldValue); err != nil {
					t.Fatalf("seed in-memory keyring: %v", err)
				}
				if scenario.keyringWrite {
					if err := os.WriteFile(path, []byte("synthetic stale file copy"), 0o600); err != nil {
						t.Fatalf("seed stale file copy: %v", err)
					}
				} else {
					stubKeyring(t, nil,
						func(string, string, string) error { return errors.New("synthetic keyring write failure") },
						nil,
					)
				}
				if scenario.configure != nil {
					scenario.configure(t, variant)
				}

				backend, err := variant.set(variant.newValue)
				if scenario.wantError && err == nil {
					t.Fatal("credential write error = nil, want failure")
				}
				if !scenario.wantError && err != nil {
					t.Fatalf("credential write failed: %v", err)
				}
				for _, want := range scenario.wantErrorHas {
					if err == nil || !strings.Contains(err.Error(), want) {
						t.Fatalf("credential write error = %v, want substring %q", err, want)
					}
				}
				if variant.reportsBackend && backend != scenario.wantBackend {
					t.Fatalf("backend = %q, want %q", backend, scenario.wantBackend)
				}

				got, readErr := variant.get()
				if readErr != nil {
					t.Fatalf("no credential remained readable: %v", readErr)
				}
				want := variant.oldValue
				if scenario.wantValue == "new" {
					want = variant.newValue
				}
				if got != want {
					t.Fatal("readback did not preserve the expected old-or-new credential invariant")
				}

				_, keyringErr := keyring.Get(keychainService, variant.keyringKey)
				if scenario.wantKeyring && keyringErr != nil {
					t.Fatalf("expected in-memory keyring credential to remain readable: %v", keyringErr)
				}
				if !scenario.wantKeyring && !errors.Is(keyringErr, keyring.ErrNotFound) {
					t.Fatalf("expected stale in-memory keyring credential to be absent, got: %v", keyringErr)
				}
				_, fileErr := os.Stat(path)
				if scenario.wantFile && fileErr != nil {
					t.Fatalf("expected credential file to remain readable: %v", fileErr)
				}
				if !scenario.wantFile && !os.IsNotExist(fileErr) {
					t.Fatalf("expected credential file to be absent, stat error: %v", fileErr)
				}
			})
		}
	}
}

func TestFileCredentialFallbackDisabledFailsClosed(t *testing.T) {
	setup(t)
	stubUnavailableKeyringWrites(t)
	oldFallback := fileCredentialFallbackEnabledFunc
	fileCredentialFallbackEnabledFunc = func() bool { return false }
	t.Cleanup(func() { fileCredentialFallbackEnabledFunc = oldFallback })

	err := SetAPIKey("tskey-api-fake")
	if err == nil {
		t.Fatal("SetAPIKey() error = nil, want fallback disabled error")
	}
	if !strings.Contains(err.Error(), "file credential fallback is disabled on Windows") ||
		!strings.Contains(err.Error(), "Credential Manager") {
		t.Fatalf("SetAPIKey() error = %v, want precise Windows remediation", err)
	}
	if _, statErr := os.Stat(apiKeyPath(t)); !os.IsNotExist(statErr) {
		t.Fatalf("apikey fallback file exists despite fail-closed fallback, stat err = %v", statErr)
	}

	err = SaveClientSecret("tskey-client-fake")
	if err == nil {
		t.Fatal("SaveClientSecret() error = nil, want fallback disabled error")
	}
	if !strings.Contains(err.Error(), "file credential fallback is disabled on Windows") ||
		!strings.Contains(err.Error(), "Credential Manager") {
		t.Fatalf("SaveClientSecret() error = %v, want precise Windows remediation", err)
	}
	if _, statErr := os.Stat(clientSecretPath(t)); !os.IsNotExist(statErr) {
		t.Fatalf("clientsecret fallback file exists despite fail-closed fallback, stat err = %v", statErr)
	}
}

func TestGetAPIKey_KeychainFirst(t *testing.T) {
	setup(t)

	if err := keyring.Set(keychainService, keychainAPIKey, "from-keychain"); err != nil {
		t.Fatalf("keyring.Set() error = %v", err)
	}
	if err := os.WriteFile(apiKeyPath(t), []byte("from-file\n"), 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	got, err := GetAPIKey()
	if err != nil {
		t.Fatalf("GetAPIKey() error = %v", err)
	}
	if got != "from-keychain" {
		t.Fatalf("GetAPIKey() = %q, want %q", got, "from-keychain")
	}
}

func TestGetAPIKey_FileFallback(t *testing.T) {
	setup(t)

	if err := os.WriteFile(apiKeyPath(t), []byte("from-file\n"), 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	got, err := GetAPIKey()
	if err != nil {
		t.Fatalf("GetAPIKey() error = %v", err)
	}
	if got != "from-file" {
		t.Fatalf("GetAPIKey() = %q, want %q", got, "from-file")
	}
}

func TestGetAPIKey_FileFallbackRepairsInsecurePermissions(t *testing.T) {
	setup(t)

	path := apiKeyPath(t)
	if err := os.WriteFile(path, []byte("from-file\n"), 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	got, err := GetAPIKey()
	if err != nil {
		t.Fatalf("GetAPIKey() error = %v", err)
	}
	if got != "from-file" {
		t.Fatalf("GetAPIKey() = %q, want %q", got, "from-file")
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("Stat() error = %v", err)
	}
	if runtime.GOOS != "windows" {
		if got := info.Mode().Perm(); got != 0o600 {
			t.Fatalf("file perms = %o, want repaired 600", got)
		}
	} else if !info.Mode().IsRegular() {
		t.Fatalf("file mode = %v, want regular legacy credential file on Windows", info.Mode())
	}
}

func TestGetAPIKey_FileFallbackSkipsPermissionRepairWhenDisabled(t *testing.T) {
	setup(t)

	oldEnforce := enforceCredentialFilePermissionsFunc
	t.Cleanup(func() { enforceCredentialFilePermissionsFunc = oldEnforce })
	enforceCredentialFilePermissionsFunc = func() bool { return false }

	path := apiKeyPath(t)
	if err := os.WriteFile(path, []byte("from-file\n"), 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	infoBefore, err := os.Stat(path)
	if err != nil {
		t.Fatalf("Stat() before read error = %v", err)
	}

	got, err := GetAPIKey()
	if err != nil {
		t.Fatalf("GetAPIKey() error = %v", err)
	}
	if got != "from-file" {
		t.Fatalf("GetAPIKey() = %q, want %q", got, "from-file")
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("Stat() error = %v", err)
	}
	if got, want := info.Mode().Perm(), infoBefore.Mode().Perm(); got != want {
		t.Fatalf("file perms = %o, want unchanged %o", got, want)
	}
}

func TestGetAPIKey_NoKey(t *testing.T) {
	setup(t)

	got, err := GetAPIKey()
	if err != nil {
		t.Fatalf("GetAPIKey() error = %v", err)
	}
	if got != "" {
		t.Fatalf("GetAPIKey() = %q, want empty string", got)
	}
}

func TestGetAPIKey_FileReadError(t *testing.T) {
	setup(t)

	if err := os.Mkdir(apiKeyPath(t), 0o700); err != nil {
		t.Fatalf("Mkdir() error = %v", err)
	}

	_, err := GetAPIKey()
	if err == nil {
		t.Fatal("GetAPIKey() error = nil, want error")
	}
}

func TestDeleteAPIKey(t *testing.T) {
	setup(t)

	if err := SetAPIKey("secret"); err != nil {
		t.Fatalf("SetAPIKey() error = %v", err)
	}

	DeleteAPIKey()

	got, err := GetAPIKey()
	if err != nil {
		t.Fatalf("GetAPIKey() error = %v", err)
	}
	if got != "" {
		t.Fatalf("GetAPIKey() = %q, want empty string", got)
	}
}

func TestDeleteAPIKey_Both(t *testing.T) {
	setup(t)

	if err := keyring.Set(keychainService, keychainAPIKey, "from-keychain"); err != nil {
		t.Fatalf("keyring.Set() error = %v", err)
	}
	path := apiKeyPath(t)
	if err := os.WriteFile(path, []byte("from-file\n"), 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	DeleteAPIKey()

	if _, err := keyring.Get(keychainService, keychainAPIKey); err == nil {
		t.Fatal("expected keychain entry to be deleted")
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("expected %q to be removed, stat err = %v", path, err)
	}
}

func TestInspectStoredCredentialsStrictNotFoundIsClean(t *testing.T) {
	setup(t)

	status, err := InspectStoredCredentialsStrict()
	if err != nil {
		t.Fatalf("InspectStoredCredentialsStrict() error = %v", err)
	}
	if status.AnyPresent() {
		t.Fatalf("InspectStoredCredentialsStrict().AnyPresent() = true, want false")
	}
}

func TestInspectStoredCredentialsStrictKeyringReadFailure(t *testing.T) {
	setup(t)
	mockKeyringWithError(t, errors.New("keyring locked"))

	status, err := InspectStoredCredentialsStrict()
	if err == nil {
		t.Fatal("InspectStoredCredentialsStrict() error = nil, want keyring read failure")
	}
	if status.AnyPresent() {
		t.Fatalf("status.AnyPresent() = true despite unreadable keyring")
	}
	if strings.Contains(err.Error(), "tskey-") {
		t.Fatalf("strict inspect leaked credential-looking material: %v", err)
	}
	if !strings.Contains(err.Error(), "keyring unreadable") {
		t.Fatalf("InspectStoredCredentialsStrict() error = %v, want keyring unreadable context", err)
	}
}

func TestDeleteStoredCredentialsStrictKeyringDeleteFailureStillRemovesFiles(t *testing.T) {
	setup(t)
	if err := os.WriteFile(apiKeyPath(t), []byte("tskey-api-file\n"), 0o600); err != nil {
		t.Fatalf("WriteFile(api key) error = %v", err)
	}
	if err := os.WriteFile(clientSecretPath(t), []byte("tskey-client-file\n"), 0o600); err != nil {
		t.Fatalf("WriteFile(client secret) error = %v", err)
	}
	mockKeyringWithError(t, errors.New("keyring locked"))

	err := DeleteStoredCredentialsStrict()
	if err == nil {
		t.Fatal("DeleteStoredCredentialsStrict() error = nil, want keyring delete/readback failure")
	}
	if strings.Contains(err.Error(), "tskey-") {
		t.Fatalf("strict delete leaked credential-looking material: %v", err)
	}
	for _, path := range []string{apiKeyPath(t), clientSecretPath(t)} {
		if _, statErr := os.Stat(path); !os.IsNotExist(statErr) {
			t.Fatalf("%s still exists after strict delete with keyring failure, stat err = %v", path, statErr)
		}
	}
}

func TestDeleteStoredCredentialsStrictDisabledKeyringLeavesResidualRisk(t *testing.T) {
	setup(t)
	if err := keyring.Set(keychainService, keychainAPIKey, "tskey-api-keyring"); err != nil {
		t.Fatalf("keyring.Set(api) error = %v", err)
	}
	if err := os.WriteFile(apiKeyPath(t), []byte("tskey-api-file\n"), 0o600); err != nil {
		t.Fatalf("WriteFile(api key) error = %v", err)
	}

	oldKeyringEnabled := keyringEnabledFunc
	defer func() { keyringEnabledFunc = oldKeyringEnabled }()
	keyringEnabledFunc = func() bool { return false }
	err := DeleteStoredCredentialsStrict()
	keyringEnabledFunc = oldKeyringEnabled
	if err == nil {
		t.Fatal("DeleteStoredCredentialsStrict() error = nil, want disabled-keyring residual risk")
	}
	if !strings.Contains(err.Error(), "keyring disabled") {
		t.Fatalf("DeleteStoredCredentialsStrict() error = %v, want disabled keyring context", err)
	}
	if strings.Contains(err.Error(), "tskey-") {
		t.Fatalf("strict delete leaked credential-looking material: %v", err)
	}
	if _, statErr := os.Stat(apiKeyPath(t)); !os.IsNotExist(statErr) {
		t.Fatalf("file fallback not removed while keyring disabled, stat err = %v", statErr)
	}
	if _, keyringErr := keyring.Get(keychainService, keychainAPIKey); keyringErr != nil {
		t.Fatalf("keyring residual fixture was unexpectedly removed/read failed: %v", keyringErr)
	}
}

func TestMigrateFromLegacy_Success(t *testing.T) {
	setup(t)

	path := apiKeyPath(t)
	if err := os.WriteFile(path, []byte("legacy-key\n"), 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	if migrated := MigrateFromLegacy(); !migrated {
		t.Fatal("MigrateFromLegacy() = false, want true")
	}

	got, err := keyring.Get(keychainService, keychainAPIKey)
	if err != nil {
		t.Fatalf("keyring.Get() error = %v", err)
	}
	if got != "legacy-key" {
		t.Fatalf("keyring value = %q, want %q", got, "legacy-key")
	}

	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("expected %q to be removed, stat err = %v", path, err)
	}
}

func TestMigrateFromLegacy_NoFile(t *testing.T) {
	setup(t)

	if migrated := MigrateFromLegacy(); migrated {
		t.Fatal("MigrateFromLegacy() = true, want false")
	}
}

func TestMigrateFromLegacy_EmptyFile(t *testing.T) {
	setup(t)

	if err := os.WriteFile(apiKeyPath(t), []byte(" \n\t "), 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	if migrated := MigrateFromLegacy(); migrated {
		t.Fatal("MigrateFromLegacy() = true, want false")
	}
}

func TestMigrateFromLegacy_KeychainFail(t *testing.T) {
	setup(t)

	path := apiKeyPath(t)
	if err := os.WriteFile(path, []byte("legacy-key\n"), 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	mockKeyringWithError(t, errors.New("no keychain"))

	if migrated := MigrateFromLegacy(); migrated {
		t.Fatal("MigrateFromLegacy() = true, want false")
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile() error = %v", err)
	}
	if got := strings.TrimSpace(string(data)); got != "legacy-key" {
		t.Fatalf("file contents = %q, want %q", got, "legacy-key")
	}
}

func TestNewTailscaleClient_WithKey(t *testing.T) {
	setup(t)

	if err := SetAPIKey("api-key"); err != nil {
		t.Fatalf("SetAPIKey() error = %v", err)
	}

	client, err := NewTailscaleClient()
	if err != nil {
		t.Fatalf("NewTailscaleClient() error = %v", err)
	}
	if client == nil {
		t.Fatal("NewTailscaleClient() returned nil client")
	}
	if got := client.Tailnet; got != "-" {
		t.Fatalf("client.Tailnet = %q, want %q", got, "-")
	}
}

func TestNewTailscaleClient_WithOAuthSecretExchangesTokenWithoutScopes(t *testing.T) {
	setup(t)

	const (
		clientID     = "clientid"
		clientSecret = "tskey-client-clientid-secretvalue"
	)
	if err := SaveClientSecret(clientSecret + "?ephemeral=true&preauthorized=true"); err != nil {
		t.Fatalf("SaveClientSecret() error = %v", err)
	}

	var tokenRequests, policyRequests int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v2/oauth/token":
			tokenRequests++
			gotID, gotSecret, ok := r.BasicAuth()
			if !ok || gotID != clientID || gotSecret != clientSecret {
				t.Errorf("OAuth basic auth = (%q, redacted, %v), want derived client ID and stripped secret", gotID, ok)
			}
			if err := r.ParseForm(); err != nil {
				t.Errorf("ParseForm() error = %v", err)
			}
			if _, requested := r.Form["scope"]; requested {
				t.Errorf("OAuth token request unexpectedly included scope=%q", r.Form.Get("scope"))
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"access_token":"fake-access-token","token_type":"Bearer","expires_in":3600}`))
		case "/api/v2/tailnet/-/acl":
			policyRequests++
			if got := r.Header.Get("Authorization"); got != "Bearer fake-access-token" {
				t.Errorf("Authorization = %q, want OAuth bearer token", got)
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{}`))
		default:
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(server.Close)

	client, err := NewTailscaleClient()
	if err != nil {
		t.Fatalf("NewTailscaleClient() error = %v", err)
	}
	baseURL, err := url.Parse(server.URL)
	if err != nil {
		t.Fatalf("url.Parse() error = %v", err)
	}
	client.BaseURL = baseURL
	if _, err := client.PolicyFile().Get(context.Background()); err != nil {
		t.Fatalf("PolicyFile().Get() error = %v", err)
	}
	if tokenRequests != 1 || policyRequests != 1 {
		t.Fatalf("requests = token:%d policy:%d, want 1 each", tokenRequests, policyRequests)
	}
}

func TestNewTailscaleClient_OAuthTakesPrecedenceWhenBothCredentialsStored(t *testing.T) {
	setup(t)
	if err := SetAPIKey("tskey-api-should-not-win"); err != nil {
		t.Fatalf("SetAPIKey() error = %v", err)
	}
	if err := SaveClientSecret("tskey-client-preferred-clientsecret"); err != nil {
		t.Fatalf("SaveClientSecret() error = %v", err)
	}

	client, err := NewTailscaleClient()
	if err != nil {
		t.Fatalf("NewTailscaleClient() error = %v", err)
	}
	if client == nil || client.Auth == nil {
		t.Fatalf("client = %+v, want OAuth authentication", client)
	}
	if client.APIKey != "" {
		t.Fatalf("client APIKey is populated, want OAuth precedence over stored API key")
	}
	oauth, ok := client.Auth.(*tailscale.OAuth)
	if !ok || oauth.ClientID != "preferred" {
		t.Fatalf("OAuth auth = %#v, want derived ClientID preferred", client.Auth)
	}
}

func TestNewTailscaleClient_InvalidOAuthSecretFailsBeforeRequest(t *testing.T) {
	setup(t)
	if err := SaveClientSecret("tskey-client-malformed"); err != nil {
		t.Fatalf("SaveClientSecret() error = %v", err)
	}

	client, err := NewTailscaleClient()
	if err == nil {
		t.Fatal("NewTailscaleClient() error = nil, want invalid-format error")
	}
	if client != nil {
		t.Fatal("NewTailscaleClient() returned a client for malformed OAuth secret")
	}
	if !strings.Contains(err.Error(), "expected tskey-client-<id>-<secret>") {
		t.Fatalf("NewTailscaleClient() error = %v, want format remediation", err)
	}
}

func TestNewTailscaleClient_RejectedOAuthCredentialSurfacesError(t *testing.T) {
	setup(t)
	if err := SaveClientSecret("tskey-client-clientid-rejected"); err != nil {
		t.Fatalf("SaveClientSecret() error = %v", err)
	}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		http.Error(w, `{"error":"invalid_client"}`, http.StatusUnauthorized)
	}))
	t.Cleanup(server.Close)
	client, err := NewTailscaleClient()
	if err != nil {
		t.Fatalf("NewTailscaleClient() error = %v", err)
	}
	baseURL, err := url.Parse(server.URL)
	if err != nil {
		t.Fatalf("url.Parse() error = %v", err)
	}
	client.BaseURL = baseURL

	_, err = client.PolicyFile().Get(context.Background())
	if err == nil {
		t.Fatal("PolicyFile().Get() error = nil, want rejected-credential error")
	}
	if !strings.Contains(err.Error(), "401 Unauthorized") {
		t.Fatalf("PolicyFile().Get() error = %v, want clear OAuth rejection status", err)
	}
}

func TestNewTailscaleClient_NoKey(t *testing.T) {
	setup(t)

	client, err := NewTailscaleClient()
	if err != nil {
		t.Fatalf("NewTailscaleClient() error = %v", err)
	}
	if client != nil {
		t.Fatal("NewTailscaleClient() returned non-nil client")
	}
}

func TestNewTailscaleClientWithUserOwnedAPIKey_UsesAPIKeyWhenOAuthAlsoExists(t *testing.T) {
	setup(t)
	if err := SetAPIKey("tskey-api-placeholder"); err != nil {
		t.Fatalf("SetAPIKey() error = %v", err)
	}
	if err := SaveClientSecret("tskey-client-placeholder-placeholder"); err != nil {
		t.Fatalf("SaveClientSecret() error = %v", err)
	}

	client, err := NewTailscaleClientWithUserOwnedAPIKey()
	if err != nil {
		t.Fatalf("NewTailscaleClientWithUserOwnedAPIKey() error = %v", err)
	}
	if client == nil || client.APIKey != "tskey-api-placeholder" || client.Auth != nil {
		t.Fatalf("client = %+v, want API-key client even when OAuth is stored", client)
	}
}

func TestNewTailscaleClientWithUserOwnedAPIKey_OAuthOnlyFailsActionably(t *testing.T) {
	setup(t)
	if err := SaveClientSecret("tskey-client-placeholder-placeholder"); err != nil {
		t.Fatalf("SaveClientSecret() error = %v", err)
	}

	client, err := NewTailscaleClientWithUserOwnedAPIKey()
	if client != nil || !errors.Is(err, ErrUserOwnedAPIKeyRequired) {
		t.Fatalf("client = %+v error = %v, want ErrUserOwnedAPIKeyRequired", client, err)
	}
	for _, want := range []string{"OAuth client secret", "user-owned tskey-api- token", "tslink login --api-key-stdin"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("error = %q, want %q", err, want)
		}
	}
}

func TestNewTailscaleClientWithUserOwnedAPIKey_RejectsWrongCredentialType(t *testing.T) {
	setup(t)
	if err := SetAPIKey("placeholder-without-api-prefix"); err != nil {
		t.Fatalf("SetAPIKey() error = %v", err)
	}

	client, err := NewTailscaleClientWithUserOwnedAPIKey()
	if client != nil || !errors.Is(err, ErrUserOwnedAPIKeyRequired) {
		t.Fatalf("client = %+v error = %v, want ErrUserOwnedAPIKeyRequired", client, err)
	}
	if !strings.Contains(err.Error(), "not a tskey-api- token") {
		t.Fatalf("error = %q, want credential-type remediation", err)
	}
}

func TestNewTailscaleClient_GetAPIKeyError(t *testing.T) {
	setup(t)

	if err := os.Mkdir(apiKeyPath(t), 0o700); err != nil {
		t.Fatalf("Mkdir() error = %v", err)
	}

	client, err := NewTailscaleClient()
	if err == nil {
		t.Fatal("NewTailscaleClient() error = nil, want error")
	}
	if client != nil {
		t.Fatal("NewTailscaleClient() returned non-nil client on error")
	}
}

func TestDeriveAuthKey_NoKey(t *testing.T) {
	setup(t)

	_, err := DeriveAuthKey(context.Background(), AuthKeyOptions{})
	if err == nil {
		t.Fatal("DeriveAuthKey() error = nil, want error")
	}
	if !strings.Contains(err.Error(), "no API key configured") {
		t.Fatalf("DeriveAuthKey() error = %v, want missing API key error", err)
	}
}

func TestGetAuthKey_LegacyFallback(t *testing.T) {
	setup(t)

	if err := os.WriteFile(authKeyPath(t), []byte("legacy-auth\n"), 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	got, err := GetAuthKey(context.Background(), AuthKeyOptions{})
	if err != nil {
		t.Fatalf("GetAuthKey() error = %v", err)
	}
	if got != "legacy-auth" {
		t.Fatalf("GetAuthKey() = %q, want %q", got, "legacy-auth")
	}
}

func TestGetAuthKey_EmptyLegacy(t *testing.T) {
	setup(t)

	if err := os.WriteFile(authKeyPath(t), []byte(" \n\t "), 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	_, err := GetAuthKey(context.Background(), AuthKeyOptions{})
	if err == nil {
		t.Fatal("GetAuthKey() error = nil, want error")
	}
	if !strings.Contains(err.Error(), "empty auth key") {
		t.Fatalf("GetAuthKey() error = %v, want empty auth key error", err)
	}
}

func TestGetAuthKey_LegacyReadError(t *testing.T) {
	setup(t)

	if err := os.Mkdir(authKeyPath(t), 0o700); err != nil {
		t.Fatalf("Mkdir() error = %v", err)
	}

	_, err := GetAuthKey(context.Background(), AuthKeyOptions{})
	if err == nil {
		t.Fatal("GetAuthKey() error = nil, want error")
	}
}

func TestGetAuthKey_NoAuth(t *testing.T) {
	setup(t)

	_, err := GetAuthKey(context.Background(), AuthKeyOptions{})
	if err == nil {
		t.Fatal("GetAuthKey() error = nil, want error")
	}
	if !strings.Contains(err.Error(), "not authenticated") {
		t.Fatalf("GetAuthKey() error = %v, want not authenticated error", err)
	}
}

func TestMigrateFromLegacy_ReadError(t *testing.T) {
	setup(t)

	if err := os.Mkdir(apiKeyPath(t), 0o700); err != nil {
		t.Fatalf("Mkdir() error = %v", err)
	}

	if migrated := MigrateFromLegacy(); migrated {
		t.Fatal("MigrateFromLegacy() = true, want false")
	}
}

func TestDeriveAuthKey_APICallError(t *testing.T) {
	setup(t)
	clientFactory, requests := rejectingAPIClientFactory(t)

	_, err := DeriveAuthKey(context.Background(), AuthKeyOptions{ClientFactory: clientFactory})
	if err == nil {
		t.Fatal("DeriveAuthKey() error = nil, want synthetic API error")
	}
	if !strings.Contains(err.Error(), "derive auth key") {
		t.Fatalf("DeriveAuthKey() error = %v, want 'derive auth key' error", err)
	}
	assertCreateAuthKeyRequest(t, requests)
}

func TestGetAuthKey_WithAPIKey_DeriveError(t *testing.T) {
	setup(t)

	if err := SetAPIKey("tskey-api-fake-key"); err != nil {
		t.Fatalf("SetAPIKey() error = %v", err)
	}

	clientFactory, requests := rejectingAPIClientFactory(t)
	_, err := GetAuthKey(context.Background(), AuthKeyOptions{ClientFactory: clientFactory})
	if err == nil {
		t.Fatal("GetAuthKey() error = nil, want error from DeriveAuthKey")
	}
	if !strings.Contains(err.Error(), "derive auth key") {
		t.Fatalf("GetAuthKey() error = %v, want 'derive auth key' error", err)
	}
	assertCreateAuthKeyRequest(t, requests)
}

// --- Tests for uncovered error paths ---

func TestSetAPIKey_FileFallback_PathError(t *testing.T) {
	// When keychain fails AND config.APIKeyPath() fails, SetAPIKey returns error
	keyring.MockInit()
	stubUnavailableKeyringWrites(t)
	setInvalidConfigHome(t)

	err := SetAPIKey("some-key")
	if err == nil {
		t.Fatal("SetAPIKey() error = nil, want invalid config path error")
	}
}

func TestGetAPIKey_PathError(t *testing.T) {
	// When keychain has no key AND config.APIKeyPath() fails
	keyring.MockInit()
	wantErr := errors.New("synthetic API key path failure")
	orig := apiKeyPathFunc
	apiKeyPathFunc = func() (string, error) { return "", wantErr }
	t.Cleanup(func() { apiKeyPathFunc = orig })

	_, err := GetAPIKey()
	if !errors.Is(err, wantErr) {
		t.Fatalf("GetAPIKey() error = %v, want %v", err, wantErr)
	}
}

func TestDeriveAuthKey_ClientError(t *testing.T) {
	// When NewTailscaleClient returns an error (GetAPIKey fails)
	keyring.MockInit()
	testenv.SetHome(t, t.TempDir())
	if err := config.EnsureDir(); err != nil {
		t.Fatalf("EnsureDir() error = %v", err)
	}

	// Create a directory at the apikey path to cause ReadFile to fail
	path := apiKeyPath(t)
	if err := os.Mkdir(path, 0o700); err != nil {
		t.Fatalf("Mkdir() error = %v", err)
	}

	_, err := DeriveAuthKey(context.Background(), AuthKeyOptions{})
	if err == nil {
		t.Fatal("DeriveAuthKey() error = nil, want error")
	}
	if !strings.Contains(err.Error(), "create client") {
		t.Fatalf("DeriveAuthKey() error = %v, want 'create client' error", err)
	}
}

func TestGetAuthKey_GetAPIKeyError(t *testing.T) {
	// When GetAPIKey itself returns an error
	keyring.MockInit()
	testenv.SetHome(t, t.TempDir())
	if err := config.EnsureDir(); err != nil {
		t.Fatalf("EnsureDir() error = %v", err)
	}

	// Create a directory at the apikey path to cause ReadFile to fail
	path := apiKeyPath(t)
	if err := os.Mkdir(path, 0o700); err != nil {
		t.Fatalf("Mkdir() error = %v", err)
	}

	_, err := GetAuthKey(context.Background(), AuthKeyOptions{})
	if err == nil {
		t.Fatal("GetAuthKey() error = nil, want error from GetAPIKey")
	}
}

func TestGetAuthKey_AuthKeyPathError(t *testing.T) {
	setup(t)

	// No API key set, so GetAuthKey falls through to legacy path.
	// Override authKeyPathFunc to simulate a failure.
	origAuthKeyPath := authKeyPathFunc
	t.Cleanup(func() { authKeyPathFunc = origAuthKeyPath })
	authKeyPathFunc = func() (string, error) {
		return "", errors.New("auth key path unavailable")
	}

	_, err := GetAuthKey(context.Background(), AuthKeyOptions{})
	if err == nil {
		t.Fatal("GetAuthKey() error = nil, want error from AuthKeyPath")
	}
	if !strings.Contains(err.Error(), "auth key path unavailable") {
		t.Fatalf("GetAuthKey() error = %v, want 'auth key path unavailable'", err)
	}
}

func TestHasStoredCredentialModes(t *testing.T) {
	cases := []struct {
		name  string
		setup func(t *testing.T)
		want  bool
	}{
		{
			name: "none",
			want: false,
		},
		{
			name: "client secret",
			setup: func(t *testing.T) {
				if err := SaveClientSecret("tskey-client-stored"); err != nil {
					t.Fatalf("SaveClientSecret() error = %v", err)
				}
			},
			want: true,
		},
		{
			name: "api key",
			setup: func(t *testing.T) {
				if err := SetAPIKey("tskey-api-stored"); err != nil {
					t.Fatalf("SetAPIKey() error = %v", err)
				}
			},
			want: true,
		},
		{
			name: "legacy auth key",
			setup: func(t *testing.T) {
				if err := os.WriteFile(authKeyPath(t), []byte("legacy-auth\n"), 0o600); err != nil {
					t.Fatalf("WriteFile() error = %v", err)
				}
			},
			want: true,
		},
		{
			name: "empty legacy auth key",
			setup: func(t *testing.T) {
				if err := os.WriteFile(authKeyPath(t), []byte(" \n\t "), 0o600); err != nil {
					t.Fatalf("WriteFile() error = %v", err)
				}
			},
			want: false,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			setup(t)
			if tc.setup != nil {
				tc.setup(t)
			}
			got, err := HasStoredCredential()
			if err != nil {
				t.Fatalf("HasStoredCredential() error = %v", err)
			}
			if got != tc.want {
				t.Fatalf("HasStoredCredential() = %v, want %v", got, tc.want)
			}
			err = RequireStoredCredential()
			if tc.want && err != nil {
				t.Fatalf("RequireStoredCredential() error = %v, want nil", err)
			}
			if !tc.want && (err == nil || !strings.Contains(err.Error(), "not authenticated")) {
				t.Fatalf("RequireStoredCredential() error = %v, want not authenticated", err)
			}
		})
	}
}

func TestHasStoredCredentialErrorPaths(t *testing.T) {
	t.Run("client secret read error", func(t *testing.T) {
		setup(t)
		path := clientSecretPath(t)
		if err := os.Mkdir(path, 0o700); err != nil {
			t.Fatalf("Mkdir() error = %v", err)
		}
		_, err := HasStoredCredential()
		assertCredentialPathError(t, err, path)
	})

	t.Run("auth key path error", func(t *testing.T) {
		setup(t)
		origAuthKeyPath := authKeyPathFunc
		t.Cleanup(func() { authKeyPathFunc = origAuthKeyPath })
		wantErr := errors.New("auth key path unavailable")
		authKeyPathFunc = func() (string, error) {
			return "", wantErr
		}
		_, err := HasStoredCredential()
		if !errors.Is(err, wantErr) {
			t.Fatalf("HasStoredCredential() error = %v, want injected auth key path error", err)
		}
	})

	t.Run("legacy auth key read error", func(t *testing.T) {
		setup(t)
		path := authKeyPath(t)
		if err := os.Mkdir(path, 0o700); err != nil {
			t.Fatalf("Mkdir() error = %v", err)
		}
		_, err := HasStoredCredential()
		assertCredentialPathError(t, err, path)
	})
}

func assertCredentialPathError(t *testing.T, err error, wantPath string) {
	t.Helper()
	if err == nil {
		t.Fatalf("credential read error = nil, want path-attributed failure for %q", wantPath)
	}
	var pathErr *os.PathError
	if errors.As(err, &pathErr) {
		if filepath.Clean(pathErr.Path) != filepath.Clean(wantPath) {
			t.Fatalf("credential read error path = %q, want %q", pathErr.Path, wantPath)
		}
		return
	}
	if !strings.Contains(err.Error(), wantPath) || !strings.Contains(err.Error(), "not a regular file") {
		t.Fatalf("credential read error = %v, want unsafe-file classification for %q", err, wantPath)
	}
}

func TestMigrateFromLegacy_PathError(t *testing.T) {
	// When config.APIKeyPath() itself fails
	keyring.MockInit()
	setInvalidConfigHome(t)

	if migrated := MigrateFromLegacy(); migrated {
		t.Fatal("MigrateFromLegacy() = true, want false for invalid config path")
	}
}

// --- ClientSecret tests ---

func clientSecretPath(t *testing.T) string {
	t.Helper()
	path, err := config.ClientSecretPath()
	if err != nil {
		t.Fatalf("ClientSecretPath() error = %v", err)
	}
	return path
}

func TestSaveClientSecret_Keychain(t *testing.T) {
	setup(t)

	path := clientSecretPath(t)
	// Write a file to ensure it gets cleaned up on keychain success
	if err := os.WriteFile(path, []byte("old-secret\n"), 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	backend, err := SaveClientSecretWithBackend("tskey-client-my-secret")
	if err != nil {
		t.Fatalf("SaveClientSecretWithBackend() error = %v", err)
	}
	if backend != CredentialBackendKeyring {
		t.Fatalf("backend = %q, want %q", backend, CredentialBackendKeyring)
	}

	got, err := GetClientSecret()
	if err != nil {
		t.Fatalf("GetClientSecret() error = %v", err)
	}
	if got != "tskey-client-my-secret" {
		t.Fatalf("GetClientSecret() = %q, want %q", got, "tskey-client-my-secret")
	}

	// File should be removed since keychain succeeded
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("expected %q to be removed, stat err = %v", path, err)
	}
}

func TestSaveClientSecret_InvalidPrefix(t *testing.T) {
	setup(t)

	err := SaveClientSecret("tskey-api-wrong-prefix")
	if err == nil {
		t.Fatal("SaveClientSecret() error = nil, want error for invalid prefix")
	}
	if !strings.Contains(err.Error(), "tskey-client-") {
		t.Fatalf("SaveClientSecret() error = %v, want prefix error", err)
	}
}

func TestSaveClientSecret_FileFallback(t *testing.T) {
	setup(t)
	stubUnavailableKeyringWrites(t)

	path := clientSecretPath(t)
	backend, err := SaveClientSecretWithBackend("tskey-client-file-secret")
	if assertWindowsFileFallbackDisabled(t, backend, err, path) {
		return
	}
	if err != nil {
		t.Fatalf("SaveClientSecretWithBackend() error = %v", err)
	}
	if backend != CredentialBackendFile {
		t.Fatalf("backend = %q, want %q", backend, CredentialBackendFile)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile() error = %v", err)
	}
	if got := string(data); got != "tskey-client-file-secret" {
		t.Fatalf("file contents = %q, want %q", got, "tskey-client-file-secret")
	}

	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("Stat() error = %v", err)
	}
	if got := info.Mode().Perm(); got != 0o600 {
		t.Fatalf("file perms = %o, want %o", got, 0o600)
	}
}

func TestSaveClientSecret_FileFallbackDeletesStaleReadableKeyringValue(t *testing.T) {
	setup(t)

	deleted := false
	stubKeyring(t,
		func(string, string) (string, error) {
			if deleted {
				return "", keyring.ErrNotFound
			}
			return "tskey-client-stale-synthetic", nil
		},
		func(string, string, string) error { return errors.New("keyring write failed") },
		func(string, string) error {
			deleted = true
			return nil
		},
	)

	backend, err := SaveClientSecretWithBackend("tskey-client-replacement-synthetic")
	if assertWindowsFileFallbackDisabled(t, backend, err, clientSecretPath(t)) {
		if deleted {
			t.Fatal("Windows policy deleted stale keyring material before refusing file fallback")
		}
		return
	}
	if err != nil {
		t.Fatalf("SaveClientSecretWithBackend() error = %v", err)
	}
	if backend != CredentialBackendFile {
		t.Fatalf("backend = %q, want %q", backend, CredentialBackendFile)
	}
	if !deleted {
		t.Fatal("stale keyring value was not deleted before file fallback")
	}
	got, err := GetClientSecret()
	if err != nil {
		t.Fatalf("GetClientSecret() error = %v", err)
	}
	if got != "tskey-client-replacement-synthetic" {
		t.Fatal("GetClientSecret() returned stale keyring material after file fallback")
	}
}

func TestSaveClientSecret_FileFallbackRefusesWhenStaleKeyringDeleteFails(t *testing.T) {
	setup(t)
	stubKeyring(t,
		func(string, string) (string, error) { return "tskey-client-stale-synthetic", nil },
		func(string, string, string) error { return errors.New("keyring write failed") },
		func(string, string) error { return errors.New("keyring delete failed") },
	)

	backend, err := SaveClientSecretWithBackend("tskey-client-replacement-synthetic")
	if assertWindowsFileFallbackDisabled(t, backend, err, clientSecretPath(t)) {
		return
	}
	if err == nil {
		t.Fatal("SaveClientSecretWithBackend() error = nil, want fail-closed delete error")
	}
	if backend != "" {
		t.Fatalf("backend = %q, want empty on failure", backend)
	}
	if !strings.Contains(err.Error(), "refusing file fallback") {
		t.Fatalf("error = %v, want explicit fallback refusal", err)
	}
	if _, statErr := os.Stat(clientSecretPath(t)); !os.IsNotExist(statErr) {
		t.Fatalf("file fallback exists despite stale keyring delete failure: %v", statErr)
	}
}

func TestSaveClientSecret_FileFallback_PathError(t *testing.T) {
	keyring.MockInit()
	stubUnavailableKeyringWrites(t)
	setInvalidConfigHome(t)

	err := SaveClientSecret("tskey-client-some-key")
	if err == nil {
		t.Fatal("SaveClientSecret() error = nil, want invalid config path error")
	}
}

func TestGetClientSecret_KeychainFirst(t *testing.T) {
	setup(t)

	if err := keyring.Set(keychainService, keychainClientSecret, "from-keychain"); err != nil {
		t.Fatalf("keyring.Set() error = %v", err)
	}
	if err := os.WriteFile(clientSecretPath(t), []byte("from-file\n"), 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	got, err := GetClientSecret()
	if err != nil {
		t.Fatalf("GetClientSecret() error = %v", err)
	}
	if got != "from-keychain" {
		t.Fatalf("GetClientSecret() = %q, want %q", got, "from-keychain")
	}
}

func TestGetClientSecret_FileFallback(t *testing.T) {
	setup(t)

	if err := os.WriteFile(clientSecretPath(t), []byte("from-file\n"), 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	got, err := GetClientSecret()
	if err != nil {
		t.Fatalf("GetClientSecret() error = %v", err)
	}
	if got != "from-file" {
		t.Fatalf("GetClientSecret() = %q, want %q", got, "from-file")
	}
}

func TestGetClientSecret_NoSecret(t *testing.T) {
	setup(t)

	got, err := GetClientSecret()
	if err != nil {
		t.Fatalf("GetClientSecret() error = %v", err)
	}
	if got != "" {
		t.Fatalf("GetClientSecret() = %q, want empty string", got)
	}
}

func TestGetClientSecret_FileReadError(t *testing.T) {
	setup(t)

	// Create a directory at the file path to cause ReadFile to fail
	if err := os.Mkdir(clientSecretPath(t), 0o700); err != nil {
		t.Fatalf("Mkdir() error = %v", err)
	}

	_, err := GetClientSecret()
	if err == nil {
		t.Fatal("GetClientSecret() error = nil, want error")
	}
}

func TestGetClientSecret_PathError(t *testing.T) {
	keyring.MockInit()
	wantErr := errors.New("synthetic client secret path failure")
	orig := clientSecretPathFunc
	clientSecretPathFunc = func() (string, error) { return "", wantErr }
	t.Cleanup(func() { clientSecretPathFunc = orig })

	_, err := GetClientSecret()
	if !errors.Is(err, wantErr) {
		t.Fatalf("GetClientSecret() error = %v, want %v", err, wantErr)
	}
}

func TestHasClientSecret_True(t *testing.T) {
	setup(t)

	if err := SaveClientSecret("tskey-client-test"); err != nil {
		t.Fatalf("SaveClientSecret() error = %v", err)
	}

	if !HasClientSecret() {
		t.Fatal("HasClientSecret() = false, want true")
	}
}

func TestHasClientSecret_False(t *testing.T) {
	setup(t)

	if HasClientSecret() {
		t.Fatal("HasClientSecret() = true, want false")
	}
}

func TestDeleteClientSecret(t *testing.T) {
	setup(t)

	if err := SaveClientSecret("tskey-client-secret"); err != nil {
		t.Fatalf("SaveClientSecret() error = %v", err)
	}

	DeleteClientSecret()

	got, err := GetClientSecret()
	if err != nil {
		t.Fatalf("GetClientSecret() error = %v", err)
	}
	if got != "" {
		t.Fatalf("GetClientSecret() = %q, want empty string", got)
	}
}

func TestDeleteClientSecret_Both(t *testing.T) {
	setup(t)

	if err := keyring.Set(keychainService, keychainClientSecret, "from-keychain"); err != nil {
		t.Fatalf("keyring.Set() error = %v", err)
	}
	path := clientSecretPath(t)
	if err := os.WriteFile(path, []byte("from-file\n"), 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	DeleteClientSecret()

	if _, err := keyring.Get(keychainService, keychainClientSecret); err == nil {
		t.Fatal("expected keychain entry to be deleted")
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("expected %q to be removed, stat err = %v", path, err)
	}
}

func TestGetAuthKey_ClientSecretFirst(t *testing.T) {
	setup(t)

	// Set both API key and client secret
	if err := SetAPIKey("tskey-api-fake"); err != nil {
		t.Fatalf("SetAPIKey() error = %v", err)
	}
	if err := SaveClientSecret("tskey-client-my-secret"); err != nil {
		t.Fatalf("SaveClientSecret() error = %v", err)
	}

	got, err := GetAuthKey(context.Background(), AuthKeyOptions{
		Tags:      []string{"tag:test"},
		Ephemeral: true,
	})
	if err != nil {
		t.Fatalf("GetAuthKey() error = %v", err)
	}
	base, rawQuery, _ := strings.Cut(got, "?")
	if base != "tskey-client-my-secret" {
		t.Fatalf("GetAuthKey() base = %q, want client secret priority", base)
	}
	values, err := url.ParseQuery(rawQuery)
	if err != nil {
		t.Fatalf("ParseQuery() error = %v", err)
	}
	if values.Get("ephemeral") != "true" || values.Get("preauthorized") != "true" {
		t.Fatalf("GetAuthKey() query = %q, want explicit ephemeral/preauthorized", rawQuery)
	}
}

func TestGetAuthKey_ClientSecretOnly(t *testing.T) {
	setup(t)

	if err := SaveClientSecret("tskey-client-only"); err != nil {
		t.Fatalf("SaveClientSecret() error = %v", err)
	}

	got, err := GetAuthKey(context.Background(), AuthKeyOptions{Tags: []string{"tag:test"}})
	if err != nil {
		t.Fatalf("GetAuthKey() error = %v", err)
	}
	if !strings.HasPrefix(got, "tskey-client-only?") {
		t.Fatalf("GetAuthKey() = %q, want client secret with auth attributes", got)
	}
	values, err := url.ParseQuery(strings.TrimPrefix(got, "tskey-client-only?"))
	if err != nil {
		t.Fatalf("ParseQuery() error = %v", err)
	}
	if values.Get("ephemeral") != "false" || values.Get("preauthorized") != "true" {
		t.Fatalf("query = %q, want explicit ephemeral=false and preauthorized=true", values.Encode())
	}
}

func TestGetAuthKey_ClientSecretRequiresTags(t *testing.T) {
	setup(t)

	if err := SaveClientSecret("tskey-client-only"); err != nil {
		t.Fatalf("SaveClientSecret() error = %v", err)
	}

	_, err := GetAuthKey(context.Background(), AuthKeyOptions{})
	if err == nil {
		t.Fatal("GetAuthKey() error = nil, want tags-required error")
	}
	if !strings.Contains(err.Error(), "requires service tags") {
		t.Fatalf("GetAuthKey() error = %v, want service tags error", err)
	}
}

func TestGetAuthKey_ClientSecretPreservesBaseURLAndOverwritesAttrs(t *testing.T) {
	setup(t)

	if err := SaveClientSecret("tskey-client-secret?baseURL=https%3A%2F%2Fheadscale.example.com&ephemeral=true&preauthorized=false"); err != nil {
		t.Fatalf("SaveClientSecret() error = %v", err)
	}

	got, err := GetAuthKey(context.Background(), AuthKeyOptions{
		Tags:      []string{"tag:test"},
		Ephemeral: false,
	})
	if err != nil {
		t.Fatalf("GetAuthKey() error = %v", err)
	}
	_, rawQuery, ok := strings.Cut(got, "?")
	if !ok {
		t.Fatalf("GetAuthKey() = %q, want query attributes", got)
	}
	values, err := url.ParseQuery(rawQuery)
	if err != nil {
		t.Fatalf("ParseQuery() error = %v", err)
	}
	if values.Get("baseURL") != "https://headscale.example.com" {
		t.Fatalf("baseURL = %q, want preserved Headscale URL", values.Get("baseURL"))
	}
	if values.Get("ephemeral") != "false" || values.Get("preauthorized") != "true" {
		t.Fatalf("query = %q, want overwritten ephemeral=false/preauthorized=true", values.Encode())
	}
}

func TestGetAuthKey_ClientSecretError(t *testing.T) {
	setup(t)

	// Create a directory at the client secret file path to cause GetClientSecret to fail
	if err := os.Mkdir(clientSecretPath(t), 0o700); err != nil {
		t.Fatalf("Mkdir() error = %v", err)
	}

	_, err := GetAuthKey(context.Background(), AuthKeyOptions{})
	if err == nil {
		t.Fatal("GetAuthKey() error = nil, want error from GetClientSecret")
	}
}

func TestDeriveAuthKey_Success(t *testing.T) {
	setup(t)

	if err := SetAPIKey("tskey-api-fake"); err != nil {
		t.Fatalf("SetAPIKey() error = %v", err)
	}

	// Override createKeyFunc to simulate a successful CreateKey call.
	origCreateKey := createKeyFunc
	t.Cleanup(func() { createKeyFunc = origCreateKey })
	createKeyFunc = func(_ *tailscale.Client, _ context.Context, req tailscale.CreateKeyRequest) (*tailscale.Key, error) {
		// Verify capabilities are passed correctly.
		caps := req.Capabilities
		if caps.Devices.Create.Reusable {
			t.Error("expected Reusable=false")
		}
		if !caps.Devices.Create.Preauthorized {
			t.Error("expected Preauthorized=true")
		}
		if !caps.Devices.Create.Ephemeral {
			t.Error("expected Ephemeral=true")
		}
		if len(caps.Devices.Create.Tags) != 1 || caps.Devices.Create.Tags[0] != "tag:test" {
			t.Errorf("Tags = %v, want [tag:test]", caps.Devices.Create.Tags)
		}
		if req.ExpirySeconds <= 0 || req.ExpirySeconds > derivedAuthKeyExpirySeconds {
			t.Errorf("ExpirySeconds = %d, want short-lived startup key", req.ExpirySeconds)
		}
		if req.Description != "TSLink service test startup auth key" {
			t.Errorf("Description = %q, want service-specific description", req.Description)
		}
		return &tailscale.Key{Key: "tskey-auth-derived"}, nil
	}

	secret, err := DeriveAuthKey(context.Background(), AuthKeyOptions{
		Tags:        []string{"tag:test"},
		Ephemeral:   true,
		Description: "TSLink service test startup auth key",
	})
	if err != nil {
		t.Fatalf("DeriveAuthKey() error = %v", err)
	}
	if secret != "tskey-auth-derived" {
		t.Fatalf("DeriveAuthKey() = %q, want %q", secret, "tskey-auth-derived")
	}
}

func TestGetAuthKey_DeriveSuccess(t *testing.T) {
	setup(t)

	if err := SetAPIKey("tskey-api-fake"); err != nil {
		t.Fatalf("SetAPIKey() error = %v", err)
	}

	// Override createKeyFunc to simulate a successful CreateKey call.
	origCreateKey := createKeyFunc
	t.Cleanup(func() { createKeyFunc = origCreateKey })
	createKeyFunc = func(_ *tailscale.Client, _ context.Context, _ tailscale.CreateKeyRequest) (*tailscale.Key, error) {
		return &tailscale.Key{Key: "tskey-auth-from-derive"}, nil
	}

	got, err := GetAuthKey(context.Background(), AuthKeyOptions{})
	if err != nil {
		t.Fatalf("GetAuthKey() error = %v", err)
	}
	if got != "tskey-auth-from-derive" {
		t.Fatalf("GetAuthKey() = %q, want %q", got, "tskey-auth-from-derive")
	}
}
