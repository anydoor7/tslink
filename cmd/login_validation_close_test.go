package cmd

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"strings"
	"testing"

	"github.com/anydoor7/tslink/internal/credentials"
	"tailscale.com/ipn/ipnstate"
)

// errEarlyValidationUp stands for a validation node whose Up failed before
// tsnet built its internal state, for example because os.Executable failed (a
// Linux host without a readable /proc/self/exe).
var errEarlyValidationUp = errors.New("tsnet: readlink /proc/self/exe: no such file or directory")

// panicOnCloseValidationServer reproduces tailscale.com v1.102.4 for the
// client-secret validation node: Up reports the early start failure, and
// tsnet.Server.Close after it dereferences a nil s.sys and panics.
type panicOnCloseValidationServer struct {
	closeCalls int
}

func (s *panicOnCloseValidationServer) Up(context.Context) (*ipnstate.Status, error) {
	return nil, fmt.Errorf("tsnet.Up: %w", errEarlyValidationUp)
}

func (s *panicOnCloseValidationServer) Close() error {
	s.closeCalls++
	panic("runtime error: invalid memory address or nil pointer dereference (simulated tsnet.Server.close after a failed start)")
}

// TestLoginWithClientSecretReturnsAnEarlyUpFailureInsteadOfPanicking: `tslink
// login --client-secret` reports the validation node's Up error instead of a
// panic trace, keeps the previous credential, still releases the node and its
// temporary state dir, and keeps the secret out of the error and the log.
func TestLoginWithClientSecretReturnsAnEarlyUpFailureInsteadOfPanicking(t *testing.T) {
	setupLoginTest(t)
	resetLoginFlags(t)
	if err := credentials.SetAPIKey("tskey-api-<test-only-existing>"); err != nil {
		t.Fatalf("SetAPIKey() error = %v", err)
	}

	oldActivate := loginActivateClientSecretFn
	oldNew := loginNewValidationServerFn
	oldRemove := loginRemoveAllFn
	oldLogger := slog.Default()
	t.Cleanup(func() {
		loginActivateClientSecretFn = oldActivate
		loginNewValidationServerFn = oldNew
		loginRemoveAllFn = oldRemove
		slog.SetDefault(oldLogger)
	})
	var logs bytes.Buffer
	slog.SetDefault(slog.New(slog.NewTextHandler(&logs, nil)))

	// The production activation, with only its tsnet node faked.
	loginActivateClientSecretFn = activateClientSecretViaUp
	fake := &panicOnCloseValidationServer{}
	var stateDir string
	loginNewValidationServerFn = func(tmpStateDir, _ string, _ []string) loginValidationServer {
		stateDir = tmpStateDir
		return fake
	}
	var removed string
	loginRemoveAllFn = func(path string) error {
		removed = path
		return os.RemoveAll(path)
	}

	const secretMaterial = "SynthSecretMaterial0123"
	err := loginWithClientSecret(loginCmd, "tskey-client-kSynthID-"+secretMaterial)
	if !errors.Is(err, errEarlyValidationUp) {
		t.Fatalf("loginWithClientSecret() error = %v, want the validation node's Up failure (%v)", err, errEarlyValidationUp)
	}
	if fake.closeCalls != 1 {
		t.Fatalf("validation node Close calls = %d, want 1: the failed node must still be released", fake.closeCalls)
	}
	if stateDir == "" || removed != stateDir {
		t.Fatalf("removed state dir = %q, want the validation node's %q", removed, stateDir)
	}
	if strings.Contains(err.Error(), secretMaterial) || strings.Contains(logs.String(), secretMaterial) {
		t.Fatal("the client secret reached the error or the log")
	}
	if !strings.Contains(logs.String(), "tsnet close after a failed start panicked") || !strings.Contains(logs.String(), clientSecretValidationHostname) {
		t.Fatalf("log = %q, want the abandoned validation node reported", logs.String())
	}

	gotAPIKey, err := credentials.GetAPIKey()
	if err != nil {
		t.Fatalf("GetAPIKey() error = %v", err)
	}
	if gotAPIKey != "tskey-api-<test-only-existing>" {
		t.Fatalf("api key = %q, want the previous credential kept", gotAPIKey)
	}
	gotSecret, err := credentials.GetClientSecret()
	if err != nil {
		t.Fatalf("GetClientSecret() error = %v", err)
	}
	if gotSecret != "" {
		t.Fatal("a client secret that failed activation was stored")
	}
}
