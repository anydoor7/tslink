// Package localapitest builds Tailscale LocalAPI clients for tests. A client
// from NewClient cannot reach the tailscaled of the machine the tests run on.
package localapitest

import (
	"errors"
	"net/http"

	"tailscale.com/client/local"
)

// ErrNoStub answers every request sent to a client built without a stub.
var ErrNoStub = errors.New("localapitest: no stub answers this LocalAPI request")

// RoundTripFunc adapts a function to http.RoundTripper.
type RoundTripFunc func(*http.Request) (*http.Response, error)

// RoundTrip calls f.
func (f RoundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) { return f(req) }

// NewClient returns a LocalAPI client whose every request goes to rt, or is
// refused with ErrNoStub when rt is nil.
//
// Both fields matter. Without Transport the client dials the platform's
// tailscaled socket. Without OmitAuth it first calls
// safesocket.LocalTCPPortAndToken, even when Transport is replaced: on macOS
// that runs lsof and reads the LocalAPI token file, on Linux it may exec
// systemctl and scan /proc. A zero local.Client does all of that.
func NewClient(rt http.RoundTripper) *local.Client {
	if rt == nil {
		rt = RoundTripFunc(func(*http.Request) (*http.Response, error) { return nil, ErrNoStub })
	}
	return &local.Client{OmitAuth: true, Transport: rt}
}
