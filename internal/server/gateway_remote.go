package server

import (
	"context"
	"net"
	"net/netip"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/anydoor7/tslink/internal/registry"
	"tailscale.com/ipn/ipnstate"
	"tailscale.com/tsd"
)

// The only remote transport is the gateway node's userspace Tailscale stack.
// There is deliberately no host-network fallback when this capability is absent.
type gatewayNodeDialer interface {
	Sys() *tsd.System
}

type gatewayRemoteUpstream struct {
	node    gatewayNodeDialer
	lc      *LocalClient
	target  string
	observe func(bool)
}

func (d *gatewayRemoteUpstream) check(ctx context.Context) string {
	_, code := d.status(ctx, false)
	return code
}

// Requests recheck tag and suffix even when the transport reuses a connection.
// Only activation and new dials need the full peer list.
func (d *gatewayRemoteUpstream) status(ctx context.Context, peers bool) (*ipnstate.Status, string) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if d.node == nil || d.lc == nil {
		return nil, "remote_upstream_unavailable"
	}
	read := d.lc.StatusWithoutPeers
	if peers {
		read = d.lc.Status
	}
	status, err := read(ctx)
	if err != nil || status == nil || status.Self == nil {
		return nil, "remote_upstream_unavailable"
	}
	tagged := status.Self.Tags != nil && status.Self.Tags.Len() > 0
	if d.observe != nil {
		d.observe(tagged)
	}
	if !tagged {
		return nil, "remote_upstream_requires_tag"
	}
	suffix := ""
	if status.CurrentTailnet != nil {
		suffix = status.CurrentTailnet.MagicDNSSuffix
	}
	if err := registry.ValidateRemoteTailnet(d.target, suffix); err != nil {
		if code, ok := registry.ErrorCode(err); ok {
			return nil, code
		}
		return nil, "remote_upstream_outside_tailnet"
	}
	return status, ""
}

// peer resolves only from this gateway's netmap, never host DNS.
func (d *gatewayRemoteUpstream) peer(ctx context.Context) (netip.Addr, string) {
	status, code := d.status(ctx, true)
	if code != "" {
		return netip.Addr{}, code
	}
	u, _ := url.Parse(d.target)
	for _, peer := range status.Peer {
		if peer == nil || !strings.EqualFold(strings.TrimSuffix(peer.DNSName, "."), strings.TrimSuffix(u.Hostname(), ".")) {
			continue
		}
		for _, ip := range peer.TailscaleIPs {
			if ip.IsValid() {
				return ip, ""
			}
		}
	}
	return netip.Addr{}, "remote_upstream_not_peer"
}

func (d *gatewayRemoteUpstream) DialContext(ctx context.Context, network, address string) (net.Conn, error) {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	u, err := url.Parse(d.target)
	if err != nil || registry.ValidateRemoteTarget(d.target) != nil || network != "tcp" || !strings.EqualFold(address, net.JoinHostPort(u.Hostname(), urlPort(u))) {
		return nil, registry.CodedError{Code: "portal_upstream_invalid", Message: "remote transport destination differs from registered target"}
	}
	ip, code := d.peer(ctx)
	switch code {
	case "":
	case "remote_upstream_not_peer":
		return nil, registry.CodedError{Code: "remote_upstream_not_peer", Message: "Remote upstream is not a gateway peer"}
	case "remote_upstream_requires_tag":
		return nil, registry.CodedError{Code: "remote_upstream_requires_tag", Message: "Remote upstream requires a tagged gateway"}
	case "remote_upstream_outside_tailnet":
		return nil, registry.CodedError{Code: "remote_upstream_outside_tailnet", Message: "Remote upstream is outside the gateway tailnet"}
	default:
		return nil, registry.CodedError{Code: "remote_upstream_unavailable", Message: "Remote upstream is unavailable"}
	}
	sys := d.node.Sys()
	if sys == nil {
		return nil, registry.CodedError{Code: "remote_upstream_unavailable", Message: "Gateway netstack is unavailable"}
	}
	dialer, ok := sys.Dialer.GetOK()
	if !ok || dialer == nil || dialer.NetstackDialTCP == nil || dialer.UseNetstackForIP == nil {
		return nil, registry.CodedError{Code: "remote_upstream_unavailable", Message: "Gateway netstack is unavailable"}
	}
	if !dialer.UseNetstackForIP(ip) {
		return nil, registry.CodedError{Code: "remote_upstream_not_peer", Message: "Remote upstream is not a gateway netstack peer"}
	}
	port, _ := strconv.ParseUint(urlPort(u), 10, 16)
	// Do not use Server.Dial/UserDial: both DNS and non-peer IPs can fall back
	// to the host. A peer disappearing here must fail inside netstack.
	return dialer.NetstackDialTCP(ctx, netip.AddrPortFrom(ip, uint16(port)))
}

// remoteUpstreamRefusal reports the stable code when err is this gateway
// refusing a remote upstream by policy (untagged gateway, foreign tailnet, no
// such peer, status unreadable) rather than the remote app failing.
func remoteUpstreamRefusal(err error) (string, bool) {
	code, ok := registry.ErrorCode(err)
	if !ok || !strings.HasPrefix(code, "remote_upstream_") {
		return "", false
	}
	return code, true
}

func (s *Server) remoteGatewayUpstream(run *portalRun, svc registry.Service) *gatewayRemoteUpstream {
	node, _ := run.node.tsnetSrv.(gatewayNodeDialer)
	return &gatewayRemoteUpstream{node: node, lc: run.lc, target: svc.Target, observe: func(tagged bool) {
		if run.gatewayTagged.Load() == tagged {
			return
		}
		s.mu.Lock()
		defer s.mu.Unlock()
		if s.portalRun == run && s.portalState.Gateway != nil && s.portalState.Gateway.Tagged != tagged {
			copy := *s.portalState.Gateway
			copy.Tagged = tagged
			s.portalState.Gateway = &copy
			run.gatewayTagged.Store(tagged)
			s.writeRuntimeSnapshotLocked(s.lastRegistryFingerprint, s.lastSnapshotComplete)
		}
	}}
}

func (d *gatewayRemoteUpstream) hostLabel() string {
	u, err := url.Parse(d.target)
	if err != nil {
		return "remote host"
	}
	label, _, _ := strings.Cut(u.Hostname(), ".")
	if label == "" {
		return "remote host"
	}
	return label
}
