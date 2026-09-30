package cmd

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"path/filepath"
	"testing"
	"time"

	tsruntime "github.com/monody0007/tslink/internal/runtime"
)

// TestEnsureDaemonNotesTheInstallItPerformed: the install announcement goes
// to stderr only, so ensureDaemon also notes what it installed -- the
// supervisor, its definition and the undo command -- for a caller that asked,
// and notes nothing when a verified daemon already ran or installation was
// skipped.
func TestEnsureDaemonNotesTheInstallItPerformed(t *testing.T) {
	dir := isolateBootstrap(t)
	installDaemonFn = func(context.Context, io.Writer) error {
		isRunningFn = func(string) bool { return true }
		detectSupervisionFn = func(string, bool, int) Supervision {
			return Supervision{Manager: supervisorName(), Installed: true, RestartOnExit: true, Autostart: true}
		}
		return tsruntime.Save(filepath.Join(dir, "runtime.json"), tsruntime.NewSnapshot(4242, time.Now(), "fixture", time.Now(), nil))
	}
	ctx, record := recordDaemonInstall(context.Background())
	var log bytes.Buffer
	if err := ensureDaemon(ctx, &log, false); err != nil {
		t.Fatalf("ensureDaemon: %v (%s)", err, &log)
	}
	path, _ := supervisorPath()
	if got := record.installed; got == nil || got.Manager != supervisorName() || got.Path != path || got.Undo != "tslink uninstall" {
		t.Fatalf("installed = %+v, want {%s %s tslink uninstall}", got, supervisorName(), path)
	}

	// Already running under verified supervision, and the opt-out: no install.
	for _, noInstall := range []bool{false, true} {
		ctx, record := recordDaemonInstall(context.Background())
		if err := ensureDaemon(ctx, io.Discard, noInstall); err != nil || record.installed != nil {
			t.Fatalf("noInstall=%v with a verified daemon: installed = %+v, err = %v; want nothing", noInstall, record.installed, err)
		}
	}
	isRunningFn = func(string) bool { return false }
	ctx, record = recordDaemonInstall(context.Background())
	if err := ensureDaemon(ctx, io.Discard, true); err != nil || record.installed != nil {
		t.Fatalf("opt-out without a daemon: installed = %+v, err = %v; want nothing", record.installed, err)
	}
}

// TestMCPResultsReportTheDaemonTheyInstalled is A3-9: share, add and
// template_apply install a persistent OS autostart unless told not to, the
// announcement went to stderr, which the model never sees, and no result
// field recorded it. Each tool result now carries daemon_installed when an
// install happened, and nothing otherwise.
func TestMCPResultsReportTheDaemonTheyInstalled(t *testing.T) {
	record := DaemonInstalled{Manager: "launchd", Path: "/Users/someone/Library/LaunchAgents/com.tslink.daemon.plist", Undo: "tslink uninstall"}
	calls := map[string]string{
		"share":          `{"target":"3000"}`,
		"add":            `{"name":"web","type":"proxy","target":"localhost:3000"}`,
		"template_apply": `{"name":"local-web"}`,
	}
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
		ensureDaemonFn = func(ctx context.Context, _ io.Writer, _ bool) error {
			running = true
			if install {
				noteDaemonInstall(ctx, record)
			}
			return nil
		}
		actions := defaultMCPActions(paths, io.Discard)
		for _, tool := range []string{"share", "add", "template_apply"} {
			running = false
			result, err := callMCPTool(context.Background(), actions, tool, json.RawMessage(calls[tool]))
			if err != nil || result.IsError {
				t.Fatalf("%s: result %+v, err %v", tool, result, err)
			}
			structured := mcpResultStructured(t, result)
			validateAgainstToolOutputSchema(t, tool, structured)
			got, present := structured["daemon_installed"]
			switch {
			case install && !present:
				t.Errorf("%s installed a daemon and its result does not say so: %v", tool, structured)
			case install:
				wire, _ := json.Marshal(got)
				if string(wire) != `{"manager":"launchd","path":"/Users/someone/Library/LaunchAgents/com.tslink.daemon.plist","undo":"tslink uninstall"}` {
					t.Errorf("%s daemon_installed = %s", tool, wire)
				}
			case present:
				t.Errorf("%s installed nothing and reports daemon_installed %v", tool, got)
			}
		}
	}
}
