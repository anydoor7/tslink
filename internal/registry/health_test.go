package registry

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestHealthDefaultsAndRoundTrip(t *testing.T) {
	h := HealthConfig{}.Effective()
	timeout, interval := h.Durations()
	if h.Path != "/" || h.StatusMin != 200 || h.StatusMax != 299 || timeout != 5*time.Second || interval != time.Minute {
		t.Fatalf("%+v %s %s", h, timeout, interval)
	}
	s := Service{Name: "app", Type: TypeProxy, Target: "http://localhost:3000", Health: &HealthConfig{Path: "/ready", BodyContains: "ready", Interval: "2m"}}
	if err := ValidateService(s); err != nil {
		t.Fatal(err)
	}
	b, err := json.Marshal(s)
	if err != nil {
		t.Fatal(err)
	}
	var got Service
	if err := json.Unmarshal(b, &got); err != nil {
		t.Fatal(err)
	}
	if got.Health == nil || got.Health.Path != "/ready" || got.Health.BodyContains != "ready" {
		t.Fatalf("%s", b)
	}
}

func TestInvalidHealthConfiguration(t *testing.T) {
	if err := ValidateHealthConfig(TypeProxy, &HealthConfig{BodyContains: strings.Repeat("s", 4097)}); err == nil {
		t.Fatal("oversized body condition accepted")
	}
	if err := ValidateHealthConfig(TypeProxy, &HealthConfig{Path: "/ready?"}); err == nil {
		t.Fatal("empty query accepted")
	}
	for _, h := range []HealthConfig{{Path: "https://host/secret"}, {Path: "//host/secret"}, {Path: "/ready?token=secret"}, {Path: "/ready#secret"}, {Path: "%"}, {StatusMin: 600}, {StatusMax: 99}, {StatusMin: 400, StatusMax: 200}, {Timeout: "wat"}, {Timeout: "99ms"}, {Timeout: "31s"}, {Interval: "wat"}, {Interval: "9s"}, {Interval: "2d"}, {Interval: "10s", Timeout: "30s"}} {
		if err := ValidateHealthConfig(TypeProxy, &h); err == nil {
			t.Fatalf("accepted %+v", h)
		}
	}
	for _, kind := range []string{TypeTCP, TypeFile} {
		if err := ValidateHealthConfig(kind, &HealthConfig{Path: "/"}); err == nil {
			t.Fatal("HTTP options accepted on", kind)
		}
		if err := ValidateHealthConfig(kind, &HealthConfig{Interval: "2m"}); err != nil {
			t.Fatal(err)
		}
	}
}
