package testenv

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

// ServiceManagerShimLogEnv names the file the planted fake service managers
// append their argv to. The shim bakes in a default path, so an invocation
// that inherits nothing still leaves a record; a test that wants its own
// record sets this on the child it spawns and reads that file instead.
const ServiceManagerShimLogEnv = "TSLINK_TEST_SERVICE_MANAGER_SHIM_LOG"

// ServiceManagerShimExitCode is the status the planted fakes exit with. It is
// outside the range tslink itself uses (0-5) and outside the shell's 126/127,
// so a test can tell "the shim refused" apart from both a real failure and a
// missing binary.
const ServiceManagerShimExitCode = 97

// ServiceManagerShimStderrPrefix begins every line the planted fakes write to
// stderr. A child that prints this proves the resolution reached the shim
// rather than the real binary.
const ServiceManagerShimStderrPrefix = "tslink-test-service-manager-shim:"

// serviceManagerShimBinaries are the OS service managers the shim stands in
// for. It is the single source of truth for that list: the source inventory
// test derives its needles from this slice, so a manager added here cannot be
// planted without also becoming a scanned name.
var serviceManagerShimBinaries = []string{"launchctl", "systemctl", "loginctl"}

// serviceManagerShimReadOnlyVerbs is an allowlist, deliberately not a list of
// forbidden verbs. A blocklist of write verbs goes quiet the moment a manager
// grows a new one; an allowlist fails loudly on the unknown verb instead, which
// is the direction this whole mechanism exists to fail in.
var serviceManagerShimReadOnlyVerbs = map[string]bool{
	"print":          true,
	"print-disabled": true,
	"list":           true,
	"version":        true,
	"show":           true,
	"show-user":      true,
	"status":         true,
	"is-active":      true,
	"is-enabled":     true,
	"is-failed":      true,
	"cat":            true,
}

// ServiceManagerShimCall is one recorded invocation of a planted fake.
type ServiceManagerShimCall struct {
	Manager string
	Args    []string
}

// Verb is the first non-flag argument, which is what decides whether the call
// would have changed system state. An invocation with no such argument returns
// the empty string and is treated as unknown, i.e. not read-only.
func (c ServiceManagerShimCall) Verb() string {
	for _, arg := range c.Args {
		if strings.HasPrefix(arg, "-") {
			continue
		}
		return arg
	}
	return ""
}

// IsReadOnly reports whether the call could not have changed system state.
func (c ServiceManagerShimCall) IsReadOnly() bool {
	return serviceManagerShimReadOnlyVerbs[c.Verb()]
}

// String renders the call the way it was typed.
func (c ServiceManagerShimCall) String() string {
	if len(c.Args) == 0 {
		return c.Manager
	}
	return c.Manager + " " + strings.Join(c.Args, " ")
}

// ServiceManagerPathShim is a directory of fake service manager binaries plus
// the file they record into.
type ServiceManagerPathShim struct {
	// Dir is the directory put at the front of PATH. It is set even on a
	// platform where nothing was planted, because the log lives there too.
	Dir string
	// LogPath is the file the fakes append to when the child inherits no
	// override for ServiceManagerShimLogEnv.
	LogPath string
	// Planted names the fakes that actually exist in Dir. It is empty on
	// Windows, where the shim protects nothing; see PlantServiceManagerShims.
	//
	// It is a list rather than a bool because the teardown report has to say
	// which of "no child called a manager" and "no fake was there to call"
	// produced a count of zero. Those two read identically otherwise, and the
	// second is the one that means the package ran unprotected.
	Planted []string
}

// Calls parses this shim's log.
func (s *ServiceManagerPathShim) Calls() ([]ServiceManagerShimCall, error) {
	if s == nil || s.LogPath == "" {
		return nil, nil
	}
	return ReadServiceManagerShimCalls(s.LogPath)
}

// PlantServiceManagerShims writes one executable fake per service manager into
// dir, creates the log the fakes append to, and returns the log path together
// with the names it actually planted. The caller owns dir.
//
// The fakes are /bin/sh scripts rather than compiled binaries so that planting
// them costs no build and cannot itself fail for toolchain reasons. On Windows
// they are not planted at all: none of these binaries exists there, and a shell
// script would not be executable anyway. That is stated here rather than hidden
// behind a nil return, because "the shim protects nothing on Windows" is a fact
// about the mechanism and not a detail.
func PlantServiceManagerShims(dir string) (logPath string, planted []string, err error) {
	logPath = filepath.Join(dir, "service-manager-calls.log")
	// The default is substituted inside "${VAR:-...}", which keeps spaces but
	// would still let these characters change what the shell does.
	if strings.ContainsAny(logPath, "'\"\n$`{}\\") {
		return "", nil, fmt.Errorf("testenv: shim log path %q contains a character that cannot be embedded in the shim script", logPath)
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", nil, err
	}
	// Create the log up front so a reader can tell "no calls" from "the shim
	// was never planted": the first is an existing empty file, the second is
	// a missing one, and ReadServiceManagerShimCalls reports them differently.
	handle, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return "", nil, err
	}
	if err := handle.Close(); err != nil {
		return "", nil, err
	}
	if runtime.GOOS == "windows" {
		return logPath, nil, nil
	}
	for _, binary := range serviceManagerShimBinaries {
		script := serviceManagerShimScript(binary, logPath)
		if err := os.WriteFile(filepath.Join(dir, binary), []byte(script), 0o700); err != nil {
			return "", nil, err
		}
		planted = append(planted, binary)
	}
	return logPath, planted, nil
}

