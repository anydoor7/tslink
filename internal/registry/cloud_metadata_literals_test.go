package registry

import (
	"net"
	"testing"
)

func TestCloudMetadataLiteralsRefused(t *testing.T) {
	for _, host := range []string{"fd00:ec2::254", "::ffff:100.100.100.200", "100.100.100.200", "metadata.tencentyun.com", "instance-data", "metadata", "METADATA.TENCENTYUN.COM."} {
		for _, kind := range []string{TypeProxy, TypeTCP} {
			t.Run(kind+"/"+host, func(t *testing.T) {
				target := net.JoinHostPort(host, "80")
				validate := ValidateTCPTarget
				if kind == TypeProxy {
					target = "http://" + target
					validate = ValidateProxyTarget
				}
				err := validate(target)
				if code, _ := ErrorCode(err); code != CodeLinkLocalTargetRefused {
					t.Fatalf("%s: code = %q, err = %v; want cloud metadata refusal", target, code, err)
				}
			})
		}
	}
}
