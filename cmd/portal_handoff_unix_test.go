//go:build !windows

package cmd

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/anydoor7/tslink/internal/testwait"
)

func TestPortalHandoffSpecialFileCleanup(t *testing.T) {
	for _, kind := range []string{"fifo", "symlink"} {
		t.Run(kind, func(t *testing.T) {
			dir := t.TempDir()
			mockServeDefaults(t, dir)
			path := filepath.Join(dir, "auth-handoff.json")
			serveSaveAuthHandoffFn, serveLoadAuthHandoffFn, serveRemoveAuthHandoffFn = saveAuthHandoff, loadAuthHandoff, removeAuthHandoff
			m := &portalHandoffRunner{mockInteractiveServer: &mockInteractiveServer{}}
			m.check = func(ctx context.Context, m *portalHandoffRunner) error {
				if err := m.authHandoff(ctx, portalHandoffEvent("home", "pending")); err != nil {
					return err
				}
				if _, err := loadAuthHandoff(path); err != nil {
					return fmt.Errorf("regular-file control: %w", err)
				}
				if err := os.Remove(path); err != nil {
					return err
				}
				if kind == "fifo" {
					if err := syscall.Mkfifo(path, 0600); err != nil {
						return err
					}
				} else {
					target := filepath.Join(dir, "unrelated-offer")
					if err := saveAuthHandoff(target, newAuthHandoffRecord("photos", "https://login.example.invalid/other", os.Getpid())); err != nil {
						return err
					}
					if err := os.Symlink(target, path); err != nil {
						return err
					}
				}
				// A FIFO with no peer blocks an open forever: returning is the
				// property. This runs inside the runner, so it reports by error.
				handed := make(chan error, 1)
				go func() { handed <- m.authHandoff(ctx, portalHandoffEvent("home", "cancelled")) }()
				var err error
				select {
				case err = <-handed:
				case <-time.After(testwait.Budget(t)):
					return fmt.Errorf("special-file cleanup stalled")
				}
				if kind == "fifo" && (err == nil || !strings.Contains(err.Error(), "regular file")) {
					return fmt.Errorf("FIFO refusal=%v want regular-file error", err)
				}
				if kind == "symlink" && (err == nil || !strings.Contains(err.Error(), "too many levels")) {
					return fmt.Errorf("symlink refusal=%v want no-follow error", err)
				}
				if _, err := os.Lstat(path); err != nil {
					return fmt.Errorf("special file removed despite refusal: %w", err)
				}
				return nil
			}
			serveNewServerFn = func(string, string) (serverRunner, error) { return m, nil }
			if err := runForegroundWithOptions(filepath.Join(dir, "tslink.pid"), "", "", foregroundOptions{AuthHandoffPath: path}); err != nil {
				t.Fatal(err)
			}
		})
	}
}
