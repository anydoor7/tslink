//go:build linux

package cmd

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"

	"github.com/anydoor7/tslink/internal/mcpscope"
)

func supervisorPath() (string, error)                       { return systemdServicePath() }
func supervisorName() string                                { return "systemd" }
func startInstalledDaemon(context.Context, io.Writer) error { return nil }

func systemdConfigEnvironment(dir string) string {
	// Environment= does not perform shell or dollar expansion. Percent is
	// still a systemd specifier, so it must be doubled.
	r := strings.NewReplacer("\\", "\\\\", "\"", "\\\"", "%", "%%", "\n", "\\n", "\r", "\\r", "\t", "\\t")
	return "Environment=\"TSLINK_CONFIG_DIR=" + r.Replace(dir) + "\""
}

func supervisorConfigMatches(data []byte, dir string) bool {
	return bytes.Contains(data, []byte("\n"+systemdConfigEnvironment(dir)+"\n"))
}

func systemdObservation(ctx context.Context) (map[string]string, error) {
	data, err := managerOutputChecked(ctx, "systemctl", "--user", "show", systemdServiceName,
		"--property=LoadState,ActiveState,SubState,MainPID,UnitFileState,Restart,FragmentPath", "--no-pager")
	if err != nil {
		if len(data) > 4096 {
			data = append(append([]byte(nil), data[:4096]...), []byte(" [truncated]")...)
		}
		detail := strings.TrimSpace(strings.ToValidUTF8(string(data), "?"))
		lower := strings.ToLower(detail)
		for _, clue := range []string{"failed to connect to bus", "failed to connect to user scope bus", "xdg_runtime_dir", "dbus_session_bus_address", "no medium found"} {
			if strings.Contains(lower, clue) {
				return nil, fmt.Errorf("%s: %s (%w); automatic installation requires a login session with a working systemd user manager and XDG_RUNTIME_DIR. In CI or without a user bus, register with --no-daemon-install and run tslink serve manually", systemdUserManagerUnavailableMessage, detail, err)
			}
		}
		return nil, fmt.Errorf("cannot inspect systemd user service: %w; %s", err, detail)
	}
	return parseSystemdProperties(data), nil
}

func checkUnregisteredSupervisor(ctx context.Context) error {
	p, err := systemdObservation(ctx)
	if err != nil {
		return err
	}
	if p["LoadState"] != "not-found" {
		return fmt.Errorf("systemd has a TSLink unit outside this install path; automatic replacement refused")
	}
	return nil
}

func checkSupervisorProcessScope(ctx context.Context) error {
	p, err := systemdObservation(ctx)
	if err != nil {
		return err
	}
	if pid, _ := strconv.Atoi(p["MainPID"]); pid > 0 {
		return fmt.Errorf("systemd still has a process (pid %d) but the selected config has no verified daemon; automatic replacement refused", pid)
	}
	return nil
}

