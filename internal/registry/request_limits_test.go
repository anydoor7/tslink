package registry

import (
	"encoding/json"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestRequestLimitsDefaultsAndRecipe(t *testing.T) {
	got, err := ResolveRequestLimits(nil)
	if err != nil || got.MaxBodyBytes != 33554432 || got.HeaderTimeout != "10s" || got.ReadTimeout != "30s" || got.IdleTimeout != "1m0s" {
		t.Fatalf("defaults=%+v err=%v", got, err)
	}
	recipe := RecommendedUploadLimits()
	resolved, err := ResolveRequestLimits(recipe)
	if err != nil || resolved.MaxBodyBytes != 20*(1<<30) || resolved.ReadTimeout != "2m0s" {
		t.Fatalf("recipe=%+v err=%v", resolved, err)
	}
	recipe.MaxBody = "1B"
	if RecommendedUploadLimits().MaxBody != "20GiB" {
		t.Fatal("recipe aliases shared configuration")
	}
}
func TestRequestLimitsSizeGrammar(t *testing.T) {
	for _, tc := range []struct {
		in   string
		want int64
	}{{"1", 1}, {"1B", 1}, {"2 KiB", 2048}, {"512MiB", 536870912}, {"2GiB", 2147483648}, {"1TiB", 1099511627776}, {"1KB", 1000}, {"1MB", 1000000}, {"1GB", 1000000000}, {"1TB", 1000000000000}, {"unlimited", -1}} {
		t.Run(tc.in, func(t *testing.T) {
			got, err := ParseRequestBodySize(tc.in)
			if err != nil || got != tc.want {
				t.Fatalf("got=%d err=%v", got, err)
			}
		})
	}
	for _, value := range []string{"", "0", "-1", "1.5MiB", "32mb", "9223372036854775808B", "9223372036854775807GiB"} {
		t.Run(value, func(t *testing.T) {
			if _, err := ParseRequestBodySize(value); err == nil {
				t.Fatal("invalid size accepted")
			}
		})
	}
}
func TestRequestLimitsAdmission(t *testing.T) {
	for _, limits := range []*RequestLimits{{MaxBody: "unlimited"}, {UnlimitedAck: true}, {MaxBody: "bogus"}, {HeaderTimeout: "-1s"}, {ReadTimeout: "0s"}, {IdleTimeout: "forever"}} {
		t.Run(strings.TrimSpace(limits.MaxBody+limits.HeaderTimeout+limits.ReadTimeout+limits.IdleTimeout), func(t *testing.T) {
			_, err := ResolveRequestLimits(limits)
			code, _ := ErrorCode(err)
			if code != CodeInvalidRequestLimits {
				t.Fatalf("error=%v code=%s", err, code)
			}
		})
	}
	unlimited := &RequestLimits{MaxBody: "unlimited", UnlimitedAck: true, HeaderTimeout: "20s", ReadTimeout: "2m", IdleTimeout: "90s"}
	got, err := ResolveRequestLimits(unlimited)
	if err != nil || got.MaxBodyBytes != -1 || got.ReadTimeout != "2m0s" {
		t.Fatalf("unlimited=%+v err=%v", got, err)
	}
	svc := Service{Name: "db", Type: TypeTCP, Target: "localhost:5432", Port: 5432, RequestLimits: unlimited}
	if code, _ := ErrorCode(ValidateService(svc)); code != CodeInvalidRequestLimits {
		t.Fatalf("tcp code=%s", code)
	}
	if svc.EffectiveRequestLimits() != nil {
		t.Fatal("TCP advertises HTTP limits")
	}
	svc.Type = TypeProxy
	svc.Target = "http://localhost:3000"
	svc.Port = 0
	svc.RequestLimits = &RequestLimits{MaxBody: "unlimited"}
	if code, _ := ErrorCode(ValidateService(svc)); code != CodeInvalidRequestLimits {
		t.Fatalf("unacknowledged service validation code=%s", code)
	}
	if svc.EffectiveRequestLimits() != nil {
		t.Fatal("invalid limits reported as effective")
	}
}
func TestRequestLimitsRegistryRoundTripAndStrictKeys(t *testing.T) {
	path := filepath.Join(t.TempDir(), "registry.json")
	svc := Service{Name: "photos", Type: TypeProxy, Target: "http://localhost:3000", RequestLimits: RecommendedUploadLimits()}
	if _, err := Add(path, svc); err != nil {
		t.Fatal(err)
	}
	loaded, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(loaded.Services[0].RequestLimits, svc.RequestLimits) {
		t.Fatal("limits lost on registry round trip")
	}
	if got := loaded.Services[0].EffectiveRequestLimits(); got == nil || got.MaxBodyBytes != 20*(1<<30) {
		t.Fatalf("stored effective limits = %+v", got)
	}
	if err := json.Unmarshal([]byte(`{"name":"photos","type":"proxy","target":"http://localhost:3000","request_limits":{"max_bdy":"1GiB"}}`), &svc); err == nil {
		t.Fatal("unknown nested limit silently accepted")
	}
	for _, code := range []string{CodeRequestBodyLimit, CodeRequestReadTimeout, CodeRequestHeaderTimeout} {
		if RequestLimitFlag(code) == "" {
			t.Fatal("missing recovery flag")
		}
	}
	if RequestLimitFlag("unknown") != "" {
		t.Fatal("unknown code has recovery flag")
	}
}
