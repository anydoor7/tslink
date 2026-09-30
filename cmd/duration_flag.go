package cmd

import (
	"time"

	"github.com/monody0007/tslink/internal/duration"
)

// durationFlag is a --wait flag value that reads TSLink's one duration grammar
// (internal/duration: Go syntax plus d for days), so the CLI accepts what the
// MCP url tool accepts. Its type is still "duration" and it prints in Go's
// form, so cobra's GetDuration, the help and the manifest see it as before.
type durationFlag time.Duration

func newDurationFlag(value time.Duration) *durationFlag {
	flag := durationFlag(value)
	return &flag
}

func (d *durationFlag) Set(value string) error {
	parsed, err := duration.Parse(value)
	if err != nil {
		return err
	}
	*d = durationFlag(parsed)
	return nil
}

func (d *durationFlag) Type() string { return "duration" }

func (d *durationFlag) String() string { return time.Duration(*d).String() }
