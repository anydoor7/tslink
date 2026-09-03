package cmd

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/monody0007/tslink/internal/credentials"
	"github.com/monody0007/tslink/internal/output"
)

type logoutKindHarness struct {
	apiKey        string
	clientSecret  string
	deletedKinds  []string
	deletedMeta   []string
	removedMeta   int
	fullDeletes   int
	inspectErr    error
	deleteKindErr error
}

func installLogoutKindHarness(t *testing.T, h *logoutKindHarness) {
	t.Helper()
	oldInspect := inspectStoredCredentialsFn
	oldDelete := deleteStoredCredentialsFn
	oldDeleteKind := deleteStoredCredentialKindFn
	oldDeleteMeta := deleteCredentialMetadataFn
	oldRemoveMeta := removeCredentialMetadataFn
	oldIsRunning := isRunningFn
	t.Cleanup(func() {
		inspectStoredCredentialsFn = oldInspect
		deleteStoredCredentialsFn = oldDelete
		deleteStoredCredentialKindFn = oldDeleteKind
		deleteCredentialMetadataFn = oldDeleteMeta
		removeCredentialMetadataFn = oldRemoveMeta
		isRunningFn = oldIsRunning
	})
	isRunningFn = func(string) bool { return false }
	inspectStoredCredentialsFn = func() (credentials.StoredCredentialStatus, error) {
		return logoutCredentialStatus(h.apiKey != "", h.clientSecret != ""), h.inspectErr
	}
	deleteStoredCredentialsFn = func() error {
		h.fullDeletes++
		h.apiKey, h.clientSecret = "", ""
		return nil
	}
	deleteStoredCredentialKindFn = func(kind string) error {
		if h.deleteKindErr != nil {
			return h.deleteKindErr
		}
		h.deletedKinds = append(h.deletedKinds, kind)
		switch kind {
		case credentials.SlotAPIKey:
			h.apiKey = ""
		case credentials.SlotClientSecret:
			h.clientSecret = ""
		}
		return nil
	}
	deleteCredentialMetadataFn = func(slot string) error {
		h.deletedMeta = append(h.deletedMeta, slot)
		return nil
	}
	removeCredentialMetadataFn = func() error {
		h.removedMeta++
		return nil
	}
}

func TestLogoutKindRemovesOnlySelectedSlot(t *testing.T) {
	for _, kind := range []string{credentials.SlotAPIKey, credentials.SlotClientSecret} {
		t.Run(kind, func(t *testing.T) {
			dir := t.TempDir()
			nodesDir := filepath.Join(dir, "nodes")
			if err := os.MkdirAll(nodesDir, 0o700); err != nil {
				t.Fatal(err)
			}
			h := &logoutKindHarness{apiKey: "tskey-api-FAKE", clientSecret: "tskey-client-FAKE"}
			installLogoutKindHarness(t, h)

			var buf bytes.Buffer
			err := logoutUserWithOptions(logoutOptions{PIDPath: filepath.Join(dir, "pid"), AuthKeyPath: filepath.Join(dir, "authkey"), NodesDir: nodesDir, ConfigDir: dir, Kind: kind}, false, &buf)
			if err != nil {
				t.Fatalf("logout --kind %s error = %v", kind, err)
			}
			other := credentials.SlotClientSecret
			if kind == credentials.SlotClientSecret {
				other = credentials.SlotAPIKey
			}
			if len(h.deletedKinds) != 1 || h.deletedKinds[0] != kind || len(h.deletedMeta) != 1 || h.deletedMeta[0] != kind {
				t.Fatalf("deleted kinds=%v meta=%v, want only %s", h.deletedKinds, h.deletedMeta, kind)
			}
			if h.fullDeletes != 0 || h.removedMeta != 0 {
				t.Fatalf("selective logout ran full deletion (deletes=%d removeMeta=%d)", h.fullDeletes, h.removedMeta)
			}
			if _, statErr := os.Stat(nodesDir); statErr != nil {
				t.Fatalf("selective logout removed node state: %v", statErr)
			}
			out := buf.String()
			if !strings.Contains(out, "Removed "+kind+" credential") || !strings.Contains(out, "kept: "+other) {
				t.Fatalf("human output = %q, want removed %s and kept %s", out, kind, other)
			}
			if strings.Contains(out, "FAKE") {
				t.Fatalf("human output leaked a credential: %q", out)
			}
		})
	}
}

