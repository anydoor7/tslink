package cmd

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/monody0007/tslink/internal/config"
	"github.com/monody0007/tslink/internal/output"
	"github.com/spf13/cobra"
)

// LogsResult is the JSON payload for the logs command.
type LogsResult struct {
	Lines []string `json:"lines"`
	Count int      `json:"count"`
	File  string   `json:"file"`
}

// Testable function variable for logs command
var logsLogDirFn = config.LogDir

func validLogLevel(level string) bool {
	switch strings.ToUpper(strings.TrimSpace(level)) {
	case "", "DEBUG", "INFO", "WARN", "ERROR":
		return true
	default:
		return false
	}
}

// tailFile reads the last N lines from a file, optionally filtering by log level.
func tailFile(path string, last int, level string) ([]string, error) {
	f, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	defer f.Close()

	// Read all lines
	var allLines []string
	scanner := bufio.NewScanner(f)
	// Increase scanner buffer for long log lines
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for scanner.Scan() {
		line := scanner.Text()
		if level != "" && !matchLevel(line, level) {
			continue
		}
		allLines = append(allLines, line)
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}

	// Return last N lines
	if last > 0 && len(allLines) > last {
		allLines = allLines[len(allLines)-last:]
	}
	return allLines, nil
}

// matchLevel checks the structured producer level field against a threshold.
// It accepts slog text format (level=WARN) and slog JSON format
// ("level":"WARN") while ignoring user-controlled message fields.
func matchLevel(line, level string) bool {
	level = strings.ToUpper(level)

	threshold, ok := logLevelValue(level)
	if !ok {
		return false
	}

	actual, ok := extractLogLevel(line)
	if !ok {
		return false
	}
	actualValue, ok := logLevelValue(actual)
	return ok && actualValue >= threshold
}

func logLevelValue(level string) (int, bool) {
	switch strings.ToUpper(strings.TrimSpace(level)) {
	case "DEBUG":
		return 0, true
	case "INFO":
		return 1, true
	case "WARN":
		return 2, true
	case "ERROR":
		return 3, true
	default:
		return 0, false
	}
}

func extractLogLevel(line string) (string, bool) {
	trimmed := strings.TrimSpace(line)
	if strings.HasPrefix(trimmed, "{") {
		var fields map[string]any
		if err := json.Unmarshal([]byte(trimmed), &fields); err == nil {
			if level, ok := fields["level"].(string); ok {
				return strings.ToUpper(strings.TrimSpace(level)), true
			}
		}
	}
	return extractTextLogLevel(line)
}

func extractTextLogLevel(line string) (string, bool) {
	inQuote := false
	escaped := false
	tokenStart := 0
	for i := 0; i <= len(line); i++ {
		end := i == len(line)
		if !end {
			ch := line[i]
			if inQuote && escaped {
				escaped = false
			} else if inQuote && ch == '\\' {
				escaped = true
			} else if ch == '"' {
				inQuote = !inQuote
			}
			if !end && (inQuote || ch != ' ' && ch != '\t') {
				continue
			}
		}
		if tokenStart < i {
			token := line[tokenStart:i]
			if strings.HasPrefix(token, "level=") {
				level := strings.Trim(strings.TrimPrefix(token, "level="), `"`)
				return strings.ToUpper(level), true
			}
		}
		tokenStart = i + 1
	}
	return "", false
}

func init() {
	logsCmd := &cobra.Command{
		Use:   "logs",
		Short: "View TSLink daemon logs",
		Long: `View the TSLink daemon log output.

By default reads from the stderr log (tslink.err.log) which contains
structured application logs. Use --source out to read stdout logs.

Examples:
  tslink logs                         Show last 50 log lines
  tslink logs --last 100              Show last 100 lines
  tslink logs --level error           Show only error-level lines
  tslink logs --source out            Show stdout log instead
  tslink logs --last 20 --json        Output as JSON`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			last, _ := cmd.Flags().GetInt("last")
			level, _ := cmd.Flags().GetString("level")
			source, _ := cmd.Flags().GetString("source")
			if !validLogLevel(level) {
				return output.ErrUsage(fmt.Sprintf("invalid --level: %q (must be debug, info, warn, or error)", level))
			}

			logDir, err := logsLogDirFn()
			if err != nil {
				return err
			}

			var logFile string
			switch source {
			case "out":
				logFile = filepath.Join(logDir, "tslink.out.log")
			case "err":
				logFile = filepath.Join(logDir, "tslink.err.log")
			default:
				return output.ErrUsage(fmt.Sprintf("invalid --source: %q (must be \"out\" or \"err\")", source))
			}

			lines, err := tailFile(logFile, last, level)
			if err != nil {
				return err
			}

			if jsonOutput(cmd) {
				output.Success("logs", LogsResult{
					Lines: lines,
					Count: len(lines),
					File:  logFile,
				})
				return nil
			}

			if len(lines) == 0 {
				fmt.Fprintln(cmd.OutOrStdout(), "No log entries found.")
				return nil
			}
			out := cmd.OutOrStdout()
			for _, line := range lines {
				fmt.Fprintln(out, line)
			}
			return nil
		},
	}

	logsCmd.Flags().Int("last", 50, "Number of lines to show")
	logsCmd.Flags().String("level", "", "Filter by minimum log level (debug, info, warn, error)")
	logsCmd.Flags().String("source", "err", "Log source: out (stdout) or err (stderr)")
	rootCmd.AddCommand(logsCmd)
}
