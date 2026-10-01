package cmd

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/anydoor7/tslink/internal/registry"
	"github.com/anydoor7/tslink/internal/testenv"
)

// TestAddBarePortMeansLocalhostLikeShare pins R5-5. `tslink share 8080` reads
// a bare port as localhost:8080, but `tslink add demo --proxy 8080` built
// http://8080 and was refused as a link-local/cloud-metadata address, and
// --tcp 8080 failed too. Both flags now read a bare port the way share does.
// host:port is the control: it must come through unchanged.
func TestAddBarePortMeansLocalhostLikeShare(t *testing.T) {
	for _, tc := range []struct {
		name       string
		flag       string
		value      string
		wantType   string
		wantTarget string
		wantPort   int
	}{
		{name: "proxy bare port", flag: "proxy", value: "8080", wantType: registry.TypeProxy, wantTarget: "http://localhost:8080"},
		{name: "tcp bare port", flag: "tcp", value: "5432", wantType: registry.TypeTCP, wantTarget: "localhost:5432", wantPort: 5432},
		{name: "proxy host and port", flag: "proxy", value: "127.0.0.1:8080", wantType: registry.TypeProxy, wantTarget: "http://127.0.0.1:8080"},
		{name: "tcp host and port", flag: "tcp", value: "127.0.0.1:5432", wantType: registry.TypeTCP, wantTarget: "127.0.0.1:5432", wantPort: 5432},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			testenv.SetHome(t, dir)
			if err := os.MkdirAll(filepath.Join(dir, ".config", "tslink"), 0o700); err != nil {
				t.Fatal(err)
			}
			if _, err := runAddCmdOutput(t, []string{"demo"}, map[string]string{tc.flag: tc.value, "no-daemon-install": "true"}); err != nil {
				t.Fatalf("add --%s %s: %v", tc.flag, tc.value, err)
			}
			reg, err := registry.Load(filepath.Join(dir, ".config", "tslink", "registry.json"))
			if err != nil || len(reg.Services) != 1 {
				t.Fatalf("registry = %+v, err=%v", reg, err)
			}
			svc := reg.Services[0]
			if svc.Type != tc.wantType || svc.Target != tc.wantTarget || svc.Port != tc.wantPort {
				t.Fatalf("stored type=%q target=%q port=%d, want %q %q %d", svc.Type, svc.Target, svc.Port, tc.wantType, tc.wantTarget, tc.wantPort)
			}
		})
	}
}

// TestAddBarePortOutOfRangeIsAUsageError keeps the digits-only form from
// falling through to the misleading link-local refusal when it is not a port.
func TestAddBarePortOutOfRangeIsAUsageError(t *testing.T) {
	for _, tc := range []struct{ flag, value string }{
		{"proxy", "0"}, {"proxy", "70000"}, {"tcp", "0"}, {"tcp", "70000"},
	} {
		params := AddParams{Name: "demo"}
		if tc.flag == "proxy" {
			params.Proxy = tc.value
		} else {
			params.TCP = tc.value
		}
		_, err := buildService(params)
		if err == nil || !strings.Contains(err.Error(), "port from 1 to 65535") || strings.Contains(err.Error(), "link-local") {
			t.Errorf("--%s %s: err=%v, want a port-range usage error", tc.flag, tc.value, err)
		}
	}
}
