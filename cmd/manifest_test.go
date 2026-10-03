package cmd

import (
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"runtime"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/anydoor7/tslink/internal/config"
	"github.com/anydoor7/tslink/internal/credentials"
	"github.com/anydoor7/tslink/internal/inspect"
	"github.com/anydoor7/tslink/internal/output"
	"github.com/anydoor7/tslink/internal/registry"
	tsruntime "github.com/anydoor7/tslink/internal/runtime"
	"github.com/anydoor7/tslink/internal/security"
	"github.com/spf13/cobra"
)

func TestManifestCarriesGeneratingBinaryPlatform(t *testing.T) {
	m := Manifest()
	if m.SchemaVersion != 2 {
		t.Fatalf("manifest schema version = %d, want 2 for required platform provenance plus entry qualifiers", m.SchemaVersion)
	}
	if m.Platform.GOOS != runtime.GOOS || m.Platform.GOARCH != runtime.GOARCH {
		t.Fatalf("manifest platform = %s/%s, want generating binary %s/%s", m.Platform.GOOS, m.Platform.GOARCH, runtime.GOOS, runtime.GOARCH)
	}
	wantPlatforms := make([]string, 0, len(supportedManifestPlatforms))
	for platform := range supportedManifestPlatforms {
		wantPlatforms = append(wantPlatforms, platform)
	}
	sort.Strings(wantPlatforms)
	if !reflect.DeepEqual(m.SupportedPlatforms, wantPlatforms) {
		t.Fatalf("manifest supported_platforms = %v, want sorted authority keys %v", m.SupportedPlatforms, wantPlatforms)
	}
	if !reflect.DeepEqual(SupportedManifestPlatforms(), wantPlatforms) {
		t.Fatalf("SupportedManifestPlatforms() = %v, want %v", SupportedManifestPlatforms(), wantPlatforms)
	}
	data, err := json.Marshal(m)
	if err != nil {
		t.Fatal(err)
	}
	var wire struct {
		Platform           PlatformInfo `json:"platform"`
		SupportedPlatforms []string     `json:"supported_platforms"`
	}
	if err := json.Unmarshal(data, &wire); err != nil {
		t.Fatal(err)
	}
	if wire.Platform != m.Platform {
		t.Fatalf("serialized manifest platform = %+v, want %+v", wire.Platform, m.Platform)
	}
	if !reflect.DeepEqual(wire.SupportedPlatforms, wantPlatforms) {
		t.Fatalf("serialized supported_platforms = %v, want %v", wire.SupportedPlatforms, wantPlatforms)
	}

	compact := CompactManifest()
	if compact.Platform != m.Platform {
		t.Fatalf("compact manifest platform = %+v, want %+v", compact.Platform, m.Platform)
	}
}

func TestManifestInstallNoAutoProvisionFlagIsPlatformNeutral(t *testing.T) {
	for _, command := range Manifest().Commands {
		if command.Path != "tslink install" {
			continue
		}
		for _, flag := range command.Flags {
			if flag.Name == "no-auto-provision" {
				if len(flag.Platforms) != 0 {
					t.Fatalf("tslink install --no-auto-provision platforms = %v, want no platform mark", flag.Platforms)
				}
				return
			}
		}
		t.Fatal("tslink install is missing --no-auto-provision")
	}
	t.Fatal("manifest is missing tslink install")
}

func TestManifestDescribesDaemonStateOwnershipAvailabilityAndAdoptionConflictCount(t *testing.T) {
	commands := make(map[string]CommandInfo)
	for _, command := range Manifest().Commands {
		commands[command.Path] = command
	}
	statusFields := commands["tslink status"].JSONResultFields
	if got := statusFields["daemon_state"]; !reflect.DeepEqual(got.Values, []string{daemonStateRunning, daemonStateAbsent, daemonStateUnknown}) {
		t.Fatalf("daemon_state manifest = %+v", got)
	}
	if got := statusFields["ownership_proof_available"]; got.Type != "boolean" {
		t.Fatalf("ownership_proof_available manifest = %+v", got)
	}
	if got := commands["tslink cleanup"].JSONResultFields["error.data.matches"]; got.Type != "integer" {
		t.Fatalf("cleanup error.data.matches manifest = %+v", got)
	}
	if got := commands["tslink cleanup"].JSONResultFields["device_skip_unknown_provenance"]; got.Type != "array" {
		t.Fatalf("cleanup device_skip_unknown_provenance manifest = %+v", got)
	}
	if got := commands["tslink cleanup"].JSONResultFields["device_skip_reason"].Description; !strings.Contains(got, "structural registry distrust takes priority over ownership-ledger unavailability") {
		t.Fatalf("cleanup device_skip_reason priority contract = %q", got)
	}
}

