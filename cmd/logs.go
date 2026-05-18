package cmd

import (
	"bufio"
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

// matchLevel checks if a log line contains the given level or higher.
// Supports slog text format (level=WARN) and JSON format ("level":"WARN").
func matchLevel(line, level string) bool {
	level = strings.ToUpper(level)

	levels := map[string]int{
		"DEBUG": 0,
		"INFO":  1,
		"WARN":  2,
		"ERROR": 3,
	}

	threshold, ok := levels[level]
	if !ok {
		return true // unknown level, show everything
	}

	// Check for slog text format: level=WARN or level=ERROR etc.
	// Check for JSON format: "level":"WARN"
	upper := strings.ToUpper(line)
	for lvl, val := range levels {
		if val < threshold {
			continue
		}
		// slog text: level=INFO or level=WARN
		if strings.Contains(upper, "LEVEL="+lvl) {
			return true
		}
		// JSON: "level":"INFO"
		if strings.Contains(upper, `"LEVEL":"`+lvl+`"`) {
			return true
		}
	}

	return false
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
				return fmt.Errorf("invalid --source: %q (must be \"out\" or \"err\")", source)
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
