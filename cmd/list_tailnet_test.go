package cmd

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/monody0007/tslink/internal/credentials"
	"github.com/monody0007/tslink/internal/inspect"
	"github.com/monody0007/tslink/internal/output"
	"github.com/monody0007/tslink/internal/registry"
	"github.com/monody0007/tslink/internal/tailapi"
	"github.com/monody0007/tslink/internal/testenv"
	tailscale "tailscale.com/client/tailscale/v2"
)

type tailnetListJSONResponse struct {
	OK      bool              `json:"ok"`
	Command string            `json:"command"`
	Data    TailnetListResult `json:"data"`
}

// stubTailnetDevices replaces the tailapi seam so the command layer is
// exercised without any Tailscale API request.
func stubTailnetDevices(t *testing.T, devices []tailapi.TailnetDevice, err error) *int {
	t.Helper()
	calls := 0
	old := listTailnetDevicesFn
	t.Cleanup(func() { listTailnetDevicesFn = old })
	listTailnetDevicesFn = func(context.Context) ([]tailapi.TailnetDevice, error) {
		calls++
		return devices, err
	}
	return &calls
}

// tailnetRegistryWith writes an isolated registry holding the named proxy
// services and points the list command at it.
func tailnetRegistryWith(t *testing.T, names ...string) string {
	t.Helper()
	regPath := filepath.Join(t.TempDir(), "registry.json")
	for _, name := range names {
		if _, err := registry.Add(regPath, registry.Service{
			Name:   name,
			Type:   registry.TypeProxy,
			Target: "http://localhost:3000",
		}); err != nil {
			t.Fatalf("registry.Add(%q): %v", name, err)
		}
	}
	old := registryPathFn
	t.Cleanup(func() { registryPathFn = old })
	registryPathFn = func() (string, error) { return regPath, nil }
	return regPath
}

// runListCmd executes the real list command through the root command with the
// given arguments, capturing stdout for the --json envelope.
func runListCmd(t *testing.T, args ...string) (string, error) {
	t.Helper()
	listCmd, _, err := rootCmd.Find([]string{"list"})
	if err != nil {
		t.Fatalf("find list command: %v", err)
	}
	resetRootJSONFlag(t)
	resetCommandLocalFlags(t, listCmd)
	t.Cleanup(func() { rootCmd.SetArgs(nil) })

	var runErr error
	stdout := captureStdout(t, func() {
		rootCmd.SetArgs(append([]string{"list"}, args...))
		runErr = rootCmd.Execute()
	})
	return stdout, runErr
}

func TestListTailnetJSONReportsOriginRegistrationAndCleanupAuthority(t *testing.T) {
	tailnetRegistryWith(t, "web")
	created := time.Date(2026, 8, 1, 9, 14, 0, 0, time.UTC)
	lastSeen := time.Date(2026, 9, 3, 18, 2, 11, 0, time.UTC)
	calls := stubTailnetDevices(t, []tailapi.TailnetDevice{
		{Hostname: "web", Name: "web.example.ts.net", Tags: []string{"tag:tsmain"}, OS: "darwin", CreatedAt: &created, ConnectedToControl: true, Authorized: true},
		{Hostname: "web-1", Tags: []string{"tag:tsmain"}, OS: "darwin", CreatedAt: &created, LastSeenAt: &lastSeen},
		{Hostname: "laptop-docs", Tags: []string{"tag:tslink-docs"}, OS: "linux", CreatedAt: &created, LastSeenAt: &lastSeen},
	}, nil)

	raw, err := runListCmd(t, "--tailnet", "--json")
	if err != nil {
		t.Fatalf("list --tailnet --json returned %v", err)
	}
	if *calls != 1 {
		t.Fatalf("tailnet device reads = %d, want exactly 1", *calls)
	}

	var resp tailnetListJSONResponse
	if err := json.Unmarshal([]byte(raw), &resp); err != nil {
		t.Fatalf("unmarshal list --tailnet --json: %v\nraw: %s", err, raw)
	}
	if !resp.OK || resp.Command != "list" {
		t.Fatalf("envelope = %+v, want ok list envelope", resp)
	}
	data := resp.Data
	if data.SchemaVersion != inspect.SchemaVersion {
		t.Fatalf("schema_version = %q, want %q", data.SchemaVersion, inspect.SchemaVersion)
	}
	if data.Count != 3 || data.RegisteredCount != 1 || data.UnregisteredCount != 2 {
		t.Fatalf("counts = count:%d registered:%d unregistered:%d, want 3/1/2", data.Count, data.RegisteredCount, data.UnregisteredCount)
	}
	if data.CleanupAuthority != listTailnetCleanupAuthority {
		t.Fatalf("cleanup_authority = %q, want the constant ownership statement", data.CleanupAuthority)
	}

	byHostname := map[string]TailnetDeviceView{}
	for _, device := range data.Devices {
		byHostname[device.Hostname] = device
	}
	exact := byHostname["web"]
	if !exact.LocallyRegistered || exact.Origin != listOriginLocalRegistry || exact.LocalService != "web" {
		t.Fatalf("exact hostname row = %+v, want locally registered", exact)
	}
	if exact.LastSeenAt != nil || !exact.ConnectedToControl {
		t.Fatalf("connected row = %+v, want connected with no last-seen value", exact)
	}
	variant := byHostname["web-1"]
	if variant.LocallyRegistered || variant.Origin != listOriginLocalNameVariant || variant.LocalService != "web" {
		t.Fatalf("collision-variant row = %+v, want an unregistered variant of web", variant)
	}
	foreign := byHostname["laptop-docs"]
	if foreign.LocallyRegistered || foreign.Origin != listOriginUnregistered || foreign.LocalService != "" {
		t.Fatalf("foreign row = %+v, want an unregistered row with no local service", foreign)
	}
	if foreign.LastSeenAt == nil || !foreign.LastSeenAt.Equal(lastSeen) || foreign.CreatedAt == nil || !foreign.CreatedAt.Equal(created) {
		t.Fatalf("foreign row timestamps = %+v, want the API-reported last-seen and created times", foreign)
	}
}

