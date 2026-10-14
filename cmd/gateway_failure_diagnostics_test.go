package cmd

import (
	tsruntime "github.com/anydoor7/tslink/internal/runtime"
	"testing"
)

func TestGatewayFailedEnrollmentDoctor(t *testing.T) {
	for _, code := range []string{"gateway_ephemeral_refused", "gateway_tag_requires_credentials"} {
		t.Run(code, func(t *testing.T) {
			// Tag refusal happens before a GatewayState exists; marker refusal carries
			// the observed gateway with Persistent=false from the failed startup.
			d := DoctorResult{Portal: tsruntime.PortalState{Enabled: true, State: "failed", Error: code}}
			if code == "gateway_ephemeral_refused" {
				d.Portal.Gateway = &tsruntime.GatewayState{Enabled: true, Persistent: false}
			}
			diagnoseGateway(&d)
			if len(d.Findings) != 1 || d.Findings[0].Code != code || d.Findings[0].Severity != "error" {
				t.Fatalf("failed enrollment hidden from doctor: %+v", d.Findings)
			}
		})
	}
}

func TestGatewayRecoveredIdentityDoctorWarning(t *testing.T) {
	d := DoctorResult{Portal: tsruntime.PortalState{Enabled: true, State: "running", Gateway: &tsruntime.GatewayState{Enabled: true, Persistent: true, IdentityChanged: true}}}
	diagnoseGateway(&d)
	const message = "Gateway NodeID changed previously; this history warning persists after recovery. Check people invite records for any stale gateway targets."
	if len(d.Findings) != 1 || d.Findings[0].Code != "gateway_node_identity_changed" || d.Findings[0].Severity != "warning" || d.Findings[0].Message != message {
		t.Fatalf("identity history warning: %+v", d.Findings)
	}
}
