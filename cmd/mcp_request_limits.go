package cmd

var mcpRequestLimitsInputSchema = objectSchema(map[string]any{
	"max_body":       map[string]any{"type": "string", "description": "Positive upload size, for example 20GiB; default 32MiB. unlimited requires unlimited_ack true."},
	"unlimited_ack":  map[string]any{"type": "boolean", "description": "Explicit acknowledgement removing the body size cap; only valid with max_body unlimited."},
	"header_timeout": map[string]any{"type": "string", "description": "Positive HTTP header read duration; default 10s."},
	"read_timeout":   map[string]any{"type": "string", "description": "Positive maximum interval without body read progress; default 30s, not total upload duration."},
	"idle_timeout":   map[string]any{"type": "string", "description": "Positive HTTP keep-alive idle duration; default 60s."},
})

var mcpRequestLimitsOutputSchema = objectSchema(map[string]any{
	"max_body_bytes": map[string]any{"type": "integer", "minimum": -1, "description": "Effective byte limit; -1 means explicitly acknowledged unlimited."},
	"header_timeout": map[string]any{"type": "string"},
	"read_timeout":   map[string]any{"type": "string", "description": "Maximum interval without upload read progress."},
	"idle_timeout":   map[string]any{"type": "string"},
}, "max_body_bytes", "header_timeout", "read_timeout", "idle_timeout")