func TestListTailnetJSONWithAbsentRegistryMarksEveryDeviceUnregistered(t *testing.T) {
	// No registry.Add call: registry.json does not exist, which is the state of
	// a machine that has registered nothing.
	regPath := filepath.Join(t.TempDir(), "registry.json")
	old := registryPathFn
	t.Cleanup(func() { registryPathFn = old })
	registryPathFn = func() (string, error) { return regPath, nil }
	stubTailnetDevices(t, []tailapi.TailnetDevice{{Hostname: "web", Tags: []string{"tag:tsmain"}}}, nil)

	raw, err := runListCmd(t, "--tailnet", "--json")
	if err != nil {
		t.Fatalf("list --tailnet --json returned %v", err)
	}
	var resp tailnetListJSONResponse
	if err := json.Unmarshal([]byte(raw), &resp); err != nil {
		t.Fatalf("unmarshal: %v\nraw: %s", err, raw)
	}
	if resp.Data.RegisteredCount != 0 || resp.Data.UnregisteredCount != 1 {
		t.Fatalf("counts = %+v, want every device unregistered", resp.Data)
	}
	if resp.Data.Devices[0].Origin != listOriginUnregistered {
		t.Fatalf("origin = %q, want %q", resp.Data.Devices[0].Origin, listOriginUnregistered)
	}
}

func TestListTailnetHumanOutputMarksUnregisteredRowsAndOwnershipLimit(t *testing.T) {
	regPath := tailnetRegistryWith(t, "web")
	created := time.Date(2026, 8, 1, 9, 14, 0, 0, time.UTC)
	stubTailnetDevices(t, []tailapi.TailnetDevice{
		{Hostname: "web", Tags: []string{"tag:tsmain"}, OS: "darwin", CreatedAt: &created, ConnectedToControl: true},
		{Hostname: "web-1", Tags: []string{"tag:tsmain"}, OS: "darwin", CreatedAt: &created},
		{Hostname: "laptop-docs", Tags: []string{"tag:tslink-docs"}, CreatedAt: &created},
	}, nil)

	var buf bytes.Buffer
	if err := listTailnetDevices(context.Background(), regPath, &buf, false); err != nil {
		t.Fatalf("listTailnetDevices: %v", err)
	}
	out := buf.String()
	for _, want := range []string{
		"HOSTNAME", "LOCAL", "LOCAL SERVICE", "LAST SEEN", "CREATED",
		"web", "web-1", "laptop-docs",
		"connected",
		"2 of 3 TSLink-owned tailnet devices are not registered on this machine.",
		listTailnetCleanupAuthority,
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("human output missing %q:\n%s", want, out)
		}
	}
	// The three LOCAL column values must be distinguishable at a glance.
	for _, row := range []struct{ hostname, local string }{
		{"web", "yes"},
		{"web-1", "variant"},
		{"laptop-docs", "no"},
	} {
		found := false
		for _, line := range strings.Split(out, "\n") {
			fields := strings.Fields(line)
			if len(fields) >= 2 && fields[0] == row.hostname {
				found = true
				if fields[1] != row.local {
					t.Fatalf("row %q LOCAL = %q, want %q\n%s", row.hostname, fields[1], row.local, out)
				}
			}
		}
		if !found {
			t.Fatalf("human output has no row for %q:\n%s", row.hostname, out)
		}
	}
}

