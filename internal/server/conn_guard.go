package server

import (
	"net"
	"reflect"
)

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
