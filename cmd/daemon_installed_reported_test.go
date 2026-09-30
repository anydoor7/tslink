package cmd

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"path/filepath"
	"strings"
	"testing"
	"time"

	tsruntime "github.com/monody0007/tslink/internal/runtime"
)

// TestEnsureDaemonReportsTheInstallItPerformed: the install announcement goes
// to stderr only, so ensureDaemon also returns what it installed -- the
// supervisor, its definition and the undo command -- and nothing when a
// verified daemon already ran or installation was skipped.
func TestEnsureDaemonReportsTheInstallItPerformed(t *testing.T) {
	dir := isolateBootstrap(t)
	installDaemonFn = func(context.Context, io.Writer) error {
		isRunningFn = func(string) bool { return true }
		detectSupervisionFn = func(string, bool, int) Supervision {
			return Supervision{Manager: supervisorName(), Installed: true, RestartOnExit: true, Autostart: true}
		}
		return tsruntime.Save(filepath.Join(dir, "runtime.json"), tsruntime.NewSnapshot(4242, time.Now(), "fixture", time.Now(), nil))
	}
	var log bytes.Buffer
	installed, err := ensureDaemon(context.Background(), &log, false)
	if err != nil {
		t.Fatalf("ensureDaemon: %v (%s)", err, &log)
	}
	path, _ := supervisorPath()
	if installed == nil || installed.Manager != supervisorName() || installed.Path != path || installed.Undo != "tslink uninstall" {
		t.Fatalf("installed = %+v, want {%s %s tslink uninstall}", installed, supervisorName(), path)
	}

	// Already running under verified supervision, and the opt-out: no install.
	for _, noInstall := range []bool{false, true} {
		if again, err := ensureDaemon(context.Background(), io.Discard, noInstall); err != nil || again != nil {
			t.Fatalf("noInstall=%v with a verified daemon: installed = %+v, err = %v; want nothing", noInstall, again, err)
		}
	}
	isRunningFn = func(string) bool { return false }
	if skipped, err := ensureDaemon(context.Background(), io.Discard, true); err != nil || skipped != nil {
		t.Fatalf("opt-out without a daemon: installed = %+v, err = %v; want nothing", skipped, err)
	}
}

// TestMCPResultsReportTheDaemonTheyInstalled is A3-9: share, add and
// template_apply install a persistent OS autostart unless told not to, the
// announcement went to stderr, which the model never sees, and no result
// field recorded it. Each result now carries daemon_installed when an install
// happened, and nothing otherwise.
func TestMCPResultsReportTheDaemonTheyInstalled(t *testing.T) {
	record := &DaemonInstalled{Manager: "launchd", Path: "/Users/someone/Library/LaunchAgents/com.tslink.daemon.plist", Undo: "tslink uninstall"}
	for _, install := range []bool{true, false} {
		restoreShareSeams(t)
		paths := mcpSharePaths(t)
		oldEnsure := ensureDaemonFn
		t.Cleanup(func() { ensureDaemonFn = oldEnsure })
		running := false
		shareIsRunningFn = func(string) bool { return running }
		shareResolveEndpointOnceFn = func(_, _, _, name string) (serviceURLResolution, error) {
			return serviceURLResolution{Result: URLResult{Name: name, URL: "https://" + name + ".tail.ts.net"}}, nil
		}
		ensureDaemonFn = func(context.Context, io.Writer, bool) (*DaemonInstalled, error) {
			running = true
			if install {
				return record, nil
			}
			return nil, nil
		}
		actions := defaultMCPActions(paths, io.Discard)

		shared, err := actions.share(context.Background(), shareRequest{Target: "3000", Ephemeral: true})
		if err != nil {
			t.Fatal(err)
		}
		running = false
		added, err := actions.add(context.Background(), AddParams{Name: "web", Proxy: "localhost:3000"}, true)
		if err != nil {
			t.Fatal(err)
		}
		applied, err := actions.templateApply(context.Background(), "local-web", false)
		if err != nil {
			t.Fatal(err)
		}
		for tool, value := range map[string]any{"share": shared, "add": added, "template_apply": applied} {
			validateAgainstToolOutputSchema(t, tool, value)
			wire, _ := json.Marshal(value)
			var fields map[string]json.RawMessage
			if err := json.Unmarshal(wire, &fields); err != nil {
				t.Fatal(err)
			}
			got, present := fields["daemon_installed"]
			switch {
			case install && !present:
				t.Errorf("%s installed a daemon and its result does not say so: %s", tool, wire)
			case install && !strings.Contains(string(got), `"undo":"tslink uninstall"`):
				t.Errorf("%s daemon_installed = %s", tool, got)
			case !install && present:
				t.Errorf("%s installed nothing and reports daemon_installed %s", tool, got)
			}
		}
	}
}
