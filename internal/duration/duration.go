// Package duration parses Go duration syntax plus d for days of 24 hours.
package duration

import (
	"fmt"
	"strconv"
	"strings"
	"time"
)

// Parse reads Go duration syntax extended with d for days, ignoring surrounding space.
func Parse(value string) (time.Duration, error) {
	text := strings.TrimSpace(value)
	if !strings.Contains(text, "d") {
		return time.ParseDuration(text)
	}
	// Rewrite each day component as hours and let Go parse the rest, so the
	// grammar stays Go's with one more unit.
	var rewritten strings.Builder
	rest := text
	if rest != "" && (rest[0] == '-' || rest[0] == '+') {
		rewritten.WriteByte(rest[0])
		rest = rest[1:]
	}
	for rest != "" {
		number := len(rest) - len(strings.TrimLeft(rest, "0123456789."))
		unit := number + len(rest[number:]) - len(strings.TrimLeft(rest[number:], "abcdefghijklmnopqrstuvwxyzµμ"))
		if number == 0 || unit == number {
			return 0, fmt.Errorf("time: invalid duration %q", value)
		}
		if rest[number:unit] == "d" {
			days, err := strconv.ParseFloat(rest[:number], 64)
			if err != nil {
				return 0, fmt.Errorf("time: invalid duration %q", value)
			}
			rewritten.WriteString(strconv.FormatFloat(days*24, 'f', -1, 64))
			rewritten.WriteString("h")
		} else {
			rewritten.WriteString(rest[:unit])
		}
		rest = rest[unit:]
	}
	parsed, err := time.ParseDuration(rewritten.String())
	if err != nil {
		return 0, fmt.Errorf("time: invalid duration %q", value)
	}
	return parsed, nil
}
