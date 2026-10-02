//go:build windows

package cmd

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/anydoor7/tslink/internal/atomicfile"
	"github.com/anydoor7/tslink/internal/config"
	"github.com/anydoor7/tslink/internal/daemon"
	"github.com/spf13/cobra"
	"golang.org/x/sys/windows"
)

type builtinSupervisorRecord struct {
	Version   int                       `json:"version"`
	ConfigDir string                    `json:"config_dir"`
	Instance  daemon.SupervisorInstance `json:"instance"`
	daemon.SupervisorState
}

var builtinSupervisorAliveFn = daemon.SupervisorAlive
var readBuiltinSupervisorFn = readBuiltinSupervisor

func builtinSupervisorTerminal(state string) bool {
	return state == "stopped" || state == "circuit_open" || state == "failed"
}

func builtinSupervisorPaths(pidPath string) (string, string) {
	dir := filepath.Dir(pidPath)
	return filepath.Join(dir, "supervisor.json"), filepath.Join(dir, "supervisor.lock")
}

func readBuiltinSupervisor(pidPath string) (builtinSupervisorRecord, error) {
	path, _ := builtinSupervisorPaths(pidPath)
	if err := atomicfile.ConvergePrivateFile(path); err != nil {
		return builtinSupervisorRecord{}, err
	}
	f, err := openBuiltinSupervisorState(path)
	if err != nil {
		return builtinSupervisorRecord{}, err
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, 65537))
	if err != nil || len(data) > 65536 {
		return builtinSupervisorRecord{}, fmt.Errorf("supervisor state unreadable or oversized")
	}
	var record builtinSupervisorRecord
	if err := json.Unmarshal(data, &record); err != nil {
		return record, fmt.Errorf("supervisor state malformed")
	}
	dir, dirErr := filepath.Abs(filepath.Dir(pidPath))
	if record.Version != 1 || dirErr != nil || !strings.EqualFold(record.ConfigDir, dir) {
		return record, fmt.Errorf("supervisor state version/config unverified")
	}
	return record, nil
}

func openBuiltinSupervisorState(path string) (*os.File, error) {
	wide, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return nil, err
	}
	// Go's os.Open omits FILE_SHARE_DELETE. A status reader holding that handle
	// would make atomic replacement fail and shut down supervision mid-restart.
	h, err := windows.CreateFile(wide, windows.GENERIC_READ, windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE, nil, windows.OPEN_EXISTING, windows.FILE_ATTRIBUTE_NORMAL, 0)
	if err != nil {
		return nil, &os.PathError{Op: "open", Path: path, Err: err}
	}
	return os.NewFile(uintptr(h), path), nil
}

func writeBuiltinSupervisorState(path string, data []byte, clock daemon.SupervisorClock) error {
	deadline := clock.Now().Add(time.Second)
	for {
		err := atomicfile.WriteFileInExistingDir(path, data, atomicfile.PrivateFileMode)
		if err == nil || (!errors.Is(err, windows.ERROR_SHARING_VIOLATION) && !errors.Is(err, windows.ERROR_ACCESS_DENIED)) || !clock.Now().Before(deadline) {
			return err
		}
		// Third-party readers may omit FILE_SHARE_DELETE. Bound this transient
		// sharing race; permanent I/O failures still stop and reclaim the child.
		if err := clock.Sleep(context.Background(), 10*time.Millisecond); err != nil {
			return err
		}
	}
}