func TestListTailnetHumanOutputReportsAnEmptyTailnet(t *testing.T) {
	regPath := tailnetRegistryWith(t)
	stubTailnetDevices(t, nil, nil)

	var buf bytes.Buffer
	if err := listTailnetDevices(context.Background(), regPath, &buf, false); err != nil {
		t.Fatalf("listTailnetDevices: %v", err)
	}
	if !strings.Contains(buf.String(), "No TSLink-owned devices found in the tailnet.") {
		t.Fatalf("empty output = %q", buf.String())
	}
}

// TestListTailnetHumanOutputDashesEveryAbsentColumn keeps the table aligned when
// the Tailscale API reports no tags, OS, or timestamps for a device.
func TestListTailnetHumanOutputDashesEveryAbsentColumn(t *testing.T) {
	regPath := tailnetRegistryWith(t)
	stubTailnetDevices(t, []tailapi.TailnetDevice{{Hostname: "sparse"}}, nil)

	var buf bytes.Buffer
	if err := listTailnetDevices(context.Background(), regPath, &buf, false); err != nil {
		t.Fatalf("listTailnetDevices: %v", err)
	}
	var row string
	for _, line := range strings.Split(buf.String(), "\n") {
		if strings.HasPrefix(line, "sparse") {
			row = line
		}
	}
	fields := strings.Fields(row)
	if len(fields) != 7 {
		t.Fatalf("sparse row = %q, want seven columns", row)
	}
	if fields[1] != "no" {
		t.Fatalf("sparse row LOCAL = %q, want no", fields[1])
	}
	for i, field := range fields[2:] {
		if field != "-" {
			t.Fatalf("sparse row column %d = %q, want a dash placeholder: %q", i+2, field, row)
		}
	}
}

func TestListTailnetSurfacesRegistryLoadFailures(t *testing.T) {
	regPath := filepath.Join(t.TempDir(), "registry.json")
	oldPath := registryPathFn
	t.Cleanup(func() { registryPathFn = oldPath })
	registryPathFn = func() (string, error) { return regPath, nil }

	sentinel := errors.New("registry.json is not readable")
	oldLoad := listLoadRegistryFn
	t.Cleanup(func() { listLoadRegistryFn = oldLoad })
	listLoadRegistryFn = func(string) (*registry.Registry, error) { return nil, sentinel }
	stubTailnetDevices(t, []tailapi.TailnetDevice{{Hostname: "web", Tags: []string{"tag:tsmain"}}}, nil)

	if _, err := runListCmd(t, "--tailnet", "--json"); !errors.Is(err, sentinel) {
		t.Fatalf("error = %v, want the registry load failure surfaced rather than every device reported unregistered", err)
	}
}

func TestListTailnetWithoutCredentialFailsWithSharedBootstrapGuidance(t *testing.T) {
	tailnetRegistryWith(t, "web")
	stubTailnetDevices(t, nil, tailapi.ErrNoAPIClient)

	_, err := runListCmd(t, "--tailnet", "--json")
	if err == nil {
		t.Fatal("list --tailnet succeeded without a credential")
	}
	if got := output.ExitCode(err); got != output.ExitAuth {
		t.Fatalf("exit code = %d, want auth %d", got, output.ExitAuth)
	}
	code, ok := registry.ErrorCode(err)
	if !ok || code != output.StableErrorCode(output.ExitAuth) {
		t.Fatalf("stable code = %q ok=%v, want %q", code, ok, output.StableErrorCode(output.ExitAuth))
	}

	next := output.NextCommandsForError(err)
	if len(next) < len(credentials.NextAPIKeyBootstrap()) {
		t.Fatalf("next = %v, want at least the shared api-key bootstrap sequence", next)
	}
	joined := strings.Join(next, "\n")
	for _, want := range []string{credentials.KeysPageURL, "tslink login --api-key-stdin", "tslink login --client-secret-stdin"} {
		if !strings.Contains(joined, want) {
			t.Fatalf("next = %v, want it to carry %q", next, want)
		}
	}

	// The JSON envelope a machine consumer sees carries the same recovery list.
	envelope := output.NewFailureForError("list", err)
	if envelope.Code != output.ExitAuth || envelope.Error == nil {
		t.Fatalf("envelope = %+v, want an auth failure", envelope)
	}
	if strings.Join(envelope.Error.Next, "\n") != joined {
		t.Fatalf("envelope next = %v, human next = %v; both must agree", envelope.Error.Next, next)
	}
	if !strings.Contains(envelope.Error.Message, "requires a stored Tailscale API credential") {
		t.Fatalf("envelope message = %q, want the credential requirement stated", envelope.Error.Message)
	}
}