func TestManifestPlatformMarksAreExactAndProseDerived(t *testing.T) {
	m := Manifest()
	commands := make(map[string]CommandInfo, len(m.Commands))
	var markedFlags []string
	for _, command := range m.Commands {
		commands[command.Path] = command
		for _, flag := range command.Flags {
			if len(flag.Platforms) > 0 {
				markedFlags = append(markedFlags, command.Path+" --"+flag.Name)
			}
		}
	}
	wantByPlatform := map[string][]string{
		"darwin":  {"tslink install --force", "tslink uninstall --force"},
		"linux":   nil,
		"windows": {"tslink install --startup"},
	}
	want := wantByPlatform[runtime.GOOS]
	if !reflect.DeepEqual(markedFlags, want) {
		t.Fatalf("%s marked flags = %v, want exactly %v", runtime.GOOS, markedFlags, want)
	}
	for _, key := range want {
		parts := strings.Split(key, " --")
		var got FlagInfo
		for _, flag := range commands[parts[0]].Flags {
			if flag.Name == parts[1] {
				got = flag
			}
		}
		if !reflect.DeepEqual(got.Platforms, []string{runtime.GOOS}) {
			t.Fatalf("%s platforms = %v, want [%s]", key, got.Platforms, runtime.GOOS)
		}
	}

	for _, flag := range commands["tslink tags delete-remote"].Flags {
		if flag.Name == "force" && len(flag.Platforms) != 0 {
			t.Fatalf("tslink tags delete-remote --force platforms = %v, want unmarked", flag.Platforms)
		}
	}

	wantFields := map[string]map[string][]string{
		"tslink install": {
			"force_available": {"darwin"}, "force_command": {"darwin"}, "force_risk": {"darwin"},
			"launchctl_output": {"darwin"}, "launchctl_target": {"darwin"}, "loaded": {"darwin"},
			"plist_path": {"darwin"}, "unavailable_domain": {"darwin"},
			"installed": {"linux", "windows"}, "path": {"linux", "windows"},
			"service_manager": {"linux", "windows"}, "started": {"linux", "windows"},
		},
		"tslink uninstall": {
			"detail": {"darwin"}, "force_available": {"darwin"}, "force_command": {"darwin"},
			"force_risk": {"darwin"}, "launchctl_outcome": {"darwin"}, "launchctl_output": {"darwin"},
			"launchctl_target": {"darwin"}, "plist_path": {"darwin"}, "unavailable_domain": {"darwin"},
			"path": {"linux", "windows"}, "service_manager": {"linux", "windows"},
		},
	}
	markedFieldCount := 0
	for commandPath, wantMarks := range wantFields {
		fields := commands[commandPath].JSONResultFields
		for name, wantPlatforms := range wantMarks {
			field, ok := fields[name]
			if !ok {
				t.Fatalf("%s missing marked JSON result field %q", commandPath, name)
			}
			if !reflect.DeepEqual(field.Platforms, wantPlatforms) {
				t.Fatalf("%s %s platforms = %v, want %v", commandPath, name, field.Platforms, wantPlatforms)
			}
		}
	}
	// The production slice, not a hand copy of it: a second literal here would
	// drift silently, and this loop is a backstop rather than a second opinion --
	// Manifest() above already panics on any field the production tripwire
	// rejects, so reaching this point means every field passed it.
	platformWords := resultFieldPlatformTripwires
	for _, command := range m.Commands {
		for name, field := range command.JSONResultFields {
			if len(field.Platforms) > 0 {
				wantPlatforms, ok := wantFields[command.Path][name]
				if !ok {
					t.Fatalf("unexpected marked JSON result field %s %s: %+v", command.Path, name, field)
				}
				if !reflect.DeepEqual(field.Platforms, wantPlatforms) {
					t.Fatalf("%s %s platforms = %v, want %v", command.Path, name, field.Platforms, wantPlatforms)
				}
				markedFieldCount++
				continue
			}
			lowered := strings.ToLower(field.Description)
			for _, word := range platformWords {
				if strings.Contains(lowered, strings.ToLower(word)) {
					t.Fatalf("%s %s description contains platform word %q without a mark: %+v", command.Path, name, word, field)
				}
			}
		}
	}
	if markedFieldCount != 23 {
		t.Fatalf("marked JSON result fields = %d, want 23", markedFieldCount)
	}
	if runtime.GOOS == "darwin" && markedFieldCount+len(markedFlags) != 25 {
		t.Fatalf("Darwin total marked entries = %d, want 25 (2 flags + 23 result fields)", markedFieldCount+len(markedFlags))
	}
}

func TestResultFieldPlatformWordTripwireRejectsUnmarkedScopePhrase(t *testing.T) {
	defer func() {
		value := recover()
		if value == nil {
			t.Fatal("platform-word tripwire accepted an unmarked result field")
		}
		if !strings.Contains(value.(string), `description contains platform word "macOS"`) {
			t.Fatalf("tripwire panic = %v, want unmarked macOS diagnosis", value)
		}
	}()
	fields := markProseScopedJSONResultFields(map[string]JSONResultFieldInfo{
		"launchd_session": {
			Type:        "string",
			Description: "Only available on macOS. Synthetic probe field added by review-21 to test predicate blindness.",
		},
	})
	mustValidateJSONResultFieldPlatformMarks("tslink uninstall", fields)
}

// TestResultFieldPlatformWordTripwireCoverage pins what the prose tripwire does
// and does not catch. The escape column is the point of the test: the tripwire
// matches a word list, so platform-scoped prose that names no listed word is
// published as available everywhere. Writing the escapes down keeps that surface
// visible instead of leaving it as a property nobody has measured.
//
// Shrinking the escape column is an improvement; growing it is a regression.
// Removing it entirely means deriving the marks from the per-GOOS structs
// instead of from prose -- see resultFieldPlatformTripwires.
func TestResultFieldPlatformWordTripwireCoverage(t *testing.T) {
	cases := []struct {
		description string
		wantPanic   bool
	}{
		// Recognized leading clauses produce a mark, so the tripwire skips them.
		{"macOS only. control", false},
		{"macOS failure only. control", false},
		{"Linux and Windows only. control", false},

		// Caught: a listed word with no recognized clause, in any casing.
		{"Only populated on darwin.", true},
		{"Darwin only. Path to the launch agent property list.", true},
		{"linux and windows only. Path to the startup artifact.", true},
		{"macos only. Whether the agent was loaded.", true},
		{"Set on WINDOWS only.", true},
		{"The plist path.", true},

		// Escapes: platform-scoped prose naming no listed word. Each of these is
		// published as available on every supported platform.
		{"Only set when the Startup folder shortcut is written.", false},
		{"Present only when Task Scheduler owns the service.", false},
		{"Populated when the Apple property list is loaded by the user domain agent.", false},
		{"Only meaningful under XDG autostart.", false},
		{"Set only on Unix-like hosts.", false},
		{"Only present when Homebrew installed the binary.", false},
		{"Populated only on POSIX platforms.", false},
		{"Set when sc.exe reports the service state.", false},
		{"Only on non-Apple platforms.", false},
		{"Emitted by the user service manager only.", false},
	}

	for _, tc := range cases {
		panicked := func() (panicked bool) {
			defer func() {
				panicked = recover() != nil
			}()
			fields := markProseScopedJSONResultFields(map[string]JSONResultFieldInfo{
				"probe": {Type: "string", Description: tc.description},
			})
			mustValidateJSONResultFieldPlatformMarks("tslink probe", fields)
			return false
		}()
		if panicked != tc.wantPanic {
			verb := "escaped"
			if panicked {
				verb = "was rejected"
			}
			t.Errorf("description %q %s; wantPanic=%v", tc.description, verb, tc.wantPanic)
		}
	}
}

func TestPlatformMarkPropagatesToEveryInheritedPersistentFlagEntry(t *testing.T) {
	root := &cobra.Command{Use: "tslink"}
	child := &cobra.Command{Use: "child"}
	grandchild := &cobra.Command{Use: "grandchild"}
	root.PersistentFlags().Bool("platform-probe", false, "synthetic persistent flag")
	mustMarkFlagPlatforms(root, "platform-probe", "darwin", "linux")
	root.AddCommand(child)
	child.AddCommand(grandchild)

	for _, test := range []struct {
		command *cobra.Command
		path    string
	}{{root, "tslink"}, {child, "tslink child"}, {grandchild, "tslink child grandchild"}} {
		var found bool
		for _, flag := range commandFlags(test.command, test.path) {
			if flag.Name != "platform-probe" {
				continue
			}
			found = true
			if !reflect.DeepEqual(flag.Platforms, []string{"darwin", "linux"}) {
				t.Fatalf("%s inherited platforms = %v, want [darwin linux]", test.path, flag.Platforms)
			}
		}
		if !found {
			t.Fatalf("%s missing inherited platform-probe", test.path)
		}
	}
}

