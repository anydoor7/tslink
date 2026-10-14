package server

import (
	"context"
	"net"
	"net/url"
	"strings"
	"time"

	"github.com/anydoor7/tslink/internal/registry"
)

// The only remote transport is the gateway node's userspace Tailscale stack.
// There is deliberately no host-network fallback when this capability is absent.
type gatewayNodeDialer interface {
	Dial(context.Context, string, string) (net.Conn, error)
}

type gatewayRemoteUpstream struct {
	node    gatewayNodeDialer
	lc      *LocalClient
	target  string
	observe func(bool)
}

func (d *gatewayRemoteUpstream) check(ctx context.Context) string {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if d.node == nil || d.lc == nil {
		return "remote_upstream_unavailable"
	}
	status, err := d.lc.StatusWithoutPeers(ctx)
	if err != nil || status == nil || status.Self == nil {
		return "remote_upstream_unavailable"
	}
	tagged := status.Self.Tags != nil && status.Self.Tags.Len() > 0
	if d.observe != nil {
		d.observe(tagged)
	}
	if !tagged {
		return "remote_upstream_requires_tag"
	}
	suffix := ""
	if status.CurrentTailnet != nil {
		suffix = status.CurrentTailnet.MagicDNSSuffix
	}
	if err := registry.ValidateRemoteTailnet(d.target, suffix); err != nil {
		if code, ok := registry.ErrorCode(err); ok {
			return code
		}
		return "remote_upstream_outside_tailnet"
	}
	return ""
}

func (d *gatewayRemoteUpstream) DialContext(ctx context.Context, network, address string) (net.Conn, error) {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	u, err := url.Parse(d.target)
	if err != nil || registry.ValidateRemoteTarget(d.target) != nil || network != "tcp" || !strings.EqualFold(address, net.JoinHostPort(u.Hostname(), urlPort(u))) {
		return nil, registry.CodedError{Code: "portal_upstream_invalid", Message: "remote transport destination differs from registered target"}
	}
	if code := d.check(ctx); code != "" {
		return nil, registry.CodedError{Code: code, Message: "Remote upstream is unavailable"}
	}
	return d.node.Dial(ctx, "tcp", address)
}

func (s *Server) remoteGatewayUpstream(run *portalRun, svc registry.Service) *gatewayRemoteUpstream {
	node, _ := run.node.tsnetSrv.(gatewayNodeDialer)
	return &gatewayRemoteUpstream{node: node, lc: run.lc, target: svc.Target, observe: func(tagged bool) {
		s.mu.Lock()
		defer s.mu.Unlock()
		if s.portalRun == run && s.portalState.Gateway != nil && s.portalState.Gateway.Tagged != tagged {
			copy := *s.portalState.Gateway
			copy.Tagged = tagged
			s.portalState.Gateway = &copy
			s.writeRuntimeSnapshotLocked(s.lastRegistryFingerprint, s.lastSnapshotComplete)
		}
	}}
}
