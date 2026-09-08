//go:build darwin

package cmd

import (
	"bytes"
	"context"
	"encoding/xml"
	"fmt"
	"io"
	"os"
	"strings"
)

func supervisorPath() (string, error)                       { return plistPath() }
func supervisorName() string                                { return "launchd" }
func startInstalledDaemon(context.Context, io.Writer) error { return nil }

// Only the dictionary values needed for ownership/autostart are decoded.
func launchdDefinition(data []byte) (map[string]any, error) {
	d := xml.NewDecoder(bytes.NewReader(data))
	for {
		t, err := d.Token()
		if err != nil {
			return nil, err
		}
		if start, ok := t.(xml.StartElement); ok && start.Name.Local == "dict" {
			return decodeLaunchdDict(d)
		}
	}
}

func decodeLaunchdDict(d *xml.Decoder) (map[string]any, error) {
	values := make(map[string]any)
	var key string
	for {
		t, err := d.Token()
		if err != nil {
			return nil, err
		}
		switch t := t.(type) {
		case xml.EndElement:
			if t.Name.Local == "dict" {
				return values, nil
			}
		case xml.StartElement:
			switch t.Name.Local {
			case "key":
				if err := d.DecodeElement(&key, &t); err != nil {
					return nil, err
				}
			case "string":
				var v string
				if err := d.DecodeElement(&v, &t); err != nil {
					return nil, err
				}
				values[key] = v
			case "true", "false":
				values[key] = t.Name.Local == "true"
				if err := d.Skip(); err != nil {
					return nil, err
				}
			case "dict":
				v, err := decodeLaunchdDict(d)
				if err != nil {
					return nil, err
				}
				values[key] = v
			default:
				if err := d.Skip(); err != nil {
					return nil, err
				}
			}
		}
	}
}

func supervisorConfigMatches(data []byte, dir string) bool {
	values, err := launchdDefinition(data)
	if err != nil {
		return false
	}
	env, ok := values["EnvironmentVariables"].(map[string]any)
	return ok && env["TSLINK_CONFIG_DIR"] == dir
}

func launchdObservations() ([]byte, string, error) {
	for _, domain := range []string{launchctlDomain(), launchctlUserDomain()} {
		target := launchctlServiceTargetForDomain(domain)
		data, err := managerOutputFn("launchctl", "print", target)
		if err == nil {
			return data, target, nil
		}
		if !launchctlTargetNotFound(data, err) {
			return nil, "", fmt.Errorf("cannot inspect launchd target %s", target)
		}
	}
	return nil, "", nil
}

func checkUnregisteredSupervisor() error {
	data, _, err := launchdObservations()
	if err != nil {
		return err
	}
	if data != nil {
		return fmt.Errorf("launchd has a loaded TSLink job without this plist; automatic replacement refused")
	}
	return nil
}

func checkSupervisorProcessScope() error {
	for _, domain := range []string{launchctlDomain(), launchctlUserDomain()} {
		data, err := managerOutputFn("launchctl", "print", launchctlServiceTargetForDomain(domain))
		if err != nil && !launchctlTargetNotFound(data, err) {
			return fmt.Errorf("cannot inspect launchd domain %s", domain)
		}
		_, pid := parseLaunchAgentState(data)
		if pid > 0 {
			return fmt.Errorf("launchd still has a process (pid %d) but the selected config has no verified daemon; automatic replacement refused", pid)
		}
	}
	return nil
}

func detectSupervision(_ string, running bool, pid int) Supervision {
	s := unmanagedSupervision(running, "No launchd ownership/autostart could be verified. Run: tslink install")
	path, err := plistPath()
	if err != nil {
		return s
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return s
	}
	values, err := launchdDefinition(data)
	if err != nil {
		return s
	}
	dir, err := absoluteConfigDir()
	if err != nil {
		return s
	}
	// For an already running legacy install the manager PID is ownership
	// proof. For a stopped job require the explicit config binding.
	matched := false
	matchedDomain := ""
	for _, domain := range []string{launchctlDomain(), launchctlUserDomain()} {
		output, err := managerOutputFn("launchctl", "print", launchctlServiceTargetForDomain(domain))
		if err != nil {
			continue
		}
		state, managedPID := parseLaunchAgentState(output)
		if running && pid > 0 && state == "running" && managedPID == pid {
			matched = true
			matchedDomain = domain
		}
		if !running && managedPID == 0 && supervisorConfigMatches(data, dir) {
			matched = true
			matchedDomain = domain
		}
	}
	if !matched {
		return s
	}
	autostart := values["RunAtLoad"] == true && values["Disabled"] != true && launchdAutostartEnabled(matchedDomain)
	// A LaunchAgent is a per-user job: launchd loads it when this user's
	// session starts, so it returns at login rather than at boot. Saying so
	// keeps the field comparable with the systemd side, where the same
	// distinction is the difference between surviving a reboot and not.
	scope := autostartScopeLogin
	detail := "launchd job verified; it starts when this user logs in, so it returns at login rather than while the machine boots to the login window. Undo: tslink uninstall"
	if !autostart {
		scope = ""
		detail = "launchd owns the job, but autostart is disabled or unverified. Inspect: launchctl print-disabled " + matchedDomain
	}
	return Supervision{Manager: "launchd", Installed: true, Path: path,
		Autostart: autostart, AutostartScope: scope, RestartOnExit: values["KeepAlive"] == true, Detail: detail}
}

func launchdAutostartEnabled(domain string) bool {
	data, err := managerOutputFn("launchctl", "print-disabled", domain)
	if err != nil || !strings.Contains(string(data), "disabled services = {") {
		return false
	}
	for _, line := range strings.Split(string(data), "\n") {
		key, value, ok := strings.Cut(line, "=>")
		if ok && strings.Trim(strings.TrimSpace(key), "\"") == plistLabel {
			value = strings.TrimSpace(value)
			return value == "false" || value == "enabled"
		}
	}
	return true
}
