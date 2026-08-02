//go:build !windows && !darwin && !linux

package daemon

import (
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"time"
)

// Less common Unix targets retain the portable ps bridge. Darwin and Linux use
// native kernel interfaces in their platform files.
func defaultProcessExecutable(pid int) (string, error) {
	out, err := exec.Command("ps", "-p", strconv.Itoa(pid), "-o", "comm=").Output()
	if err != nil {
		return "", err
	}
	identity := strings.TrimSpace(string(out))
	if identity == "" {
		return "", fmt.Errorf("empty process identity")
	}
	return identity, nil
}

func defaultProcessStartTime(pid int) (time.Time, error) {
	cmd := exec.Command("ps", "-p", strconv.Itoa(pid), "-o", "lstart=")
	cmd.Env = append(os.Environ(), "LC_ALL=C", "TZ=UTC0")
	out, err := cmd.Output()
	if err != nil {
		return time.Time{}, err
	}
	value := strings.Join(strings.Fields(string(out)), " ")
	if value == "" {
		return time.Time{}, fmt.Errorf("empty process start time")
	}
	started, err := time.ParseInLocation("Mon Jan 2 15:04:05 2006", value, time.UTC)
	if err != nil {
		return time.Time{}, fmt.Errorf("parse process start time %q: %w", value, err)
	}
	return started, nil
}

func defaultProcessArguments(pid int) ([]string, error) {
	out, err := exec.Command("ps", "-p", strconv.Itoa(pid), "-o", "command=").Output()
	if err != nil {
		return nil, err
	}
	command := strings.TrimSpace(string(out))
	if executablePath, executableErr := defaultProcessExecutable(pid); executableErr == nil && strings.HasPrefix(command, executablePath) {
		rest := strings.TrimSpace(strings.TrimPrefix(command, executablePath))
		return append([]string{executablePath}, strings.Fields(rest)...), nil
	}
	fields := strings.Fields(command)
	if len(fields) == 0 {
		return nil, fmt.Errorf("empty process command")
	}
	return fields, nil
}
