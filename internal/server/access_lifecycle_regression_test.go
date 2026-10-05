package server

import (
	"context"
	"github.com/anydoor7/tslink/internal/accesslog"
	"github.com/anydoor7/tslink/internal/config"
	"github.com/anydoor7/tslink/internal/registry"
	runtimesnapshot "github.com/anydoor7/tslink/internal/runtime"
	"github.com/anydoor7/tslink/internal/testenv"
	"github.com/anydoor7/tslink/internal/testwait"
	"net"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

func TestAccessActiveInitFailureRecovery(t *testing.T) {
	originalTick := lifecycleTickerInterval
	originalHealth := healthTickInterval
	lifecycleTickerInterval = 20 * time.Millisecond
	healthTickInterval = 10 * time.Millisecond
	defer func() { lifecycleTickerInterval = originalTick; healthTickInterval = originalHealth }()
	testenv.SetHome(t, t.TempDir())
	if err := config.EnsureDir(); err != nil {
		t.Fatal(err)
	}
	svc := registry.Service{Name: "healthfailure", Type: registry.TypeFile, Path: t.TempDir()}
	writeRegistry(t, []registry.Service{svc})
	dir, _ := config.Dir()
	prior, err := accesslog.New(dir, accesslog.Options{}, time.Now)
	if err != nil {
		t.Fatal(err)
	}
	prior.Record(accesslog.Event{Kind: "http", App: "prior", Decision: "allowed"})
	drainAccess(t, prior)
	control := accesslog.ReadHealth(dir)
	if control.Error != "" || control.LastWrite == nil {
		t.Fatalf("bad control %+v", control)
	}
	lock := filepath.Join(dir, "access-log", "writer.lock")
	if err = os.Remove(lock); err != nil {
		t.Fatal(err)
	}
	if err = os.Mkdir(lock, 0700); err != nil {
		t.Fatal(err)
	}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	fake := &accessListenerFake{fakeTSNetServer: &fakeTSNetServer{localClient: fakeWhoIsClient(t, whoIsUser("alice", "laptop"), nil)}, ln: ln}
	original := newTSNetServerFn
	newTSNetServerFn = func(registry.Service, string, string, string) tsnetServer { return fake }
	defer func() { newTSNetServerFn = original }()
	s, err := New("synthetic-test-key", "")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	ready, done := make(chan struct{}), make(chan error, 1)
	s.SetReadyFunc(func() error { close(ready); return nil })
	go func() { done <- s.Run(ctx) }()
	stop := sync.OnceFunc(func() {
		cancel()
		if err := <-done; err != nil {
			t.Error(err)
		}
		// Run has a bounded production shutdown, so returning from it does
		// not by itself join persistence. The fixture owns the final drain.
		if writer, ok := s.AccessLogWriter().(*accesslog.Lifecycle); ok {
			<-writer.Done()
		}
	})
	defer stop()
	testwait.Recv(t, ready, "not ready")
	for i := 0; i < 3; i++ {
		if requestAccess(t, "http://"+ln.Addr().String()+"/", "GET", "") != 200 {
			t.Fatal("live request failed")
		}
	}
	snapshotPath, _ := config.RuntimeSnapshotPath()
	waitHealth := func(recovered bool) accesslog.Health {
		t.Helper()
		var published accesslog.Health
		testwait.Until(t, "current access-log health published", func() bool {
			snapshot, err := runtimesnapshot.Load(snapshotPath)
			if err == nil && snapshot.AccessLog != nil {
				h := *snapshot.AccessLog
				if h.Drops == 3 && h.Current && ((!recovered && h.Error == "access_log_init_failed") || (recovered && h.Error == "" && len(h.MissingHistory) == 1 && h.MissingHistory[0].End != nil)) {
					published = h
					return true
				}
			}
			return false
		})
		return published
	}
	after := waitHealth(false)
	r, err := accesslog.Query(dir, accesslog.Filter{})
	if err != nil {
		t.Fatal(err)
	}
	if r.Summary.Count != 1 {
		t.Fatal("initialization failure must preserve old history", r)
	}
	if after.Error != "access_log_init_failed" || after.LastWrite != nil || after.Drops != 3 || len(after.MissingHistory) != 1 || after.MissingHistory[0].End != nil {
		t.Fatalf("current failure %+v", after)
	}
	if err := os.Remove(lock); err != nil {
		t.Fatal(err)
	}
	recovered := waitHealth(true)
	for i := 0; i < 3; i++ {
		if requestAccess(t, "http://"+ln.Addr().String()+"/", "GET", "") != 200 {
			t.Fatal("recovery request failed")
		}
	}
	stop()
	r, err = accesslog.Query(dir, accesslog.Filter{})
	if err != nil {
		t.Fatal(err)
	}
	if r.Summary.Count != 4 {
		t.Fatalf("existing listener did not recover writer: %+v", r)
	}
	if recovered.MissingHistory[0].End.Before(recovered.MissingHistory[0].Start) {
		t.Fatal("invalid missing-history window")
	}
	t.Logf("active listener: three HTTP 200 during failure, current drops=3; lifecycle retry restored logging; records=%d; missing window=%+v", r.Summary.Count, recovered.MissingHistory[0])
}
