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

	"github.com/monody0007/tslink/internal/inspect"
	"github.com/monody0007/tslink/internal/output"
	"github.com/monody0007/tslink/internal/registry"
	tsruntime "github.com/monody0007/tslink/internal/runtime"
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
	if runtime.GOOS == "darwin" {
		want := []string{"tslink install --force", "tslink uninstall --force"}
		if !reflect.DeepEqual(markedFlags, want) {
			t.Fatalf("marked flags = %v, want exactly %v", markedFlags, want)
		}
		for _, key := range want {
			parts := strings.Split(key, " --")
			var got FlagInfo
			for _, flag := range commands[parts[0]].Flags {
				if flag.Name == parts[1] {
					got = flag
				}
			}
			if !reflect.DeepEqual(got.Platforms, []string{"darwin"}) {
				t.Fatalf("%s platforms = %v, want [darwin]", key, got.Platforms)
			}
		}
	} else if len(markedFlags) != 0 {
		t.Fatalf("%s manifest unexpectedly carries platform-marked live flags: %v", runtime.GOOS, markedFlags)
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

	if m.Toolchain.GoReleaserVersion != "v2.17.0" {
		t.Fatalf("GoReleaserVersion = %q, want v2.17.0", m.Toolchain.GoReleaserVersion)
	}
	if m.Toolchain.HomebrewArtifact != "cask" {
		t.Fatalf("HomebrewArtifact = %q, want cask", m.Toolchain.HomebrewArtifact)
	}
	if len(m.RegistrySchema.ServiceTypes) == 0 || len(m.RegistrySchema.UnavailableFeatures) == 0 {
		t.Fatalf("registry schema is incomplete: %#v", m.RegistrySchema)
	}
	for _, feature := range m.RegistrySchema.UnavailableFeatures {
		if !contains(feature, "feature_unavailable") {
			t.Fatalf("unavailable feature %q does not name feature_unavailable", feature)
		}
	}
	if !containsString(m.APIActions, apiActionDoctor) ||
		!containsString(m.APIActions, apiActionAccessExplain) ||
		!containsString(m.APIActions, apiActionTemplateApply) ||
		!containsString(m.APIActions, apiActionManifest) {
		t.Fatalf("api actions missing shipped actions: %v", m.APIActions)
	}
	if len(m.APIActions) != 10 {
		t.Fatalf("api actions = %v, want the 9 existing actions plus manifest", m.APIActions)
	}
	if m.Release.PublicReleaseAvailable || m.Release.PrebuiltAvailable || m.Release.HomebrewTapAvailable {
		t.Fatalf("release availability must stay false before first public readback: %#v", m.Release)
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

	production := map[string]map[string]struct{}{
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
		registry.CodeUnknownConfigKey:           fmt.Errorf("unknown config key: %q", "bad"),
		registry.CodeURLNotReady:                registry.URLNotReadyError("pending"),
		registry.CodeLaunchctlDomainUnavailable: registry.CodedError{Code: registry.CodeLaunchctlDomainUnavailable, Message: "unavailable"},
		registry.CodeFeatureUnavailable:         registry.FeatureUnavailableError("unavailable"),
		registry.CodeFunnelPublicAckRequired:    registry.FunnelPublicAckError(),
		registry.CodeFunnelAllowConflict:        registry.FunnelAllowedUsersError(),
		registry.CodeFunnelControlURLConflict:   registry.FunnelControlURLError(),
		registry.CodeFunnelTypeConflict:         registry.FunnelTypeConflictError(registry.TypeTCP),
		registry.CodeFunnelCapabilityMissing:    registry.FunnelCapabilityMissingError("svc", errors.New("missing")),
		registry.CodeFunnelListenFailed:         registry.FunnelListenFailedError("svc", errors.New("listen")),
		registry.CodeServiceStartTimeout:        registry.ServiceStartTimeoutError("svc", time.Second),
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
	assertFlag("tslink tags delete-remote", "force")
	assertFlag("tslink tags delete-remote", "manage-acl")
	assertFlag("tslink status", "json")
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
	for _, name := range []string{"domain", "acme-email"} {
		reserved := flag("tslink add", name)
		if !strings.HasPrefix(reserved.Usage, "[UNAVAILABLE]") {
			t.Fatalf("add --%s usage = %q, want [UNAVAILABLE] prefix", name, reserved.Usage)
		}
	}
}

func TestCompactManifestStaysBelowAgentTokenBudget(t *testing.T) {
	data, err := json.Marshal(CompactManifest())
	if err != nil {
		t.Fatalf("json.Marshal: %v", err)
	}
	if len(data) >= 2000 {
		t.Fatalf("compact manifest = %d bytes, want < 2000", len(data))
	}
	compact := CompactManifest()
	if compact.ErrorCodes[registry.CodeURLNotReady] != 5 {
		t.Fatalf("url_not_ready exit = %d, want 5", compact.ErrorCodes[registry.CodeURLNotReady])
	}
	if _, ok := compact.Commands["url"]; !ok {
		t.Fatal("compact manifest missing url command")
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
