package cmd

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/anydoor7/tslink/internal/registry"
)

func TestPublishingPrivateServiceUsesFiniteDefaultLifetime(t *testing.T) {
	binary := compiledTSLinkBinary(t)
	for _, tc := range []struct {
		name, fixture    string
		explicit, legacy bool
	}{
		{"new_public_service_default_24h", "", false, false},
		{"private_to_public_explicit_1h", `{"schema_version":1,"services":[{"name":"preview","type":"proxy","target":"http://localhost:3000"}]}`, true, false},
		{"private_to_public_default_24h", `{"schema_version":1,"services":[{"name":"preview","type":"proxy","target":"http://localhost:3000"}]}`, false, false},
		{"legacy_public_never_remains_lossless", `{"schema_version":1,"services":[{"name":"preview","type":"proxy","target":"http://localhost:3000","funnel":true,"public_ack":true,"funnel_expires_at":"never"}]}`, false, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			path := filepath.Join(dir, "registry.json")
			if tc.fixture != "" {
				if err := os.WriteFile(path, []byte(tc.fixture), 0600); err != nil {
					t.Fatal(err)
				}
			}
			args := []string{"add", "preview", "--proxy", "localhost:3000", "--funnel", "--public", "--no-daemon-install", "--json"}
			if tc.explicit {
				args = append(args, "--funnel-ttl", "1h")
			}
			started := time.Now()
			run := e2eRunBinary(t, binary, dir, "", e2eEnv(dir), args...)
			frame, _ := e2eDecodeEnvelope(t, run, "add")
			if run.ExitCode != 0 || !frame.OK {
				t.Fatalf("add failed: %+v %+v", run, frame)
			}
			reg, err := registry.Load(path)
			if err != nil {
				t.Fatal(err)
			}
			if len(reg.Services) != 1 || !reg.Services[0].Funnel {
				t.Fatalf("not published: %+v", reg)
			}
			svc := reg.Services[0]
			t.Logf("stored funnel_expires_at=%v; legacy=%v explicit=%v", svc.FunnelExpiresAt, tc.legacy, tc.explicit)
			if tc.legacy {
				if svc.FunnelExpiresAt != nil {
					t.Fatal("legacy never changed")
				}
				return
			}
			if svc.FunnelExpiresAt == nil {
				t.Fatal("new public exposure acquired a permanent deadline by preserving private service's absent expiry")
			}
			want := 24 * time.Hour
			if tc.explicit {
				want = time.Hour
			}
			if svc.FunnelExpiresAt.Before(started.Add(want)) || svc.FunnelExpiresAt.After(time.Now().Add(want)) {
				t.Fatalf("deadline %v outside operation + %v", svc.FunnelExpiresAt, want)
			}
		})
	}
}
