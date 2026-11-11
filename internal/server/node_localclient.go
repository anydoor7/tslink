package server

import (
	"log/slog"

	"tailscale.com/client/local"
)

// LocalClient returns this node's LocalAPI client with host-daemon
// authentication turned off.
//
// tsnet serves the node's LocalAPI in process over an in-memory listener that
// permits every read and write, so it needs no token. A local.Client without
// OmitAuth still looks one up before every request: on macOS that is
// safesocket.LocalTCPPortAndToken, which forks lsof under a process-wide lock
// to find the Tailscale app. Under launchd /usr/sbin is on PATH, so lsof is
// found and every per-request WhoIs and per-handshake GetCertificate on every
// node paid for a fork, serialized across nodes (W0130: 15-20 ms on every
// request, more under load). The client keeps tsnet's in-memory dialer and is
// built once per node so its connections are pooled.
func (s *helloTrackingServer) LocalClient() (*LocalClient, error) {
	s.lcMu.Lock()
	defer s.lcMu.Unlock()
	if s.lc != nil {
		return s.lc, nil
	}
	base, err := s.Server.LocalClient()
	if err != nil {
		return nil, err
	}
	if base.Dial == nil {
		// tsnet no longer exposes its in-memory dialer; a client without it
		// would reach the host's tailscaled instead of this node. Keep tsnet's
		// own client, which is correct but pays the discovery cost again, and
		// say so once per node so a tailscale upgrade cannot lose this fix
		// silently.
		s.lc = base
		slog.Warn("tsnet LocalAPI client exposes no in-memory dialer; node LocalAPI calls run host auth discovery again", "node", s.Hostname)
		return s.lc, nil
	}
	s.lc = &local.Client{Dial: base.Dial, OmitAuth: true}
	return s.lc, nil
}
