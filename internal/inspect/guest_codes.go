package inspect

const WarningCodeGuestExpiry = "guest_link_expiring"
const WarningCodeGuestFunnel = "guest_funnel_unavailable"
const WarningCodeGuestCounters = "guest_counters_persistence_failed"

func init() {
	WarningCodeRegistry[WarningCodeGuestExpiry] = WarningCodeMeta{Severity: "warning", Source: "guest", Description: "An active guest link expires within 24 hours."}
	WarningCodeRegistry[WarningCodeGuestFunnel] = WarningCodeMeta{Severity: "warning", Source: "guest", Description: "Guest authentication is configured but Funnel availability is not verified."}
	WarningCodeRegistry[WarningCodeGuestCounters] = WarningCodeMeta{Severity: "warning", Source: "guest", Description: "Guest counters could not be persisted or their durability is unconfirmed."}
}
