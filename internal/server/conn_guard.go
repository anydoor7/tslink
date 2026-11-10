package server

import (
	"fmt"
	"log/slog"
	"net"
	"reflect"
	"runtime/debug"
	"sync/atomic"
)

// connectionPanics counts panics recovered on per-connection goroutines for
// the life of the process. Every recovery record carries the running total.
var connectionPanics atomic.Uint64

// recoverConnPanic confines a panic to the one connection it happened on.
// Defer it directly (recover only works there) at the top of a goroutine that
// serves a single accepted connection. Defer it after the defers that release
// the goroutine's slot or WaitGroup: it then runs first, and those still run
// after it. drop closes the connection and undoes any bookkeeping the
// goroutine owns.
//
// Only per-connection goroutines use it: there, dropping the connection is
// the whole correct outcome. A panic anywhere else still ends the process.
func recoverConnPanic(kind, service string, drop func()) {
	v := recover()
	if v == nil {
		return
	}
	total := connectionPanics.Add(1)
	slog.Error("recovered panic on a connection goroutine; dropping the connection",
		"code", "connection.panic_recovered",
		"kind", kind,
		"name", service,
		"panic", fmt.Sprint(v),
		"stack", string(debug.Stack()),
		"total_recovered", total,
	)
	drop()
}

// remoteAddrString returns the peer address of c, or "" when it is unknown.
// Call it instead of c.RemoteAddr().String(): on tsnet the accepted
// connection is a gVisor gonet.TCPConn, whose RemoteAddr returns a nil
// net.Addr once the endpoint has left the connected states (peer reset, or a
// local close such as crypto/tls interrupting a handshake whose context
// expired). Calling String on that nil interface crashes the daemon.
func remoteAddrString(c net.Conn) string {
	if c == nil {
		return ""
	}
	addr := c.RemoteAddr()
	if addr == nil {
		return ""
	}
	// A typed nil pointer inside the interface is just as unusable, and not
	// every net.Addr's String tolerates a nil receiver.
	if v := reflect.ValueOf(addr); v.Kind() == reflect.Pointer && v.IsNil() {
		return ""
	}
	return addr.String()
}