func TestMustMarkFlagPlatformsRejectsInheritedParentFlag(t *testing.T) {
	root := &cobra.Command{Use: "tslink"}
	child := &cobra.Command{Use: "child"}
	sibling := &cobra.Command{Use: "sibling"}
	root.PersistentFlags().Bool("force", false, "root persistent force")
	root.AddCommand(child, sibling)
	_ = commandFlags(child, "tslink child")

	defer func() {
		value := recover()
		if value == nil {
			t.Fatal("marking an inherited parent flag did not panic")
		}
		if !strings.Contains(value.(string), "mark tslink child --force: not registered on this command") {
			t.Fatalf("panic = %v, want command-ownership diagnosis", value)
		}
		if got := flagManifestPlatforms(root.PersistentFlags().Lookup("force")); len(got) != 0 {
			t.Fatalf("parent --force leaked platforms after rejected mark: %v", got)
		}
		for _, flag := range commandFlags(sibling, "tslink sibling") {
			if flag.Name == "force" && len(flag.Platforms) != 0 {
				t.Fatalf("sibling inherited --force leaked platforms after rejected mark: %v", flag.Platforms)
			}
		}
	}()
	mustMarkFlagPlatforms(child, "force", "darwin")
}

func TestMustMarkFlagPlatformsKeepsIndependentlyRegisteredSameNameFlagsIsolated(t *testing.T) {
	root := &cobra.Command{Use: "tslink"}
	a := &cobra.Command{Use: "a"}
	b := &cobra.Command{Use: "b"}
	a.Flags().Bool("force", false, "a force")
	b.Flags().Bool("force", false, "b force")
	root.AddCommand(a, b)
	_ = commandFlags(a, "tslink a")
	_ = commandFlags(b, "tslink b")

	mustMarkFlagPlatforms(a, "force", "darwin")
	if got := flagManifestPlatforms(a.Flags().Lookup("force")); !reflect.DeepEqual(got, []string{"darwin"}) {
		t.Fatalf("a --force platforms = %v, want [darwin]", got)
	}
	if got := flagManifestPlatforms(b.Flags().Lookup("force")); len(got) != 0 {
		t.Fatalf("b --force platforms = %v, want independent unmarked flag", got)
	}
}

func TestManifestCarriesMachineConsumerFacts(t *testing.T) {
	m := Manifest()
	commandPaths := make(map[string]bool, len(m.Commands))
	for _, command := range m.Commands {
		commandPaths[command.Path] = true
	}
	if !commandPaths["tslink registry check"] {
		t.Fatal("manifest missing executable tslink registry check recovery command")
	}

	if len(m.RegistrySchema.ServiceTypes) == 0 {
		t.Fatalf("registry schema is incomplete: %#v", m.RegistrySchema)
	}
	for _, code := range []string{
		registry.CodeFunnelCapabilityMissing,
		registry.CodeFunnelListenFailed,
		registry.CodeServiceStartTimeout,
		registry.CodePathNotFound,
		registry.CodePathNotDirectory,
		registry.CodePathNotAccessible,
	} {
		if _, ok := m.ErrorCodes[code]; !ok {
			t.Fatalf("manifest missing error code %q", code)
		}
	}
	for _, command := range []string{"tslink list", "tslink status"} {
		fields := commandJSONResultFields(command)
		for _, field := range []string{"services[].funnel_requested", "services[].funnel_active", "services[].funnel_state", "services[].error"} {
			if _, ok := fields[field]; !ok {
				t.Fatalf("%s manifest missing %s", command, field)
			}
		}
	}
	if _, ok := commandJSONResultFields("tslink list")["services[].state"]; !ok {
		t.Fatal("tslink list manifest missing services[].state")
	}
	if _, ok := commandJSONResultFields("tslink status")["services[].runtime_state"]; !ok {
		t.Fatal("tslink status manifest missing services[].runtime_state")
	}
	if _, ok := commandJSONResultFields("tslink status")["global_error"]; !ok {
		t.Fatal("tslink status manifest missing global_error")
	}
}

func TestManifestDocumentsInstallJSONWireFields(t *testing.T) {
	fields := commandJSONResultFields("tslink install")
	wire := reflect.TypeOf(InstallResult{})
	for i := 0; i < wire.NumField(); i++ {
		tag := strings.Split(wire.Field(i).Tag.Get("json"), ",")[0]
		if tag == "" || tag == "-" {
			continue
		}
		info, ok := fields[tag]
		if !ok {
			t.Fatalf("install manifest missing emitted wire field %q from InstallResult.%s", tag, wire.Field(i).Name)
		}
		if strings.TrimSpace(info.Type) == "" || strings.TrimSpace(info.Description) == "" {
			t.Fatalf("install manifest field %q is incomplete: %+v", tag, info)
		}
	}
	if warning := fields["warning"].Description; !strings.Contains(warning, "non-fatal") || !strings.Contains(warning, "successful install") {
		t.Fatalf("install warning contract = %q, want success-only non-fatal semantics", warning)
	}
}

func TestListStateManifestValuesMatchProductionBranches(t *testing.T) {
	field := commandJSONResultFields("tslink list")["services[].state"]
	declared := append([]string(nil), field.Values...)
	sort.Strings(declared)

	actualSet := map[string]struct{}{}
	for _, service := range []StatusServiceView{
		{},
		{RuntimeState: tsruntime.ServiceRuntimeFailed},
		{Endpoint: inspect.EndpointView{State: inspect.EndpointStateExact, Display: "https://svc.tailnet.ts.net"}},
	} {
		actualSet[listSummary(service).State] = struct{}{}
	}
	actual := make([]string, 0, len(actualSet))
	for state := range actualSet {
		actual = append(actual, state)
	}
	sort.Strings(actual)
	if !reflect.DeepEqual(declared, actual) {
		t.Fatalf("list state manifest values = %v, production branches emit %v", declared, actual)
	}
}

