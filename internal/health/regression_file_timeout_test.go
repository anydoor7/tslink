//go:build darwin || linux

package health

import (
	"context"
	"github.com/anydoor7/tslink/internal/registry"
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"time"
)

func TestReviewFileProbeTimeout(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "file")
	if err := os.WriteFile(path, []byte("ok"), 0600); err != nil {
		t.Fatal(err)
	}
	svc := registry.Service{Type: registry.TypeFile, Path: dir, File: "file", Health: &registry.HealthConfig{Timeout: "5s"}}
	if code := Probe(context.Background(), svc); code != "" {
		t.Fatal("control", code)
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if err := syscall.Mkfifo(path, 0600); err != nil {
		t.Fatal(err)
	}
	result := make(chan string, 1)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { result <- Probe(ctx, svc) }()
	select {
	case code := <-result:
		if code != "health_file_invalid" {
			t.Fatal("wrong FIFO rejection", code)
		}
		t.Log("rejected FIFO before blocking", code)
	case <-time.After(5 * time.Second):
		t.Error("FIFO probe blocked before invalid-file rejection")
		cancel()
		select {
		case code := <-result:
			t.Log("returned on cancel", code)
		case <-time.After(5 * time.Second):
			// Join the blocked probe by opening its FIFO writer, then leave no worker.
			fd, err := syscall.Open(path, syscall.O_WRONLY|syscall.O_NONBLOCK, 0)
			if err != nil {
				t.Fatal(err)
			}
			syscall.Close(fd)
			code := <-result
			t.Errorf("probe deadline AND cancellation ignored until FIFO writer opened after the cancellation hang guard; code=%s", code)
		}
	}
}

func TestFileProbeFIFOReplacementBetweenStatAndOpen(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "file")
	if err := os.WriteFile(path, []byte("ok"), 0600); err != nil {
		t.Fatal(err)
	}
	fifo := path + ".fifo"
	if err := syscall.Mkfifo(fifo, 0600); err != nil {
		t.Fatal(err)
	}
	result := make(chan string, 1)
	go func() {
		result <- probeFileWithOpen(context.Background(), path, false, func(path string) (*os.File, error) {
			if err := os.Rename(path, path+".regular"); err != nil {
				return nil, err
			}
			if err := os.Rename(fifo, path); err != nil {
				return nil, err
			}
			return openProbeFile(path)
		})
	}()
	select {
	case got := <-result:
		if got != "health_file_invalid" {
			t.Fatal("replacement accepted", got)
		}
	case <-time.After(5 * time.Second):
		// Rescue a regressed blocking open so this test fails by assertion,
		// rather than hanging until the package's test timeout.
		fd, err := syscall.Open(path, syscall.O_WRONLY|syscall.O_NONBLOCK, 0)
		if err != nil {
			t.Fatal("FIFO rescue failed", err)
		}
		_ = syscall.Close(fd)
		got := <-result
		t.Errorf("replacement FIFO blocked until a writer opened; code=%q", got)
	}
	// Following a symlink must inspect its destination before opening it.
	link := filepath.Join(dir, "link")
	if err := os.Symlink(path, link); err != nil {
		t.Fatal(err)
	}
	if got := probeFile(context.Background(), link, false); got != "health_file_invalid" {
		t.Fatal(got)
	}
}