// Stop the parent first, even while no daemon PID exists. Its shutdown event
// cancels a pending backoff and gracefully stops its direct child.
func stopBuiltinSupervisor(pidPath string) (bool, error) {
	record, err := readBuiltinSupervisor(pidPath)
	if os.IsNotExist(err) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	alive, err := builtinSupervisorAliveFn(record.Instance)
	if err != nil && builtinSupervisorTerminal(record.State) {
		// A terminal record is history, not authority to signal a reusable PID.
		// It must not block a later stop/uninstall after that PID is reused.
		return false, nil
	}
	if err != nil || !alive {
		return false, err
	}
	if err := daemon.StopSupervisor(record.Instance); err != nil {
		return true, err
	}
	last, err := readBuiltinSupervisor(pidPath)
	if err != nil {
		return true, err
	}
	if last.State != "stopped" {
		return true, fmt.Errorf("supervisor exited without confirmed intentional shutdown: %s", last.State)
	}
	return true, nil
}

func clearBuiltinSupervisor(pidPath string) error {
	path, lock := builtinSupervisorPaths(pidPath)
	if _, err := os.Stat(filepath.Dir(pidPath)); os.IsNotExist(err) {
		return nil
	}
	return daemon.WithSupervisorLock(lock, func() error {
		if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
			return err
		}
		return nil
	})
}

func runBuiltinSupervisor(ctx context.Context, noAuto bool) error {
	if err := config.EnsureDir(); err != nil {
		return err
	}
	dir, err := absoluteConfigDir()
	if err != nil {
		return err
	}
	pidPath := filepath.Join(dir, "tslink.pid")
	path, lock := builtinSupervisorPaths(pidPath)
	clock := daemon.NewSupervisorClock()
	return daemon.WithSupervisorLock(lock, func() error {
		previous, err := readBuiltinSupervisor(pidPath)
		if err != nil && !os.IsNotExist(err) {
			return err
		}
		// A sign-in or task-level retry cannot reset the crash-loop breaker.
		// Explicit install clears it after stopping the old supervisor.
		if previous.State == "circuit_open" {
			return nil
		}
		if !daemon.IsPIDFileMissing(pidPath) && !daemon.IsProcessAbsentFromPIDFile(pidPath) && !daemon.IsForeignProcessFromPIDFile(pidPath) {
			return fmt.Errorf("existing daemon PID must be stopped before supervision")
		}
		instance, err := daemon.CurrentSupervisorInstance()
		if err != nil {
			return err
		}
		ctx, cleanup, err := daemon.ShutdownContext(ctx)
		if err != nil {
			return err
		}
		defer cleanup()
		job, err := daemon.NewChildJob()
		if err != nil {
			return err
		}
		defer job.Close()
		// Capture the environment once. No credentials or environment values
		// enter state/logs; every child receives precisely the same environment.
		env := os.Environ()
		workingDir, err := os.Getwd()
		if err != nil {
			return err
		}
		args := []string{"serve", "--no-browser"}
		if noAuto {
			args = append(args, "--no-auto-provision")
		}
		runErr := daemon.RunSupervisor(ctx, clock, func() (daemon.SupervisorChild, error) {
			command := exec.Command(instance.Executable, args...)
			command.Env, command.Dir = env, workingDir
			command.Stdout, command.Stderr = os.Stdout, os.Stderr
			return job.Start(command)
		}, func(state daemon.SupervisorState) error {
			data, err := json.Marshal(builtinSupervisorRecord{1, dir, instance, state})
			if err != nil {
				return err
			}
			return writeBuiltinSupervisorState(path, append(data, '\n'), clock)
		})
		// An explicit shutdown must never be retried by the task backstop,
		// including a child-stop failure. The caller checks the terminal record;
		// the job closes here and reclaims any remaining assigned child.
		if ctx.Err() != nil {
			return nil
		}
		return runErr
	})
}

func init() {
	stopSupervisorFn = stopBuiltinSupervisor
	stopServiceTransactionFn = withSupervisorTransaction
	c := &cobra.Command{Use: "supervise", Hidden: true, Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			noAuto, _ := cmd.Flags().GetBool("no-auto-provision")
			return runBuiltinSupervisor(cmd.Context(), noAuto)
		}}
	c.Flags().Bool("no-auto-provision", false, "Internal daemon policy")
	rootCmd.AddCommand(c)
}
