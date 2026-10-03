package server

import (
	"context"
	"fmt"
	"net"
	"sync/atomic"
	"time"

	"github.com/anydoor7/tslink/internal/accesslog"
)

type tcpAccessKey struct{}
type tcpAccessOptions struct {
	writer   accesslog.Writer
	identity *IdentityResolver
	now      func() time.Time
}

var tcpAccessSequence atomic.Uint64

type accessTCPConn struct {
	net.Conn
	in  atomic.Int64
	out atomic.Int64
}

func (c *accessTCPConn) Read(p []byte) (int, error) {
	n, e := c.Conn.Read(p)
	c.in.Add(int64(n))
	return n, e
}
func (c *accessTCPConn) Write(p []byte) (int, error) {
	n, e := c.Conn.Write(p)
	c.out.Add(int64(n))
	return n, e
}
func (c *accessTCPConn) CloseWrite() error {
	if h, ok := c.Conn.(interface{ CloseWrite() error }); ok {
		return h.CloseWrite()
	}
	return nil
}
func tcpAccessBegin(ctx context.Context, conn net.Conn, name, reason string) (net.Conn, func()) {
	opts, _ := ctx.Value(tcpAccessKey{}).(*tcpAccessOptions)
	if opts == nil || opts.writer == nil {
		return conn, func() {}
	}
	start := time.Now()
	at := opts.now().UTC()
	id := fmt.Sprintf("%x-%x", at.UnixNano(), tcpAccessSequence.Add(1))
	remote := conn.RemoteAddr().String()
	decision := "allowed"
	if reason != "" {
		decision = "denied"
	}
	e := accesslog.Event{Time: at, Kind: "tcp_open", App: name, Connection: id, Decision: decision, Reason: reason}
	recordAccess(opts.writer, e, opts.identity, remote, false)
	counted := &accessTCPConn{Conn: conn}
	return counted, func() {
		e.Kind = "tcp_close"
		e.Time = opts.now().UTC()
		e.BytesIn = counted.in.Load()
		e.BytesOut = counted.out.Load()
		e.DurationMS = float64(time.Since(start)) / float64(time.Millisecond)
		recordAccess(opts.writer, e, opts.identity, remote, false)
	}
}