func TestLogoutKindJSONListsDeletedAndKeepsOther(t *testing.T) {
	dir := t.TempDir()
	h := &logoutKindHarness{apiKey: "tskey-api-FAKE", clientSecret: "tskey-client-FAKE"}
	installLogoutKindHarness(t, h)
	got := captureStdout(t, func() {
		err := logoutUserWithOptions(logoutOptions{PIDPath: filepath.Join(dir, "pid"), AuthKeyPath: filepath.Join(dir, "authkey"), NodesDir: filepath.Join(dir, "nodes"), ConfigDir: dir, Kind: credentials.SlotAPIKey}, true, &bytes.Buffer{})
		if err != nil {
			t.Fatalf("logout --kind api-key --json error = %v", err)
		}
	})
	res := parseResult(t, got)
	if !res.OK || res.Command != "logout" {
		t.Fatalf("envelope = %+v", res)
	}
	data := dataMap(t, got)
	if data["was_logged_in"] != true || data["kind"] != credentials.SlotAPIKey {
		t.Fatalf("data = %v", data)
	}
	deleted, _ := data["deleted_credentials"].([]any)
	if len(deleted) != 1 || deleted[0] != credentials.SlotAPIKey {
		t.Fatalf("deleted_credentials = %v, want [api-key]", data["deleted_credentials"])
	}
	if h.clientSecret == "" {
		t.Fatal("client secret was deleted by --kind api-key")
	}
	if strings.Contains(got, "FAKE") {
		t.Fatalf("JSON leaked a credential: %s", got)
	}
}

func TestLogoutKindAbsentSlotIsNotLoggedIn(t *testing.T) {
	dir := t.TempDir()
	h := &logoutKindHarness{apiKey: "", clientSecret: "tskey-client-FAKE"}
	installLogoutKindHarness(t, h)
	var buf bytes.Buffer
	if err := logoutUserWithOptions(logoutOptions{PIDPath: filepath.Join(dir, "pid"), Kind: credentials.SlotAPIKey}, false, &buf); err != nil {
		t.Fatalf("error = %v", err)
	}
	if !strings.Contains(buf.String(), "No api-key credential is stored") || len(h.deletedKinds) != 0 {
		t.Fatalf("output = %q deleted=%v", buf.String(), h.deletedKinds)
	}
	got := captureStdout(t, func() {
		if err := logoutUserWithOptions(logoutOptions{PIDPath: filepath.Join(dir, "pid"), Kind: credentials.SlotAPIKey}, true, &bytes.Buffer{}); err != nil {
			t.Fatalf("json error = %v", err)
		}
	})
	data := dataMap(t, got)
	if data["was_logged_in"] != false || data["kind"] != credentials.SlotAPIKey {
		t.Fatalf("data = %v", data)
	}
	if deleted, _ := data["deleted_credentials"].([]any); len(deleted) != 0 {
		t.Fatalf("deleted_credentials = %v, want empty array", data["deleted_credentials"])
	}
}

func TestLogoutKindFailureModes(t *testing.T) {
	dir := t.TempDir()
	t.Run("invalid kind is usage error", func(t *testing.T) {
		installLogoutKindHarness(t, &logoutKindHarness{apiKey: "tskey-api-FAKE"})
		err := logoutUserWithOptions(logoutOptions{PIDPath: filepath.Join(dir, "pid"), Kind: "apikey"}, false, &bytes.Buffer{})
		if output.ExitCode(err) != output.ExitUsage {
			t.Fatalf("error = %v exit=%d, want usage", err, output.ExitCode(err))
		}
	})
	t.Run("daemon running refuses", func(t *testing.T) {
		installLogoutKindHarness(t, &logoutKindHarness{apiKey: "tskey-api-FAKE"})
		isRunningFn = func(string) bool { return true }
		err := logoutUserWithOptions(logoutOptions{PIDPath: filepath.Join(dir, "pid"), Kind: credentials.SlotAPIKey}, false, &bytes.Buffer{})
		if err == nil || !strings.Contains(err.Error(), "currently running") {
			t.Fatalf("error = %v", err)
		}
	})
	t.Run("inspect failure fails closed", func(t *testing.T) {
		h := &logoutKindHarness{apiKey: "tskey-api-FAKE", inspectErr: errors.New("keyring unreadable")}
		installLogoutKindHarness(t, h)
		err := logoutUserWithOptions(logoutOptions{PIDPath: filepath.Join(dir, "pid"), Kind: credentials.SlotAPIKey}, false, &bytes.Buffer{})
		if err == nil || len(h.deletedKinds) != 0 {
			t.Fatalf("error = %v deleted=%v, want refusal before deletion", err, h.deletedKinds)
		}
	})
	t.Run("delete failure surfaces and skips success", func(t *testing.T) {
		h := &logoutKindHarness{apiKey: "tskey-api-FAKE", deleteKindErr: errors.New("keyring delete denied")}
		installLogoutKindHarness(t, h)
		got := captureStdout(t, func() {
			err := logoutUserWithOptions(logoutOptions{PIDPath: filepath.Join(dir, "pid"), Kind: credentials.SlotAPIKey}, true, &bytes.Buffer{})
			if err == nil || !strings.Contains(err.Error(), "delete api-key credential") {
				t.Fatalf("error = %v", err)
			}
		})
		if got != "" {
			t.Fatalf("success envelope emitted despite delete failure: %s", got)
		}
	})
	t.Run("readback still present fails", func(t *testing.T) {
		h := &logoutKindHarness{apiKey: "tskey-api-FAKE"}
		installLogoutKindHarness(t, h)
		deleteStoredCredentialKindFn = func(string) error { return nil } // pretends to delete, value stays
		err := logoutUserWithOptions(logoutOptions{PIDPath: filepath.Join(dir, "pid"), Kind: credentials.SlotAPIKey}, false, &bytes.Buffer{})
		if err == nil || !strings.Contains(err.Error(), "still present after cleanup") {
			t.Fatalf("error = %v", err)
		}
	})
}

