package inspect

const (
	WarningCodeAccessLogDrops       = "access_log_drops"
	WarningCodeAccessLogUnavailable = "access_log_unavailable"
)

func init() {
	WarningCodeRegistry[WarningCodeAccessLogDrops] = WarningCodeMeta{Severity: "warning", Source: "access_log", Description: "The bounded access-log queue or disk writer dropped events; inspect local storage or increase queue capacity."}
	WarningCodeRegistry[WarningCodeAccessLogUnavailable] = WarningCodeMeta{Severity: "warning", Source: "access_log", Description: "Local access logging is unavailable; inspect disk permissions and writer health."}
}