func TestAllManifestValuesMatchProductionOutputSets(t *testing.T) {
	listStates := map[string]struct{}{}
	for _, service := range []StatusServiceView{
		{},
		{RuntimeState: tsruntime.ServiceRuntimeFailed},
		{Endpoint: inspect.EndpointView{State: inspect.EndpointStateExact, Display: "https://svc.tailnet.ts.net"}},
	} {
		listStates[listSummary(service).State] = struct{}{}
	}

	funnelStates := map[string]struct{}{}
	for _, state := range []string{
		tsruntime.FunnelStateNotRequested,
		tsruntime.FunnelStateRequestedUnknown,
		tsruntime.FunnelStateActive,
		tsruntime.FunnelStateCapabilityMissing,
		tsruntime.FunnelStateListenFailed,
		tsruntime.FunnelStateStartTimeout,
	} {
		snapshot := tsruntime.NewSnapshot(1, time.Unix(1, 0), "fingerprint", time.Unix(2, 0), []tsruntime.ServiceState{{
			Service:     registry.Service{Name: "svc", Type: registry.TypeProxy, Target: "http://localhost:3000", Funnel: state != tsruntime.FunnelStateNotRequested, PublicAck: state != tsruntime.FunnelStateNotRequested},
			FunnelState: state,
		}})
		funnelStates[snapshot.Services[0].FunnelState] = struct{}{}
	}

	// Independent derivations for the two additive tailnet/SSH enums: drive the
	// production classifiers rather than restating their constants.
	tailnetOrigins := map[string]struct{}{}
	for _, hostname := range []string{"web", "web-1", "someone-elses-service"} {
		origin, _ := classifyTailnetDeviceOrigin(hostname, []registry.Service{{Name: "web"}})
		tailnetOrigins[origin] = struct{}{}
	}

	tailscaleSSHStates := map[string]struct{}{}
	for _, outcome := range []struct {
		enabled bool
		err     error
	}{{enabled: true}, {}, {err: errors.New("local Tailscale client unreachable")}} {
		stubDoctorTailscaleSSH(t, outcome.enabled, outcome.err)
		var sshResult DoctorResult
		diagnoseTailscaleSSH(&sshResult)
		tailscaleSSHStates[sshResult.TailscaleSSH.State] = struct{}{}
	}

	production := map[string]map[string]struct{}{
		"tslink apps share/action":              sliceSet([]string{templateActionCreate, templateActionCreated, templateActionSkipExisting}),
		"tslink status/daemon_state":            sliceSet([]string{daemonStateRunning, daemonStateAbsent, daemonStateUnknown}),
		"tslink list/services[].state":          listStates,
		"tslink list/services[].funnel_state":   funnelStates,
		"tslink status/services[].funnel_state": funnelStates,
		"tslink status/services[].runtime_state": sliceSet([]string{
			statusEndpointStateUnknown,
			normalizedRuntimeState(tsruntime.ServiceSnapshot{RuntimeState: tsruntime.ServiceRuntimeRunning}),
			normalizedRuntimeState(tsruntime.ServiceSnapshot{RuntimeState: tsruntime.ServiceRuntimeFailed}),
		}),
		"tslink uninstall/launchctl_outcome": sliceSet([]string{
			launchctlOutcomeNotInstalled,
			launchctlOutcomeUnloaded,
			launchctlOutcomeAlreadyAbsent,
			launchctlOutcomeUnconfirmed,
		}),
		// Credential-lifecycle enum fields. Their production truth is the same
		// first-party constant set the manifest advertises, compared here from an
		// independent reference exactly like daemon_state above.
		"tslink login/credential_kind":          sliceSet([]string{credentials.KindAPIAccessToken, credentials.KindOAuthClientSecret}),
		"tslink login/expires_at_source":        sliceSet([]string{credentials.ExpirySourceUser, credentials.ExpirySourceAssumedMax}),
		"tslink login/retired_credential":       sliceSet([]string{credentials.SlotAPIKey, credentials.SlotClientSecret}),
		"tslink login/page":                     sliceSet([]string{loginBootstrapPageKeys, loginBootstrapPageOAuth}),
		"tslink logout/kind":                    sliceSet([]string{credentials.SlotAPIKey, credentials.SlotClientSecret}),
		"tslink status/credential_expiry_state": sliceSet(credentialExpiryStateValues()),
		"tslink list/devices[].origin":          tailnetOrigins,
		"tslink doctor/tailscale_ssh.state":     tailscaleSSHStates,
	}

	seen := map[string]bool{}
	for _, command := range Manifest().Commands {
		for field, info := range command.JSONResultFields {
			if len(info.Values) == 0 {
				continue
			}
			key := command.Path + "/" + field
			actual, ok := production[key]
			if !ok {
				t.Fatalf("manifest Values field %q has no production output-set comparison", key)
			}
			if !reflect.DeepEqual(sliceSet(info.Values), actual) {
				t.Fatalf("%s values=%v production=%v", key, info.Values, sortedSet(actual))
			}
			seen[key] = true
		}
	}
	if len(seen) != len(production) {
		t.Fatalf("compared=%v, want all production sets=%v", seen, production)
	}
}

func sliceSet(values []string) map[string]struct{} {
	result := make(map[string]struct{}, len(values))
	for _, value := range values {
		result[value] = struct{}{}
	}
	return result
}

func sortedSet(values map[string]struct{}) []string {
	result := make([]string, 0, len(values))
	for value := range values {
		result = append(result, value)
	}
	sort.Strings(result)
	return result
}

