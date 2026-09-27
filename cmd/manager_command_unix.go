//go:build darwin || linux

package cmd

import "time"

const managerMutationTimeout = 120 * time.Second

func managerCommandTimeout(args ...string) time.Duration {
	for _, arg := range args {
		if arg == "show" || arg == "print" {
			return managerQueryTimeout
		}
	}
	return managerMutationTimeout
}
