//go:build windows

package cmd

import (
	"bytes"
	"context"
	"io"
	"os"
	"os/exec"
)

func supervisorPath() (string, error)    { return windowsStartupScriptPath() }
func supervisorName() string             { return "windows-startup" }
func checkUnregisteredSupervisor() error { return nil }
func checkSupervisorProcessScope() error { return nil }

func windowsConfigEnvironment(dir string) string {
	return "shell.Environment(\"PROCESS\")(\"TSLINK_CONFIG_DIR\") = " + vbsStringLiteral(dir)
}

func supervisorConfigMatches(data []byte, dir string) bool {
	return bytes.Contains(data, []byte(windowsConfigEnvironment(dir)+"\r\n"))
}

func startInstalledDaemon(ctx context.Context, out io.Writer) error {
	path, err := windowsStartupScriptPath()
	if err != nil {
		return err
	}
	command := exec.CommandContext(ctx, "wscript.exe", path)
	command.Stdout, command.Stderr = out, out
	return command.Run()
}

func detectSupervision(_ string, running bool, _ int) Supervision {
	s := unmanagedSupervision(running, "Windows has no verified TSLink restart supervisor.")
	path, err := windowsStartupScriptPath()
	if err != nil {
		return s
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return s
	}
	dir, err := absoluteConfigDir()
	if err != nil || !supervisorConfigMatches(data, dir) {
		return s
	}
	return windowsStartupSupervision(path)
}