func TestManifestErrorExitTaxonomyMatchesRuntime(t *testing.T) {
	tests := map[string]error{
		"access_request_busy":                   registry.CodedError{Code: "access_request_busy", Message: "request writer busy"},
		"access_request_capacity":               registry.CodedError{Code: "access_request_capacity", Message: "bounded inbox full"},
		"access_request_decided":                registry.CodedError{Code: "access_request_decided", Message: "request already decided"},
		"access_request_duplicate":              registry.CodedError{Code: "access_request_duplicate", Message: "request already pending"},
		"access_request_rate_limited":           registry.CodedError{Code: "access_request_rate_limited", Message: "submission rate exceeded"},
		"access_request_unavailable":            registry.CodedError{Code: "access_request_unavailable", Message: "app unavailable"},
		"access_request_owner_required":         registry.CodedError{Code: "access_request_owner_required", Message: "owner required"},
		"portal_funnel_refused":                 registry.ValidatePortal(&registry.PortalConfig{Enabled: true, Hostname: "home", Owner: "owner", Funnel: true}),
		"portal_identity_invalid":               registry.ValidatePortal(&registry.PortalConfig{Hostname: "home", Owner: "Owner"}),
		"portal_hostname_conflict":              registry.CodedError{Code: "portal_hostname_conflict", Message: "hostname collision"},
		"mcp_person_owner_required":             registry.CodedError{Code: "mcp_person_owner_required", Message: "The owner must add the person first"},
		"mcp_scope_denied":                      registry.CodedError{Code: "mcp_scope_denied", Message: "scope refusal"},
		"mcp_audit_unavailable":                 mcpAuditUnavailable(false),
		"people_service_unsupported":            registry.ValidateService(registry.Service{Name: "db", Type: registry.TypeTCP, Target: "localhost:5432", PeopleScoped: true}),
		"invite_failed":                         registry.CodedError{Code: "invite_failed", Message: "bundle failure"},
		"invite_state_failed":                   registry.CodedError{Code: "invite_state_failed", Message: "state failure"},
		"people_invite_busy":                    registry.CodedError{Code: "people_invite_busy", Message: "remote-work contention"},
		"person_grant_inactive":                 registry.CodedError{Code: "person_grant_inactive", Message: "grant removed"},
		"invite_reconciliation_failed":          registry.CodedError{Code: "invite_reconciliation_failed", Message: "list failure"},
		"invite_reconciliation_required":        registry.CodedError{Code: "invite_reconciliation_required", Message: "unknown POST"},
		"invite_reconciliation_conflict":        registry.CodedError{Code: "invite_reconciliation_conflict", Message: "already associated ID"},
		"invite_link_unavailable":               registry.CodedError{Code: "invite_link_unavailable", Message: "missing saved link"},
		"invite_cleanup_failed":                 registry.CodedError{Code: "invite_cleanup_failed", Message: "remote cleanup failure"},
		registry.CodeInvalidRequestLimits:       registry.CodedError{Code: registry.CodeInvalidRequestLimits, Message: "unlimited requires acknowledgement"},
		"internal_error":                        errors.New("boom"),
		"usage_error":                           output.ErrUsage("bad usage"),
		"auth_error":                            output.ErrAuth("bad auth"),
		"conflict":                              output.ErrConflict("conflict"),
		"not_found":                             output.ErrNotFound("missing"),
		registry.CodeServiceTypeAmbiguous:       registry.ServiceTypeAmbiguousError(),
		registry.CodeInvalidServiceName:         registry.ValidateName("Bad_Name"),
		registry.CodeInvalidTag:                 registry.ValidateTag("tag:"),
		registry.CodeAllowUnsupportedTCP:        registry.AllowUnsupportedTCPError(),
		registry.CodePathMustBeAbsolute:         registry.PathMustBeAbsoluteError("relative"),
		registry.CodePathNotFound:               registry.PathNotFoundError("/missing"),
		registry.CodePathNotDirectory:           registry.PathNotDirectoryError("/file"),
		registry.CodePathNotAccessible:          registry.PathNotAccessibleError("/denied", errors.New("denied")),
		registry.CodePathExposesConfigDir:       registry.PathExposesConfigDirError("/home/u", "/home/u/.config/tslink"),
		registry.CodeUnknownConfigKey:           fmt.Errorf("unknown config key: %q", "bad"),
		registry.CodeConfigLoadFailed:           &config.ConfigLoadError{Path: "/home/u/.config/tslink/config.json", Problem: `unknown key "default_tags"`},
		registry.CodeURLNotReady:                registry.URLNotReadyError("pending"),
		registry.CodeLaunchctlDomainUnavailable: registry.CodedError{Code: registry.CodeLaunchctlDomainUnavailable, Message: "unavailable"},
		registry.CodeFunnelPublicAckRequired:    registry.FunnelPublicAckError(),
		registry.CodeFunnelExpiryRequired:       registry.FunnelExpiryRequiredError("svc"),
		registry.CodeFunnelAllowConflict:        registry.FunnelAllowedUsersError(),
		registry.CodeFunnelControlURLConflict:   registry.FunnelControlURLError(),
		registry.CodeFunnelTypeConflict:         registry.FunnelTypeConflictError(registry.TypeTCP),
		registry.CodeFunnelCapabilityMissing:    registry.FunnelCapabilityMissingError("svc", errors.New("missing")),
		registry.CodeFunnelListenFailed:         registry.FunnelListenFailedError("svc", errors.New("listen")),
		registry.CodeServiceStartTimeout:        registry.ServiceStartTimeoutError("svc", time.Second),
		registry.CodeInviteAPIKeyRequired:       registry.CodedError{Code: registry.CodeInviteAPIKeyRequired, Message: "API key required"},
		registry.CodeInviteRoleInvalid:          registry.CodedError{Code: registry.CodeInviteRoleInvalid, Message: "role invalid"},
		registry.CodeInviteRecipientInvalid:     registry.CodedError{Code: registry.CodeInviteRecipientInvalid, Message: "recipient invalid"},
		registry.CodeInviteNotFound:             registry.CodedError{Code: registry.CodeInviteNotFound, Message: "invite missing"},
		registry.CodeInviteDeviceAmbiguous:      registry.CodedError{Code: registry.CodeInviteDeviceAmbiguous, Message: "device ambiguous"},
		registry.CodeInviteOwnershipUnproven:    registry.CodedError{Code: registry.CodeInviteOwnershipUnproven, Message: "ownership unproven"},
		registry.CodeInviteAPIForbidden:         registry.CodedError{Code: registry.CodeInviteAPIForbidden, Message: "forbidden"},
		registry.CodeInviteResendEmailMissing:   registry.CodedError{Code: registry.CodeInviteResendEmailMissing, Message: "email missing"},
		registry.CodeInviteIDInvalid:            registry.CodedError{Code: registry.CodeInviteIDInvalid, Message: "ID invalid"},
		registry.CodeInviteKindInvalid:          registry.CodedError{Code: registry.CodeInviteKindInvalid, Message: "kind invalid"},
		registry.CodeInviteRateLimited:          registry.CodedError{Code: registry.CodeInviteRateLimited, Message: "rate limited"},
		registry.CodeInviteStateConflict:        registry.CodedError{Code: registry.CodeInviteStateConflict, Message: "state conflict"},
		registry.CodeInviteRequestInvalid:       registry.CodedError{Code: registry.CodeInviteRequestInvalid, Message: "request invalid"},
		registry.CodeInviteResponseInvalid:      registry.CodedError{Code: registry.CodeInviteResponseInvalid, Message: "response invalid"},
		registry.CodeInviteAPIUnauthorized:      registry.CodedError{Code: registry.CodeInviteAPIUnauthorized, Message: "unauthorized"},
		registry.CodeAPITokenUnauthorized:       &registry.StableCodeError{Code: registry.CodeAPITokenUnauthorized, Err: errors.New("unauthorized")},
		registry.CodeAPIForbidden:               &registry.StableCodeError{Code: registry.CodeAPIForbidden, Err: errors.New("forbidden")},
		registry.CodeLoginVerifyFailed:          &registry.StableCodeError{Code: registry.CodeLoginVerifyFailed, Err: errors.New("verify failed")},
		registry.CodeLegacyConfigDirPresent:     &config.LegacyConfigDirError{Legacy: `C:\Users\u\.config\tslink`, Current: `C:\Users\u\AppData\Roaming\tslink`},
		registry.CodeCredentialURLMismatch:      registry.CodedError{Code: registry.CodeCredentialURLMismatch, Message: "minted key refused for another control server"},
		// Codes the product emitted without a manifest entry until the
		// errcode table (A3-4, A4-3).
		"daemon_not_running":                         daemonNotRunningError(),
		"daemon_setup_failed":                        daemonSetupError(errors.New("unit rejected")),
		"daemon_supervision_unverified":              registry.CodedError{Code: "daemon_supervision_unverified", Message: "supervisor unverified"},
		registry.CodeEnrollmentRequired:              registry.CodedError{Code: registry.CodeEnrollmentRequired, Message: "authorize first"},
		registry.CodeInvalidServiceConfig:            registry.CodedError{Code: registry.CodeInvalidServiceConfig, Message: "invalid entry"},
		registry.CodeLinkLocalTargetRefused:          registry.LinkLocalTargetRefusedError("169.254.169.254:80"),
		registry.CodeRegistryReloadInvalid:           registry.CodedError{Code: registry.CodeRegistryReloadInvalid, Message: "reload invalid"},
		registry.CodeMCPElevatedInviteRefused:        registry.CodedError{Code: registry.CodeMCPElevatedInviteRefused, Message: "needs the owner's opt-in"},
		inspect.WarningCodeRuntimeSnapshotMissing:    &tsruntime.SnapshotError{Status: tsruntime.StatusMissing, Code: inspect.WarningCodeRuntimeSnapshotMissing, Err: errors.New("missing")},
		inspect.WarningCodeRuntimeSnapshotStale:      &tsruntime.SnapshotError{Status: tsruntime.StatusStale, Code: inspect.WarningCodeRuntimeSnapshotStale, Err: errors.New("stale")},
		inspect.WarningCodeRuntimeSnapshotUnreadable: &tsruntime.SnapshotError{Status: tsruntime.StatusUnreadable, Code: inspect.WarningCodeRuntimeSnapshotUnreadable, Err: errors.New("unreadable")},
	}
	manifest := errorCodeManifest()
	if len(manifest) != len(tests) {
		t.Fatalf("manifest codes=%d runtime probes=%d", len(manifest), len(tests))
	}
	for stable, err := range tests {
		info, ok := manifest[stable]
		if !ok {
			t.Fatalf("manifest missing runtime code %q", stable)
		}
		failure := output.NewFailureForError("probe", err)
		if failure.Error == nil || failure.Error.Code != stable || failure.Code != info.ExitCode || output.ExitCode(err) != info.ExitCode {
			t.Fatalf("%s manifest=%+v runtime envelope=%+v runtime exit=%d", stable, info, failure, output.ExitCode(err))
		}
	}
	if got := manifest[registry.CodeInviteRateLimited].ExitCode; got != output.ExitError {
		t.Fatalf("invite_rate_limited exit = %d, want general error %d rather than conflict", got, output.ExitError)
	}
	if got := manifest[registry.CodeInviteStateConflict].ExitCode; got != output.ExitConflict {
		t.Fatalf("invite_state_conflict exit = %d, want conflict %d", got, output.ExitConflict)
	}
	authDescription := manifest[registry.CodeInviteAPIForbidden].Description
	if !strings.Contains(authDescription, "401") || !strings.Contains(authDescription, "403") {
		t.Fatalf("invite_api_forbidden description = %q, want both runtime auth statuses", authDescription)
	}
	if !strings.Contains(authDescription, registry.CodeInviteAPIUnauthorized) {
		t.Fatalf("invite_api_forbidden description = %q, want pointer to the split 401 code", authDescription)
	}
	for _, code := range []string{registry.CodeInviteAPIUnauthorized, registry.CodeAPITokenUnauthorized, registry.CodeAPIForbidden} {
		if got := manifest[code].ExitCode; got != output.ExitAuth {
			t.Fatalf("%s exit = %d, want auth %d", code, got, output.ExitAuth)
		}
	}
	if got := manifest[registry.CodeLoginVerifyFailed].ExitCode; got != output.ExitError {
		t.Fatalf("login_verify_failed exit = %d, want general error %d", got, output.ExitError)
	}
}

