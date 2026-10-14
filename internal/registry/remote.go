package registry

import (
	"net"
	"net/url"
	"strconv"
	"strings"
)

// ValidateRemoteTarget is the offline part of remote admission. Runtime checks
// additionally bind this MagicDNS hostname to the gateway's observed tailnet.
func ValidateRemoteTarget(target string) error {
	u, err := url.Parse(target)
	if err != nil || u.Scheme != "https" || u.User != nil || u.Opaque != "" || u.Fragment != "" {
		return gatewayError("portal_upstream_invalid", "remote upstream requires an HTTPS MagicDNS URL without credentials or a fragment")
	}
	host := strings.TrimSuffix(strings.ToLower(u.Hostname()), ".")
	if net.ParseIP(host) != nil || !strings.HasSuffix(host, ".ts.net") || len(strings.Split(host, ".")) < 4 {
		return gatewayError("portal_upstream_invalid", "remote upstream must be a MagicDNS hostname, not an IP address")
	}
	for _, label := range strings.Split(host, ".") {
		if ValidateName(label) != nil {
			return gatewayError("portal_upstream_invalid", "remote upstream hostname is invalid")
		}
	}
	if u.Port() != "" {
		n, err := strconv.Atoi(u.Port())
		if err != nil || n < 1 || n > 65535 {
			return gatewayError("portal_upstream_invalid", "remote upstream port must be within 1-65535")
		}
	} else if strings.Contains(u.Host, ":") {
		return gatewayError("portal_upstream_invalid", "remote upstream port is empty")
	}
	return nil
}

func ValidateRemoteTailnet(target, suffix string) error {
	if err := ValidateRemoteTarget(target); err != nil {
		return err
	}
	u, _ := url.Parse(target)
	host := strings.TrimSuffix(strings.ToLower(u.Hostname()), ".")
	suffix = strings.TrimSuffix(strings.ToLower(suffix), ".")
	label, ok := strings.CutSuffix(host, "."+suffix)
	if suffix == "" || !ok || ValidateName(label) != nil {
		return gatewayError("remote_upstream_outside_tailnet", "remote upstream is outside the gateway's observed tailnet suffix")
	}
	return nil
}
