package tailapi

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/monody0007/tslink/internal/config"
	"github.com/monody0007/tslink/internal/registry"
	"github.com/monody0007/tslink/internal/testenv"
	tailscale "tailscale.com/client/tailscale/v2"
)

func TestListTSLinkDevicesReturnsOnlyTaggedDevicesSortedAndWithoutNodeIdentity(t *testing.T) {
	setup(t)
	fake := testenv.NewStatefulTailnet(t)
	useStatefulTailnet(t, fake)

	created := time.Date(2026, 8, 1, 9, 14, 0, 0, time.UTC)
	lastSeen := time.Date(2026, 9, 3, 18, 2, 11, 0, time.UTC)
	fake.SetDevices([]tailscale.Device{
		{
			NodeID: "node-worker", ID: "legacy-worker", Hostname: "worker", Name: "worker.example.ts.net",
			Tags: []string{"tag:tslink-worker"}, OS: "linux", MachineKey: "mkey:secret", NodeKey: "nodekey:secret",
			Created: tailscale.Time{Time: created}, LastSeen: &tailscale.Time{Time: lastSeen}, Authorized: true,
		},
		{
			NodeID: "node-web", ID: "legacy-web", Hostname: "app", Name: "app.example.ts.net",
			Tags: []string{"tag:tsmain"}, OS: "darwin", ConnectedToControl: true, Authorized: true,
			Created: tailscale.Time{Time: created},
		},
		{
			NodeID: "node-foreign", ID: "legacy-foreign", Hostname: "not-tslink", Tags: []string{"tag:other"},
		},
		{
			NodeID: "node-untagged", ID: "legacy-untagged", Hostname: "bare",
		},
	})

	devices, err := ListTSLinkDevices(context.Background())
	if err != nil {
		t.Fatalf("ListTSLinkDevices() error = %v", err)
	}
	if len(devices) != 2 {
		t.Fatalf("ListTSLinkDevices() = %+v, want only the two TSLink-tagged devices", devices)
	}
	if devices[0].Hostname != "app" || devices[1].Hostname != "worker" {
		t.Fatalf("hostnames = %q,%q, want hostname-sorted app,worker", devices[0].Hostname, devices[1].Hostname)
	}
	if devices[0].OS != "darwin" || !devices[0].ConnectedToControl || devices[0].LastSeenAt != nil {
		t.Fatalf("connected device = %+v, want darwin, connected, and no last-seen value", devices[0])
	}
	if devices[0].CreatedAt == nil || !devices[0].CreatedAt.Equal(created) {
		t.Fatalf("created_at = %v, want %v", devices[0].CreatedAt, created)
	}
	if devices[1].LastSeenAt == nil || !devices[1].LastSeenAt.Equal(lastSeen) {
		t.Fatalf("last_seen_at = %v, want %v", devices[1].LastSeenAt, lastSeen)
	}
	if !reflect.DeepEqual(devices[1].Tags, []string{"tag:tslink-worker"}) {
		t.Fatalf("tags = %v, want the wire tags", devices[1].Tags)
	}

	// The struct carries no node identity at all, so no caller can copy a
	// NodeID out of this read-only view into a deletion path.
	encoded, err := json.Marshal(devices)
	if err != nil {
		t.Fatalf("marshal devices: %v", err)
	}
	for _, secret := range []string{"node-worker", "node-web", "legacy-worker", "mkey:secret", "nodekey:secret"} {
		if strings.Contains(string(encoded), secret) {
			t.Fatalf("tailnet device view leaked %q: %s", secret, encoded)
		}
	}

	// Read-only: the fake still holds every device it started with.
	if len(fake.Devices()) != 4 {
		t.Fatalf("fake devices after list = %d, want all 4 retained", len(fake.Devices()))
	}
	for _, request := range fake.Requests() {
		if request.Method != http.MethodGet {
			t.Fatalf("ListTSLinkDevices issued %s %s, want GET only", request.Method, request.Path)
		}
	}
}

func TestListTSLinkDevicesHonoursConfiguredDefaultTag(t *testing.T) {
	setup(t)
	fake := testenv.NewStatefulTailnet(t)
	useStatefulTailnet(t, fake)
	fake.SetDevices([]tailscale.Device{
		{NodeID: "node-custom", Hostname: "custom", Tags: []string{"tag:custom-default"}},
	})

	if devices, err := ListTSLinkDevices(context.Background()); err != nil || len(devices) != 0 {
		t.Fatalf("ListTSLinkDevices() = %+v, err=%v, want the foreign tag filtered out", devices, err)
	}

	// setup(t) already redirected TSLINK_CONFIG_DIR into a temp home, so this
	// writes an isolated config.json rather than the operator's.
	if err := config.SaveGlobalConfig(config.GlobalConfig{DefaultTag: "tag:custom-default"}); err != nil {
		t.Fatalf("save global config: %v", err)
	}
	devices, err := ListTSLinkDevices(context.Background())
	if err != nil || len(devices) != 1 || devices[0].Hostname != "custom" {
		t.Fatalf("ListTSLinkDevices() = %+v, err=%v, want the configured default tag accepted", devices, err)
	}
}