func TestManifestExcludesJSONFromMCPCommand(t *testing.T) {
	for _, command := range Manifest().Commands {
		if command.Path != "tslink mcp" {
			continue
		}
		for _, flag := range command.Flags {
			if flag.Name == "json" {
				t.Fatalf("tslink mcp flags=%+v, stdout is reserved for JSON-RPC", command.Flags)
			}
		}
		return
	}
	t.Fatal("manifest missing tslink mcp")
}

func TestLaunchctlDomainUnavailableErrorCodeMatchesPinnedManifestWireValue(t *testing.T) {
	const wireValue = "launchctl_domain_unavailable"
	if registry.CodeLaunchctlDomainUnavailable != wireValue {
		t.Fatalf("CodeLaunchctlDomainUnavailable = %q, want pinned wire value %q", registry.CodeLaunchctlDomainUnavailable, wireValue)
	}
	info, ok := errorCodeManifest()[wireValue]
	if !ok {
		t.Fatalf("error-code manifest missing pinned wire value %q", wireValue)
	}
	if info.ExitCode != 1 || !strings.Contains(info.Description, "--force") {
		t.Fatalf("%s manifest entry = %+v, want exit 1 and explicit recovery semantics", wireValue, info)
	}
}

func TestManifestHighRiskFlagsAndCredentialBoundaries(t *testing.T) {
	m := Manifest()

	assertHighRisk := func(command string, want ...string) {
		t.Helper()
		for _, op := range m.HighRiskOperations {
			if op.Command != command {
				continue
			}
			for _, flag := range want {
				if !containsString(op.RequiredFlags, flag) {
					t.Fatalf("%s required flags = %v, want %s", command, op.RequiredFlags, flag)
				}
			}
			return
		}
		t.Fatalf("high-risk operation for %s not found", command)
	}
	assertHighRisk("tslink login", "--manage-acl")
	assertHighRisk("tslink serve", "--manage-acl")
	assertHighRisk("tslink tags delete-remote", "--force", "--manage-acl")
	assertHighRisk("tslink invite user")
	assertHighRisk("tslink invite device")
	assertHighRisk("tslink invite revoke", "--kind")
	assertHighRisk("tslink invite resend", "--kind")

	var funnelPlan *HighRiskOperation
	for i := range m.HighRiskOperations {
		operation := &m.HighRiskOperations[i]
		if strings.Contains(operation.Operation, "Funnel shared tag owner") {
			funnelPlan = operation
			break
		}
	}
	if funnelPlan == nil || funnelPlan.Command != "tslink serve" || funnelPlan.Default != "enabled" || len(funnelPlan.RequiredFlags) != 0 || !strings.Contains(funnelPlan.Boundary, "--no-auto-provision") {
		t.Fatalf("Funnel high-risk operation = %+v, want default-on operation with kill switch", funnelPlan)
	}
	var remotePlan *security.RemoteSideEffectPlan
	for i := range m.RemoteSideEffectPlans {
		plan := &m.RemoteSideEffectPlans[i]
		if plan.ID == "remote.acl.funnel_auto_provision" {
			remotePlan = plan
			break
		}
	}
	if remotePlan == nil || !remotePlan.Mutates || remotePlan.Default != "enabled" || remotePlan.DisableFlag != "--no-auto-provision" || !containsString(remotePlan.Resources, registry.FunnelTag) {
		t.Fatalf("Funnel remote side-effect plan = %+v, want auditable default-on mutation", remotePlan)
	}
	invitePlanIDs := map[string]bool{
		"remote.invite.user.create":   false,
		"remote.invite.device.create": false,
		"remote.invite.user.revoke":   false,
		"remote.invite.device.revoke": false,
		"remote.invite.user.resend":   false,
		"remote.invite.device.resend": false,
	}
	for _, plan := range m.RemoteSideEffectPlans {
		if _, ok := invitePlanIDs[plan.ID]; ok {
			invitePlanIDs[plan.ID] = plan.Mutates && plan.Default == "explicit_command" && plan.OptInFlag == ""
		}
	}
	for id, valid := range invitePlanIDs {
		if !valid {
			t.Fatalf("manifest invite side-effect plan %s missing or invalid: %+v", id, m.RemoteSideEffectPlans)
		}
	}

	var sawStdin, sawArgvAvoid bool
	for _, src := range m.CredentialSources.Automation {
		if containsString(src.Flags, "--api-key-stdin") || containsString(src.Flags, "--client-secret-stdin") {
			sawStdin = true
		}
	}
	for _, src := range m.CredentialSources.Avoid {
		if containsString(src.Flags, "--api-key") || containsString(src.Flags, "--client-secret") {
			sawArgvAvoid = true
		}
	}
	if !sawStdin || !sawArgvAvoid {
		t.Fatalf("credential boundary must prefer stdin and warn on argv: %#v", m.CredentialSources)
	}
}

