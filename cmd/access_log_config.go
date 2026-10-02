package cmd

import (
	"fmt"
	"strconv"

	"github.com/anydoor7/tslink/internal/accesslog"
	"github.com/anydoor7/tslink/internal/config"
)

func updateAccessLogOption(cfg *config.GlobalConfig, key, value string) error {
	if cfg.AccessLog == nil {
		cfg.AccessLog = &accesslog.Options{}
	}
	o := cfg.AccessLog
	switch key {
	case "access-log-enabled", "access-log-path":
		var v *bool
		if value != "" {
			if value != "true" && value != "false" {
				return fmt.Errorf("%s must be true or false", key)
			}
			b := value == "true"
			v = &b
		}
		if key == "access-log-enabled" {
			o.Enabled = v
		} else {
			o.RecordPath = v
		}
	case "access-log-retention-days", "access-log-max-bytes", "access-log-queue-size":
		var n int64
		if value != "" {
			var err error
			n, err = strconv.ParseInt(value, 10, 64)
			if err != nil || n <= 0 {
				return fmt.Errorf("%s must be a positive integer", key)
			}
		}
		switch key {
		case "access-log-retention-days":
			if n > 3650 {
				return fmt.Errorf("retention must be 1..3650 days")
			}
			o.RetentionDays = int(n)
		case "access-log-max-bytes":
			o.MaxBytes = n
		case "access-log-queue-size":
			if n > 65536 {
				return fmt.Errorf("queue size must be 1..65536")
			}
			o.QueueSize = int(n)
		}
	default:
		return fmt.Errorf("unknown access log option")
	}
	return o.Validate()
}
func accessLogOptionValue(cfg config.GlobalConfig, key string) (string, bool) {
	if cfg.AccessLog == nil {
		return "", false
	}
	o := cfg.AccessLog
	switch key {
	case "access-log-enabled":
		if o.Enabled != nil {
			return strconv.FormatBool(*o.Enabled), true
		}
	case "access-log-path":
		if o.RecordPath != nil {
			return strconv.FormatBool(*o.RecordPath), true
		}
	case "access-log-retention-days":
		if o.RetentionDays != 0 {
			return strconv.Itoa(o.RetentionDays), true
		}
	case "access-log-max-bytes":
		if o.MaxBytes != 0 {
			return strconv.FormatInt(o.MaxBytes, 10), true
		}
	case "access-log-queue-size":
		if o.QueueSize != 0 {
			return strconv.Itoa(o.QueueSize), true
		}
	}
	return "", false
}
