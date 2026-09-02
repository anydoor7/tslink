package testenv

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"testing"
	"time"

	tailscale "tailscale.com/client/tailscale/v2"
)

func TestStatefulTailnetImplementsV29DevicesPolicyAndSettings(t *testing.T) {
	fake := NewStatefulTailnet(t)
	fake.SetDevices([]tailscale.Device{
		{ID: "legacy-1", NodeID: "node-one", Hostname: "web", Tags: []string{"tag:tsmain"}},
		{ID: "legacy-2", NodeID: "node-two", Hostname: "worker", Tags: []string{"tag:worker"}},
	})
	fake.SetPolicy(`{
		// HuJSON remains available to PolicyFile.Raw.
		"tagOwners": {"tag:tsmain": ["autogroup:admin"]}
	}`)
	fake.SetTailnetSettings(tailscale.TailnetSettings{HTTPSEnabled: true, DevicesApprovalOn: true})
	client := fake.Client("test-placeholder")

	devices, err := client.Devices().List(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(devices) != 2 || devices[0].NodeID != "node-one" || strings.Join(devices[0].Tags, ",") != "tag:tsmain" {
		t.Fatalf("devices = %+v, want v2.9.0 nodeId/hostname/tags shape", devices)
	}
	if err := client.Devices().Delete(context.Background(), "node-one"); err != nil {
		t.Fatal(err)
	}
	devices, err = client.Devices().List(context.Background())
	if err != nil || len(devices) != 1 || devices[0].NodeID != "node-two" {
		t.Fatalf("devices after delete = %+v, err=%v", devices, err)
	}

	acl, err := client.PolicyFile().Get(context.Background())
	if err != nil || strings.Join(acl.TagOwners["tag:tsmain"], ",") != "autogroup:admin" {
		t.Fatalf("PolicyFile.Get() = %+v, err=%v", acl, err)
	}
	raw, err := client.PolicyFile().Raw(context.Background())
	if err != nil || !strings.Contains(raw.HuJSON, "HuJSON remains") || raw.ETag == "" {
		t.Fatalf("PolicyFile.Raw() = %+v, err=%v", raw, err)
	}
	previousETag := raw.ETag
	updated := `{"tagOwners":{"tag:new":["autogroup:admin"]}}`
	if err := client.PolicyFile().Set(context.Background(), updated, previousETag); err != nil {
		t.Fatal(err)
	}
	raw, err = client.PolicyFile().Raw(context.Background())
	if err != nil || raw.HuJSON != updated || raw.ETag == previousETag {
		t.Fatalf("updated policy = %+v, err=%v, previous ETag=%q", raw, err, previousETag)
	}
	if err := client.PolicyFile().Set(context.Background(), updated, previousETag); err == nil {
		t.Fatal("stale policy ETag was accepted, want HTTP 412")
	} else {
		var apiErr tailscale.APIError
		if !errors.As(err, &apiErr) || apiErr.Status != http.StatusPreconditionFailed {
			t.Fatalf("stale policy error = %v, want APIError 412", err)
		}
	}

	settings, err := client.TailnetSettings().Get(context.Background())
	if err != nil || !settings.HTTPSEnabled || !settings.DevicesApprovalOn {
		t.Fatalf("TailnetSettings.Get() = %+v, err=%v", settings, err)
	}
}

func TestStatefulTailnetCanConstructForbiddenTimeoutAndPartialFailure(t *testing.T) {
	fake := NewStatefulTailnet(t)
	client := fake.Client("test-placeholder")

	fake.FailNext(http.MethodGet, TailnetDevicesPath, http.StatusForbidden)
	if _, err := client.Devices().List(context.Background()); err == nil {
		t.Fatal("forbidden device list succeeded")
	} else {
		var apiErr tailscale.APIError
		if !errors.As(err, &apiErr) || apiErr.Status != http.StatusForbidden {
			t.Fatalf("forbidden error = %v, want APIError 403", err)
		}
	}

	fake.DelayNext(http.MethodGet, TailnetSettingsPath, 200*time.Millisecond)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()
	if _, err := client.TailnetSettings().Get(ctx); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("delayed settings error = %v, want deadline exceeded", err)
	}

	fake.SetDevices([]tailscale.Device{
		{NodeID: "node-first", Hostname: "first"},
		{NodeID: "node-second", Hostname: "second"},
	})
	fake.FailNext(http.MethodDelete, TailnetDevicePath("node-second"), http.StatusForbidden)
	if err := client.Devices().Delete(context.Background(), "node-first"); err != nil {
		t.Fatal(err)
	}
	if err := client.Devices().Delete(context.Background(), "node-second"); err == nil {
		t.Fatal("second delete succeeded, want injected partial failure")
	}
	remaining := fake.Devices()
	if len(remaining) != 1 || remaining[0].NodeID != "node-second" {
		t.Fatalf("devices after partial failure = %+v, want only failed second device", remaining)
	}

	for _, request := range fake.Requests() {
		if request.Method == "" || request.Path == "" {
			t.Fatalf("recorded request missing non-secret metadata: %+v", request)
		}
	}
}