func TestManifestCommandsCarryInheritedFlags(t *testing.T) {
	m := Manifest()
	commands := map[string]CommandInfo{}
	for _, c := range m.Commands {
		commands[c.Path] = c
	}

	for _, path := range []string{
		"tslink doctor",
		"tslink access explain",
		"tslink template apply",
		"tslink tags delete-remote",
	} {
		if _, ok := commands[path]; !ok {
			t.Fatalf("manifest missing command %s", path)
		}
	}
	assertFlag := func(command, flag string) {
		t.Helper()
		c, ok := commands[command]
		if !ok {
			t.Fatalf("manifest missing command %s", command)
		}
		for _, f := range c.Flags {
			if f.Name == flag {
				return
			}
		}
		t.Fatalf("%s flags = %#v, want %s", command, c.Flags, flag)
	}
	assertFlag("tslink login", "api-key-stdin")
	assertFlag("tslink login", "client-secret-stdin")
	assertFlag("tslink login", "manage-acl")
	assertFlag("tslink serve", "manage-acl")
	assertFlag("tslink serve", "no-auto-provision")
	assertFlag("tslink install", "no-auto-provision")
	assertFlag("tslink tags delete-remote", "force")
	assertFlag("tslink tags delete-remote", "manage-acl")
	assertFlag("tslink status", "json")
}

func TestCleanupManifestDescribesAdoptionApplyAndEverySkipClass(t *testing.T) {
	manifest := Manifest()
	var adoption *HighRiskOperation
	for i := range manifest.HighRiskOperations {
		operation := &manifest.HighRiskOperations[i]
		if operation.Command == "tslink cleanup" && operation.Operation == "legacy device ownership adoption" {
			adoption = operation
			break
		}
	}
	if adoption == nil || !containsString(adoption.RequiredFlags, "--adopt") || !containsString(adoption.RequiredFlags, "--force") || !containsString(adoption.RequiredFlags, "--dry-run=false") {
		t.Fatalf("adoption operation = %+v, want three explicit apply flags", adoption)
	}
	for _, want := range []string{"preview is read-only", "TSLink-tagged", "absent from registry.json", "currently registered", "without retired_at"} {
		if !strings.Contains(adoption.Boundary, want) {
			t.Fatalf("adoption boundary = %q, missing %q", adoption.Boundary, want)
		}
	}
	description := commandJSONResultFields("tslink cleanup")["device_cleanup_skipped"].Description
	for _, want := range []string{"ownership proof", "registry", "API client", "hostname-only", "remote cleanup"} {
		if !strings.Contains(description, want) {
			t.Fatalf("device_cleanup_skipped description = %q, missing %q", description, want)
		}
	}
	reasonDescription := commandJSONResultFields("tslink cleanup")["device_skip_reason"].Description
	if strings.Contains(reasonDescription, "Stable") || !strings.Contains(reasonDescription, "sorted orphan set") {
		t.Fatalf("device_skip_reason description = %q, want data-dependent human-readable contract", reasonDescription)
	}
}

func TestManifestDocumentsDarwinUninstallJSONContract(t *testing.T) {
	m := Manifest()
	var uninstall CommandInfo
	for _, command := range m.Commands {
		if command.Path == "tslink uninstall" {
			uninstall = command
			break
		}
	}
	if uninstall.Path == "" {
		t.Fatal("manifest missing tslink uninstall")
	}
	for _, field := range []string{
		"plist_path",
		"path",
		"removed",
		"service_manager",
		"warning",
		"launchctl_outcome",
		"launchctl_target",
		"launchctl_output",
		"detail",
		"unavailable_domain",
		"force_available",
		"force_command",
		"force_risk",
	} {
		if _, ok := uninstall.JSONResultFields[field]; !ok {
			t.Fatalf("uninstall manifest missing emitted field %q", field)
		}
	}

	outcome, ok := uninstall.JSONResultFields["launchctl_outcome"]
	if !ok {
		t.Fatal("uninstall manifest missing launchctl_outcome")
	}
	for _, want := range []string{"not_installed", "unloaded", "already_absent", "unconfirmed"} {
		if !containsString(outcome.Values, want) {
			t.Fatalf("launchctl_outcome values = %v, want %q", outcome.Values, want)
		}
	}
	target := uninstall.JSONResultFields["launchctl_target"].Description
	if !strings.Contains(target, "empty for not_installed and already_absent") {
		t.Fatalf("launchctl_target contract = %q", target)
	}
	rawOutput := uninstall.JSONResultFields["launchctl_output"].Description
	if !strings.Contains(rawOutput, "Verbatim trimmed launchctl output") || !strings.Contains(rawOutput, "already_absent") || !strings.Contains(rawOutput, "omitted") {
		t.Fatalf("launchctl_output contract = %q", rawOutput)
	}
	detail := uninstall.JSONResultFields["detail"].Description
	if !strings.Contains(detail, "TSLink-authored") || !strings.Contains(detail, "separate") {
		t.Fatalf("detail contract = %q", detail)
	}
	warning := uninstall.JSONResultFields["warning"].Description
	if !strings.Contains(warning, "non-fatal") || !strings.Contains(warning, "successful") || !strings.Contains(warning, "omitted on failures") {
		t.Fatalf("uninstall warning contract = %q, want success-only non-fatal semantics", warning)
	}
}

