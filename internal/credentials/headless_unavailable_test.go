package credentials

import (
	"errors"
	"os"
	"testing"

	"github.com/anydoor7/tslink/internal/config"
)

func TestWholeProviderUnavailablePreservesAuthority(t *testing.T) {
	for _, mode := range []string{"api", "oauth"} {
		for _, disabled := range []bool{false, true} {
			name := mode + "_unavailable"
			if disabled {
				name = mode + "_disabled_control"
			}
			t.Run(name, func(t *testing.T) {
				setup(t)
				old := keyringEnabledFunc
				keyringEnabledFunc = func() bool { return !disabled }
				t.Cleanup(func() { keyringEnabledFunc = old })
				unavailable := errors.New("synthetic keyring unavailable")
				stubKeyring(t, func(string, string) (string, error) { return "", unavailable }, func(string, string, string) error { return unavailable }, func(string, string) error { return unavailable })
				value := "tskey-api-FAKE-review"
				oldValue := "tskey-api-FAKE-OLD"
				set := SetAPIKeyWithBackend
				get := GetAPIKey
				path, pathErr := config.APIKeyPath()
				if mode == "oauth" {
					value = "tskey-client-FAKE-review"
					oldValue = "tskey-client-FAKE-OLD"
					set = SaveClientSecretWithBackend
					get = GetClientSecret
					path, pathErr = config.ClientSecretPath()
				}
				if pathErr != nil {
					t.Fatal(pathErr)
				}
				backend, err := set(value)
				if disabled {
					if err != nil || backend != CredentialBackendFile {
						t.Fatalf("file-only control: %q %v", backend, err)
					}
					info, statErr := os.Stat(path)
					if statErr != nil || info.Mode().Perm() != 0o600 {
						t.Fatalf("file-only mode: %v %v", info, statErr)
					}
					got, getErr := get()
					if getErr != nil || got != value {
						t.Fatalf("file unreadable: %v", getErr)
					}
					return
				}
				if err == nil || backend != "" {
					t.Fatalf("unproven keyring absence silently swapped authority: backend=%q err=%v", backend, err)
				}
				if _, statErr := os.Stat(path); !os.IsNotExist(statErr) {
					t.Fatalf("fallback created despite unproven keyring absence: %v", statErr)
				}
				keyringGetFunc = func(string, string) (string, error) { return oldValue, nil }
				got, getErr := get()
				if getErr != nil || got != oldValue {
					t.Fatalf("recovered keyring credential lost: %v", getErr)
				}
			})
		}
	}
}
