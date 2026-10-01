package server

import (
	"context"
	"errors"
	"os"
	"testing"

	"github.com/anydoor7/tslink/internal/config"
	"github.com/anydoor7/tslink/internal/registry"
	"github.com/anydoor7/tslink/internal/testenv"
)

// TestStoppedFileNodeReleasesServedDirectory pins that stopping a file node
// releases the os.Root it holds on the served directory. Windows refuses to
// delete or rename a directory while any handle to it is open, so a handle kept
// past the stop would make the user's own directory undeletable for as long as
// the daemon runs. The os.Remove below only discriminates on Windows (unix
// unlinks an open directory); the ErrClosed check discriminates everywhere.
func TestStoppedFileNodeReleasesServedDirectory(t *testing.T) {
	for _, tc := range []struct {
		name string
		stop func(*Server)
	}{
		{name: "graceful shutdown", stop: func(s *Server) { s.closeAllNodes() }},
		{name: "service removal", stop: func(s *Server) {
			s.mu.Lock()
			s.stopNodeLocked("files")
			s.mu.Unlock()
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			testenv.SetHome(t, t.TempDir())
			if err := config.EnsureDir(); err != nil {
				t.Fatalf("EnsureDir() error = %v", err)
			}
			oldNew := newTSNetServerFn
			newTSNetServerFn = func(registry.Service, string, string, string) tsnetServer { return &fakeTSNetServer{} }
			t.Cleanup(func() { newTSNetServerFn = oldNew })

			s, err := New("key", "")
			if err != nil {
				t.Fatalf("New() error = %v", err)
			}
			t.Cleanup(s.closeAllNodes)
			served := t.TempDir()
			if err := s.startNodeLocked(context.Background(), registry.Service{Name: "files", Type: registry.TypeFile, Path: served}); err != nil {
				t.Fatalf("startNodeLocked() error = %v", err)
			}
			node := s.nodes["files"]
			if node == nil {
				t.Fatal("file node was not registered")
			}
			handler, ok := node.handlerCloser.(*FileHandler)
			if !ok {
				t.Fatalf("file node handler closer = %T, want *FileHandler", node.handlerCloser)
			}
			// Control: while the node runs, its root is open.
			if f, err := handler.fsys.root.Open("."); err != nil {
				t.Fatalf("running file node cannot open its served root: %v", err)
			} else {
				_ = f.Close()
			}

			tc.stop(s)

			if _, err := handler.fsys.root.Open("."); !errors.Is(err, os.ErrClosed) {
				t.Fatalf("served root still open after the node stopped: Open(.) error = %v, want os.ErrClosed", err)
			}
			if err := os.Remove(served); err != nil {
				t.Fatalf("served directory cannot be removed after the node stopped: %v", err)
			}
		})
	}
}
