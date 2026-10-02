//go:build !darwin && !linux && !windows

package cmd

import "github.com/anydoor7/tslink/internal/testenv"

// osServiceManagerSeams is empty on platforms where this package installs no
// OS service manager through a process exit. Windows registers autostart by
// writing a Startup-folder shortcut (cmd/install_windows.go), so there is no
// service-manager binary to close off here; the guard still runs, so any
// future exit added on this platform has one obvious place to be registered.
func osServiceManagerSeams() []testenv.ServiceManagerSeam { return nil }