// serviceManagerShimScript builds one fake. It records each argument as its own
// tab-separated field so that the parent can assert on argv boundaries rather
// than on a re-joined string, refuses to run anything, and exits with a status
// no real invocation produces.
//
// The record is assembled in a variable and emitted by a single printf, not
// printed field by field into an open append. Several fakes run at once -- a
// full cmd run on 2026-09-16 recorded 73 of these calls from concurrently
// spawned children -- and a `{ printf; printf; } >>log` block is one write(2) per
// printf, so the fields of two calls interleave. That was not theoretical: the
// first full-suite run under this shim produced the line
// `launchctl printlaunchctl gui/501/com.tslink.daemon`, which the verb
// classifier then read as a state change. One append per line is what makes
// the log parseable; it is also what keeps the classifier from being fed
// arbitrary text.
func serviceManagerShimScript(binary, defaultLog string) string {
	var script strings.Builder
	script.WriteString("#!/bin/sh\n")
	script.WriteString("# Planted by tslink's testenv. Executing the real " + binary + " from a test\n")
	script.WriteString("# child process is what took the production daemon down on 2026-09-16.\n")
	// The default is unquoted inside ${...:-...}; the enclosing double quotes
	// are what preserve spaces. Adding single quotes here would make them
	// literal characters in the path.
	script.WriteString("__tslink_log=\"${" + ServiceManagerShimLogEnv + ":-" + defaultLog + "}\"\n")
	script.WriteString("__tslink_line=$(printf '%s' '" + binary + "'; for __tslink_arg in \"$@\"; do printf '\\t%s' \"$__tslink_arg\"; done)\n")
	script.WriteString("printf '%s\\n' \"$__tslink_line\" >>\"$__tslink_log\" 2>/dev/null\n")
	script.WriteString("printf '" + ServiceManagerShimStderrPrefix + " refused to execute the real %s (argv:%s)\\n' '" + binary + "' \" $*\" >&2\n")
	script.WriteString(fmt.Sprintf("exit %d\n", ServiceManagerShimExitCode))
	return script.String()
}

// ReadServiceManagerShimCalls parses a shim log into calls.
func ReadServiceManagerShimCalls(logPath string) ([]ServiceManagerShimCall, error) {
	handle, err := os.Open(logPath)
	if err != nil {
		return nil, err
	}
	defer handle.Close()

	var calls []ServiceManagerShimCall
	scanner := bufio.NewScanner(handle)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for scanner.Scan() {
		line := scanner.Text()
		if line == "" {
			continue
		}
		fields := strings.Split(line, "\t")
		calls = append(calls, ServiceManagerShimCall{Manager: fields[0], Args: fields[1:]})
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	return calls, nil
}

// InstallServiceManagerPathShim plants the fakes in a fresh temporary
// directory and puts that directory at the front of this process's PATH.
//
// Why PATH and not a helper every spawn site has to call: production resolves
// its service manager by bare name (exec.Command("launchctl", ...)), and Go
// resolves a bare name against the *spawning* process's PATH. A child that
// inherits this process's environment therefore lands on the fake, and so does
// its own children. Inheriting is the default for exec.Cmd (a nil Env) and for
// every helper in this repo (append(os.Environ(), ...)), so the protected path
// is the one a spawn site gets by doing nothing. There is no call to forget.
//
// Bypassing it requires a deliberate act that is visible in source: building
// cmd.Env from scratch without os.Environ(), overwriting PATH afterwards, or
// naming the binary by absolute path. The source scan in
// service_manager_exit_inventory_test.go is what covers those.
//
// The returned restore function puts PATH back and removes the planted
// directory; read Calls() before calling it.
func InstallServiceManagerPathShim() (*ServiceManagerPathShim, func(), error) {
	dir, err := os.MkdirTemp("", "tslink-service-manager-shim-")
	if err != nil {
		return nil, nil, err
	}
	logPath, planted, err := PlantServiceManagerShims(dir)
	if err != nil {
		_ = os.RemoveAll(dir)
		return nil, nil, err
	}

	restorePath := environmentRestorer("PATH")
	restoreLog := environmentRestorer(ServiceManagerShimLogEnv)
	if err := os.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH")); err != nil {
		_ = os.RemoveAll(dir)
		return nil, nil, err
	}
	if err := os.Setenv(ServiceManagerShimLogEnv, logPath); err != nil {
		restorePath()
		_ = os.RemoveAll(dir)
		return nil, nil, err
	}

	shim := &ServiceManagerPathShim{Dir: dir, LogPath: logPath, Planted: planted}
	return shim, func() {
		restoreLog()
		restorePath()
		_ = os.RemoveAll(dir)
	}, nil
}

// environmentRestorer captures one variable's current presence and value.
func environmentRestorer(name string) func() {
	previous, present := os.LookupEnv(name)
	return func() {
		if present {
			_ = os.Setenv(name, previous)
			return
		}
		_ = os.Unsetenv(name)
	}
}
