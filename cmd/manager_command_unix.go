//go:build darwin || linux

package cmd

import (
	"context"
	"errors"
	"time"
)

const managerMutationTimeout = 120 * time.Second

func managerCommandTimeout(args ...string) time.Duration {
	for _, arg := range args {
		if arg == "show" || arg == "print" {
			return managerQueryTimeout
		}
	}
	return managerMutationTimeout
}

// retryManagerQueryOnTimeout repeats a read-only manager query once when the
// first attempt ran out its budget. One slow answer from a busy but healthy
// manager says nothing about the job. A second timeout is returned as is, and
// callers treat it as unknown, never as "not owned" or "not running".
func retryManagerQueryOnTimeout(query func() ([]byte, error)) ([]byte, error) {
	output, err := query()
	if errors.Is(err, context.DeadlineExceeded) {
		output, err = query()
	}
	return output, err
}
