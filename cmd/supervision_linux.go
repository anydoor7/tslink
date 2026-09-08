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

func systemdObservation() (map[string]string, error) {
	data, err := managerOutputFn("systemctl", "--user", "show", systemdServiceName,
		"--property=LoadState,ActiveState,SubState,MainPID,UnitFileState,Restart,FragmentPath", "--no-pager")
	if err != nil {
		return nil, fmt.Errorf("cannot inspect systemd user service: %w", err)
	}
	return parseSystemdProperties(data), nil
}

func checkUnregisteredSupervisor() error {
	p, err := systemdObservation()
	if err != nil {
		return err
	}
	if p["LoadState"] != "not-found" {
		return fmt.Errorf("systemd has a TSLink unit outside this install path; automatic replacement refused")
	}
	return nil
}

func checkSupervisorProcessScope() error {
	p, err := systemdObservation()
	if err != nil {
		return err
	}
	if pid, _ := strconv.Atoi(p["MainPID"]); pid > 0 {
		return fmt.Errorf("systemd still has a process (pid %d) but the selected config has no verified daemon; automatic replacement refused", pid)
	}
	return nil
}

func detectSupervision(_ string, running bool, pid int) Supervision {
	s := unmanagedSupervision(running, "No systemd ownership/autostart could be verified. Run: tslink install")
	path, err := systemdServicePath()
	if err != nil {
		return s
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return s
	}
	p, err := systemdObservation()
	if err != nil || p["LoadState"] != "loaded" || p["FragmentPath"] != path {
		return s
	}
	mainPID, _ := strconv.Atoi(p["MainPID"])
	if running {
		if pid <= 0 || mainPID != pid || p["ActiveState"] != "active" || p["SubState"] != "running" {
			return s
		}
	} else {
		dir, err := absoluteConfigDir()
		if err != nil || mainPID > 0 || !supervisorConfigMatches(data, dir) {
			return s
		}
	}
	return Supervision{Manager: "systemd", Installed: true, Path: path,
		Autostart: p["UnitFileState"] == "enabled", RestartOnExit: p["Restart"] == "always" || p["Restart"] == "on-failure",
		Detail: "systemd user unit verified; starts at user login. For boot before login and logout survival: loginctl enable-linger \"$USER\". Undo: tslink uninstall"}
}
