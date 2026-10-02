package health

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/anydoor7/tslink/internal/registry"
)

func TestProbeRefusedTargetsBeforeIO(t *testing.T) {
	// Cancellation keeps the old implementation from contacting a refused
	// host in RED runs. Its timeout/dial-failure code still exposes the bypass.
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	for _, tc := range []struct{ kind, target string }{
		{registry.TypeProxy, "http://169.254.169.254/latest/meta-data/"},
		{registry.TypeProxy, "http://metadata.google.internal/"},
		{registry.TypeProxy, "http://[fe80::1%25en0]/"},
		{registry.TypeProxy, "http://0.0.0.0/"},
		{registry.TypeTCP, "169.254.169.254:80"},
		{registry.TypeTCP, "metadata:80"},
		{registry.TypeTCP, "[fe80::1%en0]:80"},
		{registry.TypeTCP, "127.0.0.1:0"},
	} {
		t.Run(tc.kind+"/"+tc.target, func(t *testing.T) {
			if got := Probe(ctx, registry.Service{Type: tc.kind, Target: tc.target}); got != "health_target_invalid" {
				t.Fatalf("refusal must precede I/O/cancellation: got %q", got)
			}
		})
	}
}

func TestFileProbeReplacementAndCancellation(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "app")
	if err := os.WriteFile(path, []byte("old"), 0600); err != nil {
		t.Fatal(err)
	}
	if got := probeFile(context.Background(), path, false); got != "" {
		t.Fatal("control", got)
	}
	got := probeFileWithOpen(context.Background(), path, false, func(path string) (*os.File, error) {
		// Retain the original inode so immediate inode reuse cannot mask a race.
		if err := os.Rename(path, path+".old"); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte("replacement"), 0600); err != nil {
			t.Fatal(err)
		}
		return openProbeFile(path)
	})
	if got != "health_file_invalid" {
		t.Fatalf("replacement accepted: %q", got)
	}
	got = probeFileWithOpen(context.Background(), path, false, func(path string) (*os.File, error) {
		f, err := openProbeFile(path)
		if err == nil {
			_ = f.Close()
		}
		return f, err
	})
	if got != "health_file_invalid" {
		t.Fatal("fstat failure", got)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if got := probeFile(ctx, path, false); got != "health_timeout" {
		t.Fatal(got)
	}
	got = probeFileWithOpen(context.Background(), path, false, func(path string) (*os.File, error) { return nil, os.ErrPermission })
	if got != "health_file_unavailable" {
		t.Fatal(got)
	}
	ctx, cancel = context.WithCancel(context.Background())
	got = probeFileWithOpen(ctx, path, false, func(path string) (*os.File, error) { cancel(); return openProbeFile(path) })
	if got != "health_timeout" {
		t.Fatal(got)
	}
}