func TestListTSLinkDevicesWithoutClientReturnsErrNoAPIClient(t *testing.T) {
	setup(t)

	devices, err := ListTSLinkDevices(context.Background())
	if !errors.Is(err, ErrNoAPIClient) {
		t.Fatalf("ListTSLinkDevices() error = %v, want ErrNoAPIClient", err)
	}
	if devices != nil {
		t.Fatalf("ListTSLinkDevices() = %+v, want no devices without a client", devices)
	}
}

func TestListTSLinkDevicesClassifiesAPIErrors(t *testing.T) {
	cases := []struct {
		name       string
		status     int
		wantCode   string
		wantCoded  bool
		wantSubstr string
	}{
		{name: "unauthorized", status: http.StatusUnauthorized, wantCode: registry.CodeAPITokenUnauthorized, wantCoded: true},
		{name: "forbidden", status: http.StatusForbidden, wantCode: registry.CodeAPIForbidden, wantCoded: true},
		{name: "server error keeps the operation prefix", status: http.StatusInternalServerError, wantSubstr: "list tailnet devices"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			setup(t)
			fake := testenv.NewStatefulTailnet(t)
			useStatefulTailnet(t, fake)
			fake.FailNext(http.MethodGet, testenv.TailnetDevicesPath, tc.status)

			_, err := ListTSLinkDevices(context.Background())
			if err == nil {
				t.Fatal("ListTSLinkDevices() succeeded, want the injected failure")
			}
			if tc.wantCoded {
				code, ok := registry.ErrorCode(err)
				if !ok || code != tc.wantCode {
					t.Fatalf("error code = %q ok=%v, want %q", code, ok, tc.wantCode)
				}
				return
			}
			if !strings.Contains(err.Error(), tc.wantSubstr) {
				t.Fatalf("error = %v, want it to name the operation %q", err, tc.wantSubstr)
			}
		})
	}
}

func TestListTSLinkDevicesPropagatesContextCancellation(t *testing.T) {
	setup(t)
	fake := testenv.NewStatefulTailnet(t)
	useStatefulTailnet(t, fake)
	fake.DelayNext(http.MethodGet, testenv.TailnetDevicesPath, 200*time.Millisecond)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()
	if _, err := ListTSLinkDevices(ctx); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("ListTSLinkDevices() error = %v, want deadline exceeded", err)
	}
}

func TestOptionalDeviceTimeMapsBothWireAbsencesToNil(t *testing.T) {
	if got := optionalDeviceTime(nil); got != nil {
		t.Fatalf("optionalDeviceTime(nil) = %v, want nil", got)
	}
	if got := optionalDeviceTime(&tailscale.Time{}); got != nil {
		t.Fatalf("optionalDeviceTime(zero) = %v, want nil for the empty-string wire value", got)
	}
	moment := time.Date(2026, 1, 2, 3, 4, 5, 0, time.FixedZone("test", 3600))
	got := optionalDeviceTime(&tailscale.Time{Time: moment})
	if got == nil || !got.Equal(moment) || got.Location() != time.UTC {
		t.Fatalf("optionalDeviceTime(%v) = %v, want the same instant normalized to UTC", moment, got)
	}
}

func TestHostnameMatchesServiceNameIsTheCleanupDiscoveryPredicate(t *testing.T) {
	cases := []struct {
		hostname string
		service  string
		want     bool
	}{
		{"app", "app", true},
		{"app-1", "app", true},
		{"app-0", "app", false},
		{"app-staging", "app", false},
		{"other", "app", false},
	}
	for _, tc := range cases {
		if got := HostnameMatchesServiceName(tc.hostname, tc.service); got != tc.want {
			t.Errorf("HostnameMatchesServiceName(%q, %q) = %v, want %v", tc.hostname, tc.service, got, tc.want)
		}
		if got := hostnameMatchesCleanupTarget(tc.hostname, tc.service); got != HostnameMatchesServiceName(tc.hostname, tc.service) {
			t.Errorf("HostnameMatchesServiceName(%q, %q) diverged from the unexported cleanup predicate", tc.hostname, tc.service)
		}
	}
}