func TestListTailnetPropagatesNonCredentialErrorsUnchanged(t *testing.T) {
	tailnetRegistryWith(t, "web")
	sentinel := &registry.StableCodeError{Code: registry.CodeAPIForbidden, Err: errors.New("forbidden")}
	stubTailnetDevices(t, nil, sentinel)

	_, err := runListCmd(t, "--tailnet", "--json")
	if !errors.Is(err, sentinel) {
		t.Fatalf("error = %v, want the tailapi error unchanged", err)
	}
	if got := output.ExitCode(err); got != output.ExitAuth {
		t.Fatalf("exit code = %d, want auth %d for api_forbidden", got, output.ExitAuth)
	}
}

func TestListTailnetConflictsWithEveryRegistryFilterFlag(t *testing.T) {
	cases := []struct {
		name string
		args []string
		flag string
	}{
		{"name", []string{"--tailnet", "--name", "web"}, "--name"},
		{"type", []string{"--tailnet", "--type", "proxy"}, "--type"},
		{"fields", []string{"--tailnet", "--fields", "name"}, "--fields"},
		{"verbose", []string{"--tailnet", "--verbose"}, "--verbose"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			tailnetRegistryWith(t, "web")
			calls := stubTailnetDevices(t, nil, nil)

			_, err := runListCmd(t, append(tc.args, "--json")...)
			if err == nil {
				t.Fatalf("list %v succeeded, want a usage error", tc.args)
			}
			if got := output.ExitCode(err); got != output.ExitUsage {
				t.Fatalf("exit code = %d, want usage %d", got, output.ExitUsage)
			}
			if !strings.Contains(err.Error(), tc.flag) {
				t.Fatalf("error = %v, want it to name %s", err, tc.flag)
			}
			if *calls != 0 {
				t.Fatalf("tailnet device reads = %d, want none for a rejected flag combination", *calls)
			}
		})
	}
}

func TestListDefaultPathNeverReadsTheTailnet(t *testing.T) {
	tailnetRegistryWith(t, "web")
	calls := stubTailnetDevices(t, nil, errors.New("the default list must not reach the tailnet"))

	raw, err := runListCmd(t, "--json")
	if err != nil {
		t.Fatalf("list --json returned %v", err)
	}
	if *calls != 0 {
		t.Fatalf("tailnet device reads = %d, want none for the default list", *calls)
	}
	resp := parseListJSONResponse(t, raw)
	if resp.Data.Count != 1 || resp.Data.Services[0].Name != "web" {
		t.Fatalf("default list data = %+v, want the unchanged registry projection", resp.Data)
	}
	if strings.Contains(raw, "cleanup_authority") || strings.Contains(raw, "\"devices\"") {
		t.Fatalf("default list emitted tailnet fields: %s", raw)
	}
}

// TestListTailnetUsesTheRealTailapiClientPath drives the command against the
// stateful loopback tailnet with no seam stubbed, so it proves the flag reaches
// the existing internal/tailapi client construction rather than a second one.
func TestListTailnetUsesTheRealTailapiClientPath(t *testing.T) {
	tailnetRegistryWith(t, "web")
	fake := testenv.NewStatefulTailnet(t)
	t.Setenv(tailapi.APIBaseURLEnv, fake.URL())
	t.Setenv("TSLINK_API_KEY", "test-placeholder")
	fake.SetDevices([]tailscale.Device{
		{NodeID: "node-web", Hostname: "web", Tags: []string{"tag:tsmain"}},
		{NodeID: "node-orphan", Hostname: "web-1", Tags: []string{"tag:tsmain"}},
		{NodeID: "node-foreign", Hostname: "not-tslink", Tags: []string{"tag:other"}},
	})

	raw, err := runListCmd(t, "--tailnet", "--json")
	if err != nil {
		t.Fatalf("list --tailnet --json returned %v", err)
	}
	var resp tailnetListJSONResponse
	if err := json.Unmarshal([]byte(raw), &resp); err != nil {
		t.Fatalf("unmarshal: %v\nraw: %s", err, raw)
	}
	if resp.Data.Count != 2 || resp.Data.RegisteredCount != 1 || resp.Data.UnregisteredCount != 1 {
		t.Fatalf("data = %+v, want the two TSLink-tagged devices with one registered", resp.Data)
	}
	if strings.Contains(raw, "node-web") || strings.Contains(raw, "node-orphan") {
		t.Fatalf("list --tailnet emitted a NodeID: %s", raw)
	}
	// Read-only against the fake tailnet: nothing was deleted.
	if len(fake.Devices()) != 3 {
		t.Fatalf("fake devices after list --tailnet = %d, want all 3 retained", len(fake.Devices()))
	}
	for _, request := range fake.Requests() {
		if request.Method != "GET" || request.Path != testenv.TailnetDevicesPath {
			t.Fatalf("list --tailnet issued %s %s, want only GET %s", request.Method, request.Path, testenv.TailnetDevicesPath)
		}
	}
}