func TestManifestFlagsAreSelfDescribingAndRelationshipsAreExplicit(t *testing.T) {
	m := Manifest()
	commands := map[string]CommandInfo{}
	for _, command := range m.Commands {
		commands[command.Path] = command
		for _, flag := range command.Flags {
			if strings.TrimSpace(flag.Usage) == "" {
				t.Fatalf("%s --%s has empty usage", command.Path, flag.Name)
			}
		}
	}

	flag := func(command, name string) FlagInfo {
		t.Helper()
		for _, candidate := range commands[command].Flags {
			if candidate.Name == name {
				return candidate
			}
		}
		t.Fatalf("%s missing --%s", command, name)
		return FlagInfo{}
	}
	proxy := flag("tslink add", "proxy")
	if !containsString(proxy.OneOf, "--dir") || !containsString(proxy.OneOf, "--tcp") {
		t.Fatalf("add --proxy one_of = %v", proxy.OneOf)
	}
	funnel := flag("tslink add", "funnel")
	if !containsString(funnel.Requires, "--public") || !containsString(funnel.Conflicts, "--allow") {
		t.Fatalf("add --funnel relationships = %+v", funnel)
	}
	noAutoProvision := flag("tslink add", "no-auto-provision")
	if !containsString(noAutoProvision.Requires, "--funnel") {
		t.Fatalf("add --no-auto-provision relationships = %+v", noAutoProvision)
	}
	showURLs := flag("tslink invite list", "show-urls")
	if showURLs.Default != "false" || !strings.Contains(showURLs.Usage, "bearer invite URLs") {
		t.Fatalf("invite list --show-urls = %+v, want explicit default-off bearer disclosure", showURLs)
	}
	for _, command := range []string{"tslink invite revoke", "tslink invite resend"} {
		kind := flag(command, "kind")
		if kind.Default != "" || !strings.Contains(kind.Usage, "Required invite namespace") {
			t.Fatalf("%s --kind = %+v, want explicit required namespace", command, kind)
		}
	}
	listFields := commands["tslink invite list"].JSONResultFields
	if complete, ok := listFields["complete"]; !ok || complete.Type != "boolean" || !strings.Contains(complete.Description, "partial") {
		t.Fatalf("invite list manifest complete field = %+v present=%t, want explicit partial-result signal", complete, ok)
	}
	if _, ok := listFields["device_targets"]; !ok {
		t.Fatal("invite list manifest missing per-target device check results")
	}
	removeFields := commands["tslink people remove"].JSONResultFields
	if removeFields["complete"].Type != "boolean" || removeFields["cleanup"].Type != "array" {
		t.Fatal("people remove manifest must expose remote completion and cleanup evidence", removeFields)
	}
	for _, name := range []string{"reconcile-invite", "replace-invite"} {
		if !containsString(flag("tslink people update", name).Requires, "--invite") {
			t.Fatal("people update recovery flag requires --invite", name)
		}
	}
	resendFields := commands["tslink invite resend"].JSONResultFields
	if _, ok := resendFields["invite_url"]; ok {
		t.Fatal("invite resend manifest still advertises invite_url")
	}
}

func TestCompactManifestStaysBelowAgentTokenBudget(t *testing.T) {
	data, err := json.Marshal(CompactManifest())
	if err != nil {
		t.Fatalf("json.Marshal: %v", err)
	}
	// The explicit invite namespace flag and stable invite error codes, plus the
	// credential-lifecycle surface (login --expires-in/--expires-at/--retire-other/
	// --open-keys-page/--open-oauth-page, logout --kind, and the api_token_unauthorized,
	// api_forbidden, invite_api_unauthorized, login_verify_failed error codes) add
	// machine contract surface; keep a fixed ceiling while accounting for it.
	// The architecture round then registered the per-service and daemon codes
	// it introduced (credential_control_url_mismatch, funnel_expiry_required,
	// path_exposes_config_dir, legacy_config_dir_present, config_load_failed)
	// and dropped feature_unavailable, which took the compact form past 2500
	// bytes (2550 measured alone); the code->exit map is what an agent looks
	// up, so it stays and the ceiling moves to 3000. Batch B3 then derived the
	// map from the one error-code table, which added the ten codes the old map
	// missed and mcp_elevated_invite_refused: 2860 bytes at the end of B3.
	// People sharing, health, recipes and request limits expand the compact agent surface.
	// Keep a bounded budget for the combined command tree.
	// QR flags, the request commands and seven request codes bring the complete
	// compact contract past 5000 bytes with scopes, guests and audit.
	// Preserve every merged command and error code within a 5500-byte budget.
	if len(data) >= 5500 {
		t.Fatalf("compact manifest = %d bytes, want < 5500", len(data))
	}
	compact := CompactManifest()
	if compact.ErrorCodes[registry.CodeURLNotReady] != 5 {
		t.Fatalf("url_not_ready exit = %d, want 5", compact.ErrorCodes[registry.CodeURLNotReady])
	}
	if _, ok := compact.Commands["url"]; !ok {
		t.Fatal("compact manifest missing url command")
	}
	for _, unavailable := range []string{"domain", "acme-email"} {
		if containsString(compact.Commands["add"], unavailable) {
			t.Fatalf("compact manifest advertises unavailable add flag %q", unavailable)
		}
	}
	for _, command := range []string{"invite user", "invite device", "invite list", "invite revoke", "invite resend"} {
		if _, ok := compact.Commands[command]; !ok {
			t.Fatalf("compact manifest missing %s command", command)
		}
	}
}

func TestCompactManifestContractIsLivePlatformSpecificAndQualifierFree(t *testing.T) {
	full := Manifest()
	compact := CompactManifest()
	if compact.SchemaVersion != full.SchemaVersion || compact.Platform != full.Platform {
		t.Fatalf("compact provenance = schema %d platform %+v, want full schema %d platform %+v", compact.SchemaVersion, compact.Platform, full.SchemaVersion, full.Platform)
	}
	commandsType := reflect.TypeOf(compact.Commands)
	if commandsType != reflect.TypeOf(map[string][]string{}) || commandsType.Elem().Elem().Kind() != reflect.String {
		t.Fatalf("compact Commands type = %v, want map[string][]string with no qualifier-bearing entry shape", commandsType)
	}
	if _, ok := reflect.TypeOf(compact).FieldByName("SupportedPlatforms"); ok {
		t.Fatal("compact manifest unexpectedly acquired full-manifest supported-platform qualifier context")
	}
	data, err := json.Marshal(compact)
	if err != nil {
		t.Fatal(err)
	}
	var object map[string]json.RawMessage
	if err := json.Unmarshal(data, &object); err != nil {
		t.Fatal(err)
	}
	if _, ok := object["supported_platforms"]; ok {
		t.Fatal("compact wire unexpectedly contains supported_platforms")
	}
	if _, ok := object["platform"]; !ok {
		t.Fatal("compact wire is missing its live binary platform")
	}
}

func TestManifestPlatformDescriptionsAreStable(t *testing.T) {
	if got := platformNeutralCommandShort("tslink install", "Install as macOS LaunchAgent"); strings.Contains(got, "macOS") {
		t.Fatalf("install short = %q, want platform-neutral description", got)
	}
	if got := platformNeutralCommandShort("tslink uninstall", "Remove systemd unit"); strings.Contains(got, "systemd") {
		t.Fatalf("uninstall short = %q, want platform-neutral description", got)
	}
}

func containsString(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}

func contains(value, want string) bool {
	for i := 0; i+len(want) <= len(value); i++ {
		if value[i:i+len(want)] == want {
			return true
		}
	}
	return false
}
