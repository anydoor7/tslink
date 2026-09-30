package registry

import (
	"net"
	"testing"
)

func TestLoopbackShorthandAndMetadataRespellings(t *testing.T) {
	for _, tc := range []struct {
		host    string
		refused bool
	}{
		{"127.1", false}, {"127.0.0.1", false}, {"0x7f000001", false}, {"0177.1", false}, {"2130706433", false},
		{"169.254.43518", true}, {"0xA9FEA9FE", true}, {"0251.0376.0251.0376", true}, {"2852039166", true}, {"0", true},
	} {
		for _, kind := range []string{TypeProxy, TypeTCP} {
			t.Run(kind+"/"+tc.host, func(t *testing.T) {
				target := net.JoinHostPort(tc.host, "8080")
				validate := ValidateTCPTarget
				if kind == TypeProxy {
					target = "http://" + target
					validate = ValidateProxyTarget
				}
				err := validate(target)
				if tc.refused {
					if code, _ := ErrorCode(err); code != CodeLinkLocalTargetRefused {
						t.Fatalf("metadata respelling %s: got %v, want refusal", target, err)
					}
				} else if err != nil {
					t.Fatalf("loopback %s must be accepted: %v", target, err)
				}
			})
		}
	}
}
