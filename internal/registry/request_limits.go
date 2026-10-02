package registry

import (
	"fmt"
	"math"
	"regexp"
	"strconv"
	"strings"
	"time"
)

const (
	DefaultMaxRequestBody       = "32MiB"
	DefaultRequestHeaderTimeout = "10s"
	DefaultRequestReadTimeout   = "30s"
	DefaultHTTPIdleTimeout      = "60s"
	// RecommendedLargeUploadBody is finite; recipes must not silently remove protection.
	RecommendedLargeUploadBody = "20GiB"
	CodeInvalidRequestLimits   = "invalid_request_limits"
	CodeRequestBodyLimit       = "request_body_limit"
	CodeRequestReadTimeout     = "request_read_timeout"
	CodeRequestHeaderTimeout   = "request_header_timeout"
)

// RequestLimits is optional. Empty fields inherit safe defaults. ReadTimeout is
// the maximum interval without a successful body read, never total upload time.
type RequestLimits struct {
	MaxBody       string `json:"max_body,omitempty"`
	UnlimitedAck  bool   `json:"unlimited_ack,omitempty"`
	HeaderTimeout string `json:"header_timeout,omitempty"`
	ReadTimeout   string `json:"read_timeout,omitempty"`
	IdleTimeout   string `json:"idle_timeout,omitempty"`
}

// EffectiveRequestLimits is the public, normalized HTTP limit view. -1 means
// an explicitly acknowledged unlimited body; timeout values are Go durations.
type EffectiveRequestLimits struct {
	MaxBodyBytes  int64  `json:"max_body_bytes"`
	HeaderTimeout string `json:"header_timeout"`
	ReadTimeout   string `json:"read_timeout"`
	IdleTimeout   string `json:"idle_timeout"`
}

// RecommendedUploadLimits lets application recipes opt into large uploads.
// Each call returns independent configuration for the recipe to customize.
func RecommendedUploadLimits() *RequestLimits {
	return &RequestLimits{MaxBody: RecommendedLargeUploadBody, ReadTimeout: "2m"}
}

var requestSizePattern = regexp.MustCompile(`^([0-9]+)\s*(B|KiB|MiB|GiB|TiB|KB|MB|GB|TB)?$`)

func ParseRequestBodySize(value string) (int64, error) {
	if value == "unlimited" {
		return -1, nil
	}
	parts := requestSizePattern.FindStringSubmatch(strings.TrimSpace(value))
	if parts == nil {
		return 0, fmt.Errorf("max request body must be a positive size (for example 512MiB or 20GiB), or unlimited")
	}
	n, err := strconv.ParseInt(parts[1], 10, 64)
	scales := map[string]int64{"": 1, "B": 1, "KiB": 1 << 10, "MiB": 1 << 20, "GiB": 1 << 30, "TiB": 1 << 40, "KB": 1000, "MB": 1000000, "GB": 1000000000, "TB": 1000000000000}
	scale := scales[parts[2]]
	if err != nil || n <= 0 || n > math.MaxInt64/scale {
		return 0, fmt.Errorf("max request body size is out of range")
	}
	return n * scale, nil
}

func ResolveRequestLimits(limits *RequestLimits) (EffectiveRequestLimits, error) {
	value := RequestLimits{}
	if limits != nil {
		value = *limits
	}
	if value.MaxBody == "" {
		value.MaxBody = DefaultMaxRequestBody
	}
	max, err := ParseRequestBodySize(value.MaxBody)
	invalid := func(message string) (EffectiveRequestLimits, error) {
		return EffectiveRequestLimits{}, CodedError{Code: CodeInvalidRequestLimits, Message: message, Next: []string{"tslink add --help"}}
	}
	if err != nil {
		return invalid(err.Error())
	}
	if max < 0 && !value.UnlimitedAck {
		return invalid("unlimited requires --ack-unlimited-request-body (MCP: unlimited_ack true)")
	}
	if max >= 0 && value.UnlimitedAck {
		return invalid("unlimited_ack is only valid with max_body unlimited")
	}
	if value.HeaderTimeout == "" {
		value.HeaderTimeout = DefaultRequestHeaderTimeout
	}
	if value.ReadTimeout == "" {
		value.ReadTimeout = DefaultRequestReadTimeout
	}
	if value.IdleTimeout == "" {
		value.IdleTimeout = DefaultHTTPIdleTimeout
	}
	durations := []*string{&value.HeaderTimeout, &value.ReadTimeout, &value.IdleTimeout}
	names := []string{"header_timeout", "read_timeout", "idle_timeout"}
	for i, p := range durations {
		d, err := time.ParseDuration(*p)
		if err != nil || d <= 0 {
			return invalid(names[i] + " must be a positive duration")
		}
		*p = d.String()
	}
	return EffectiveRequestLimits{MaxBodyBytes: max, HeaderTimeout: value.HeaderTimeout, ReadTimeout: value.ReadTimeout, IdleTimeout: value.IdleTimeout}, nil
}

func (s Service) EffectiveRequestLimits() *EffectiveRequestLimits {
	if s.Type != TypeProxy && s.Type != TypeFile {
		return nil
	}
	value, err := ResolveRequestLimits(s.RequestLimits)
	if err != nil {
		return nil
	}
	return &value
}

func RequestLimitFlag(code string) string {
	switch code {
	case CodeRequestBodyLimit:
		return "--max-request-body"
	case CodeRequestHeaderTimeout:
		return "--request-header-timeout"
	case CodeRequestReadTimeout:
		return "--request-read-timeout"
	default:
		return ""
	}
}
