package inspect

const WarningCodeGuestExpiry = "guest_link_expiring"
const WarningCodeGuestFunnel = "guest_funnel_unavailable"

func init() {
	WarningCodeRegistry[WarningCodeGuestExpiry] = WarningCodeMeta{Severity: "warning", Source: "guest", Description: "An active guest link expires within 24 hours."}
	WarningCodeRegistry[WarningCodeGuestFunnel] = WarningCodeMeta{Severity: "warning", Source: "guest", Description: "Guest authentication is configured but Funnel availability is not verified."}
}
