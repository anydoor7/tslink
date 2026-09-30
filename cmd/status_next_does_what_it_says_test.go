package cmd

import (
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/monody0007/tslink/internal/registry"
)

// TestStatusNextNamesACommandThatDoesWhatItSays is A3-9's status finding:
// with no credential and no authorized node, status.next sent agents to
// `tslink serve --json`, which without a credential starts an unsupervised
// background daemon of its own; the next share or add then refuses to reuse
// it (daemon_supervision_unverified). next now names the command that takes
// the next step: `tslink install` starts the supervised background service,
// and with a daemon already running, `tslink status --json` is where its
// authorization URL appears.
func TestStatusNextNamesACommandThatDoesWhatItSays(t *testing.T) {
	cases := []struct {
		name       string
		running    bool
		registered bool
		want       []string
	}{
		{"no daemon, a registered service", false, true, []string{"tslink install"}},
		{"a daemon enrolling a registered service", true, true, []string{"tslink status --json"}},
		{"nothing registered", false, false, nil},
		{"a daemon with nothing registered", true, false, nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			regPath := filepath.Join(dir, "registry.json")
			if tc.registered {
				addStatusTestService(t, regPath, registry.Service{Name: "web", Type: registry.TypeProxy, Target: "http://localhost:3000"})
			}
			withStatusURLSeams(t, tc.running, 4242, time.Time{})
			status, err := getPollableStatus(filepath.Join(dir, "tslink.pid"), regPath, filepath.Join(dir, "runtime.json"), filepath.Join(dir, "auth-handoff.json"))
			if err != nil {
				t.Fatal(err)
			}
			if status.AuthStatus != authStatusNotAuthenticated || status.CredentialStored {
				t.Fatalf("fixture: status = %+v, want not_authenticated without a credential", status)
			}
			if !reflect.DeepEqual(status.Next, tc.want) {
				t.Fatalf("next = %v, want %v", status.Next, tc.want)
			}
			for _, step := range status.Next {
				if strings.Contains(step, "tslink serve") {
					t.Fatalf("next = %v still sends the agent to serve", status.Next)
				}
			}
		})
	}
}
