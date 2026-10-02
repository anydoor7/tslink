package cmd

import (
	"fmt"
	"reflect"
	"strings"

	"github.com/anydoor7/tslink/internal/registry"
	"github.com/spf13/cobra"
)

func addRequestLimitFlags(cmd *cobra.Command) {
	cmd.Flags().String("max-request-body", "", "Maximum HTTP upload size (default 32MiB); unlimited requires --ack-unlimited-request-body")
	cmd.Flags().Bool("ack-unlimited-request-body", false, "Acknowledge removal of the HTTP request body size limit")
	cmd.Flags().String("request-header-timeout", "", "HTTP header read timeout (default 10s)")
	cmd.Flags().String("request-read-timeout", "", "Maximum time without body read progress, not total upload time (default 30s)")
	cmd.Flags().String("idle-timeout", "", "HTTP keep-alive idle timeout (default 60s)")
}

func requestLimitsFromFlags(cmd *cobra.Command) *registry.RequestLimits {
	names := []string{"max-request-body", "ack-unlimited-request-body", "request-header-timeout", "request-read-timeout", "idle-timeout"}
	set := false
	for _, name := range names {
		set = set || cmd.Flags().Changed(name)
	}
	if !set {
		return nil
	}
	max, _ := cmd.Flags().GetString("max-request-body")
	ack, _ := cmd.Flags().GetBool("ack-unlimited-request-body")
	header, _ := cmd.Flags().GetString("request-header-timeout")
	read, _ := cmd.Flags().GetString("request-read-timeout")
	idle, _ := cmd.Flags().GetString("idle-timeout")
	return &registry.RequestLimits{MaxBody: max, UnlimitedAck: ack, HeaderTimeout: header, ReadTimeout: read, IdleTimeout: idle}
}

func sameEffectiveRequestLimits(a, b registry.Service) bool {
	return reflect.DeepEqual(a.EffectiveRequestLimits(), b.EffectiveRequestLimits())
}

func requestLimitDifferences(a, b registry.Service) string {
	first, _ := registry.ResolveRequestLimits(a.RequestLimits)
	second, _ := registry.ResolveRequestLimits(b.RequestLimits)
	var differences []string
	for _, field := range []struct{ flag, existing, requested string }{
		{"--max-request-body", requestBodyLimitLabel(first.MaxBodyBytes), requestBodyLimitLabel(second.MaxBodyBytes)},
		{"--request-header-timeout", first.HeaderTimeout, second.HeaderTimeout},
		{"--request-read-timeout", first.ReadTimeout, second.ReadTimeout},
		{"--idle-timeout", first.IdleTimeout, second.IdleTimeout},
	} {
		if field.existing != field.requested {
			differences = append(differences, fmt.Sprintf("%s: existing=%s, requested=%s", field.flag, field.existing, field.requested))
		}
	}
	return strings.Join(differences, "; ")
}

func requestBodyLimitLabel(bytes int64) string {
	if bytes < 0 {
		return "unlimited"
	}
	return fmt.Sprintf("%dB", bytes)
}

func requestLimitsLabel(l *registry.EffectiveRequestLimits) string {
	if l == nil {
		return "-"
	}
	return fmt.Sprintf("body=%s header=%s read-idle=%s idle=%s", requestBodyLimitLabel(l.MaxBodyBytes), l.HeaderTimeout, l.ReadTimeout, l.IdleTimeout)
}