func TestFullLogoutRemovesMetadataAndListsDeletedSlots(t *testing.T) {
	dir := t.TempDir()
	h := &logoutKindHarness{apiKey: "tskey-api-FAKE", clientSecret: "tskey-client-FAKE"}
	installLogoutKindHarness(t, h)
	var buf bytes.Buffer
	if err := logoutUser(filepath.Join(dir, "pid"), filepath.Join(dir, "authkey"), filepath.Join(dir, "nodes"), dir, false, &buf); err != nil {
		t.Fatalf("logoutUser() error = %v", err)
	}
	if h.fullDeletes != 1 || h.removedMeta != 1 || len(h.deletedKinds) != 0 {
		t.Fatalf("full logout deletes=%d removeMeta=%d kinds=%v", h.fullDeletes, h.removedMeta, h.deletedKinds)
	}
	if !strings.Contains(buf.String(), "Logged out (deleted: api-key, client-secret)") {
		t.Fatalf("human output = %q", buf.String())
	}

	h = &logoutKindHarness{apiKey: "tskey-api-FAKE"}
	installLogoutKindHarness(t, h)
	got := captureStdout(t, func() {
		if err := logoutUser(filepath.Join(dir, "pid"), filepath.Join(dir, "authkey"), filepath.Join(dir, "nodes"), dir, true, &bytes.Buffer{}); err != nil {
			t.Fatalf("logoutUser() json error = %v", err)
		}
	})
	data := dataMap(t, got)
	deleted, _ := data["deleted_credentials"].([]any)
	if data["was_logged_in"] != true || len(deleted) != 1 || deleted[0] != credentials.SlotAPIKey {
		t.Fatalf("data = %v", data)
	}
	if _, hasKind := data["kind"]; hasKind {
		t.Fatalf("full logout must omit kind: %v", data)
	}
}

func TestFullLogoutMetadataRemovalFailureIsReported(t *testing.T) {
	dir := t.TempDir()
	h := &logoutKindHarness{apiKey: "tskey-api-FAKE"}
	installLogoutKindHarness(t, h)
	removeCredentialMetadataFn = func() error { return errors.New("permission denied") }
	err := logoutUser(filepath.Join(dir, "pid"), filepath.Join(dir, "authkey"), filepath.Join(dir, "nodes"), dir, false, &bytes.Buffer{})
	if err == nil || !strings.Contains(err.Error(), "remove credential metadata") {
		t.Fatalf("error = %v, want metadata removal failure", err)
	}
}

func TestLogoutHelpAndFlagDescribeKind(t *testing.T) {
	flag := logoutCmd.Flags().Lookup("kind")
	if flag == nil || flag.DefValue != "" || !strings.Contains(flag.Usage, credentials.SlotAPIKey) || !strings.Contains(flag.Usage, credentials.SlotClientSecret) {
		t.Fatalf("logout --kind flag = %+v", flag)
	}
	for _, want := range []string{"--kind api-key", "credential-meta.json", "which slots were deleted"} {
		if !strings.Contains(logoutCmd.Long, want) {
			t.Fatalf("logout help missing %q", want)
		}
	}
}