// The recovery command differs by cause, so the causes are kept apart. The one
// that used to be folded in with the rest is an installed unit the user
// manager cannot be asked about: reinstalling rewrites the same file and
// changes neither lingering nor the missing session, so sending the operator
// to 'tslink install' there is sending them at the wrong thing.
func detectSupervisionContext(ctx context.Context, _ string, running bool, pid int) Supervision {
	path, err := systemdServicePath()
	if err != nil {
		return unmanagedSupervision(running, "No systemd ownership/autostart could be verified: the unit path could not be resolved ("+err.Error()+"). Run: tslink install")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			if _, managerErr := systemdObservation(ctx); managerErr != nil {
				return unmanagedSupervision(running, "No systemd user unit is installed at "+path+". "+managerErr.Error()+". After establishing that login session, Run: tslink install")
			}
			return unmanagedSupervision(running, "No systemd user unit is installed at "+path+". Run: tslink install")
		}
		return unmanagedSupervision(running, "The systemd user unit at "+path+" exists but could not be read ("+err.Error()+"). Inspect it before running: tslink install")
	}
	observed, err := systemdObservation(ctx)
	if err != nil {
		return unmanagedSupervision(running, "A systemd user unit is installed at "+path+", but the systemd user manager could not be queried ("+err.Error()+"). The unit file is already in place, so this is usually a missing login session or disabled lingering rather than a missing install, and reinstalling changes neither. For a host with no interactive login run: loginctl enable-linger \"$USER\". Then confirm with: systemctl --user status "+systemdServiceName)
	}
	if observed["LoadState"] != "loaded" || observed["FragmentPath"] != path {
		return unmanagedSupervision(running, fmt.Sprintf("A systemd user unit is installed at %s, but systemd has not loaded it from that path (LoadState=%q, FragmentPath=%q). Run: systemctl --user daemon-reload. If it stays unloaded, run: tslink install", path, observed["LoadState"], observed["FragmentPath"]))
	}
	mainPID, _ := strconv.Atoi(observed["MainPID"])
	if running {
		if pid <= 0 || mainPID != pid || observed["ActiveState"] != "active" || observed["SubState"] != "running" {
			return unmanagedSupervision(running, fmt.Sprintf("A TSLink daemon is running (pid %d), but the systemd unit at %s does not own it (MainPID=%q, ActiveState=%q, SubState=%q). Stop the unsupervised daemon with 'tslink stop', then run: tslink install", pid, path, observed["MainPID"], observed["ActiveState"], observed["SubState"]))
		}
	} else {
		dir, dirErr := absoluteConfigDir()
		switch {
		case dirErr != nil:
			return unmanagedSupervision(running, "A systemd user unit is installed at "+path+", but this config directory could not be resolved ("+dirErr.Error()+"), so its binding could not be checked.")
		case mainPID > 0:
			return unmanagedSupervision(running, fmt.Sprintf("The systemd unit at %s reports a running process (MainPID=%d) while no verified TSLink daemon was found for config %s. Inspect it with 'systemctl --user status %s' before any install or restart", path, mainPID, dir, systemdServiceName))
		case !supervisorConfigMatches(data, dir):
			return unmanagedSupervision(running, fmt.Sprintf("The systemd unit at %s is installed but bound to a different config directory than %s, so it would not supervise this config. Run: tslink install", path, dir))
		}
	}
	autostart := observed["UnitFileState"] == "enabled"
	scope, scopeDetail := linuxAutostartScope(ctx, autostart)
	return Supervision{Manager: "systemd", Installed: true, Path: path,
		Autostart: autostart, AutostartScope: scope, RestartOnExit: observed["Restart"] == "always" || observed["Restart"] == "on-failure",
		Detail: "systemd user unit verified; " + scopeDetail + " Undo: tslink uninstall"}
}

// linuxAutostartScope resolves boot versus login for a systemd *user* unit.
// An enabled unit is started by the per-user manager, and without lingering
// that manager exists only while the user has a session, so on a host nobody
// logs into the unit does not come back after a reboot. Enabling lingering
// affects every service this user owns, which makes it the user's decision;
// this reports the state and the exact command instead of performing it.
func linuxAutostartScope(ctx context.Context, autostart bool) (string, string) {
	if !autostart {
		return "", "the unit file is not enabled, so systemd does not start it on its own. Enable it with: tslink install."
	}
	switch linuxLingerState(ctx) {
	case lingerEnabled:
		return autostartScopeBoot, "systemd lingering is enabled for this user, so it starts at boot and survives logout."
	case lingerDisabled:
		return autostartScopeLogin, "it starts when this user logs in. Lingering is disabled, so it does NOT start at boot while nobody is logged in; for that run: loginctl enable-linger \"$USER\"."
	default:
		return autostartScopeUnknown, "it starts when this user logs in. Whether it also starts at boot could not be determined because systemd lingering could not be read; check with: loginctl show-user \"$USER\" --property=Linger, and enable boot start with: loginctl enable-linger \"$USER\"."
	}
}

type lingerState int

const (
	lingerUnknown lingerState = iota
	lingerEnabled
	lingerDisabled
)

// linuxLingerState reads the same property, through the same seam, as the
// install-time warning, so the two never disagree about what loginctl said.
func linuxLingerState(ctx context.Context) lingerState {
	user := linuxUserNameFn()
	if user == "" {
		return lingerUnknown
	}
	if err := mcpscope.CheckEffect(ctx); err != nil {
		return lingerUnknown
	}
	output, err := loginctlCombinedOutputFn(ctx, "show-user", user, "--property=Linger", "--value")
	if err != nil {
		return lingerUnknown
	}
	switch strings.TrimSpace(string(output)) {
	case "yes":
		return lingerEnabled
	case "no", "":
		return lingerDisabled
	default:
		return lingerUnknown
	}
}

func detectSupervision(ctx context.Context, pidPath string, running bool, pid int) Supervision {
	return detectSupervisionContext(ctx, pidPath, running, pid)
}
