package tailapi

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"testing"

	"github.com/anydoor7/tslink/internal/registry"
)

func TestReview3ListCompleteResponseContract(t *testing.T) {
	for _, kind := range []string{"devices", "device-invites", "user-invites"} {
		t.Run(kind, func(t *testing.T) {
			valid := "[]"
			if kind == "devices" {
				valid = `{"devices":[]}`
			}
			cases := []struct {
				name, body string
				status     int
				valid      bool
			}{
				{"complete-empty", valid, 200, true},
				// The live API answers root null for a device or tailnet with no
				// invites (observed 2026-10-20); the devices listing has no such
				// observation and stays strict.
				{"null", "null", 200, kind != "devices"},
				{"null-padded", " \n null \t", 200, kind != "devices"},
				{"blank", "", 200, false},
				{"whitespace", " \n\t", 200, false},
				{"missing-array", "{}", 200, false},
				{"null-array", `{"devices":null}`, 200, false},
				{"wrong-type", `{"devices":{}}`, 200, false},
				{"truncated", "[", 200, false},
			}
			// Every successful-looking status other than the contract's 200.
			for status := 201; status < 300; status++ {
				cases = append(cases, struct {
					name, body string
					status     int
					valid      bool
				}{fmt.Sprint(status), valid, status, false})
			}
			for _, tc := range cases {
				t.Run(tc.name, func(t *testing.T) {
					withInviteServer(t, func(w http.ResponseWriter, r *http.Request) {
						if r.Method != http.MethodGet {
							t.Errorf("unexpected mutation %s", r.Method)
						}
						w.WriteHeader(tc.status)
						io.WriteString(w, tc.body)
					})
					client, err := inviteClientFn()
					if err != nil {
						t.Fatal(err)
					}
					// Every valid invite body above is an empty listing. Callers
					// JSON-encode the result, so it must be [] and never null.
					var encoded []byte
					switch kind {
					case "devices":
						_, err = client.ListDevices(context.Background())
					case "device-invites":
						var got []deviceInviteResponse
						got, err = client.ListDeviceInvites(context.Background(), "n1")
						if err == nil {
							encoded, _ = json.Marshal(got)
						}
					case "user-invites":
						var got []userInviteResponse
						got, err = client.ListUserInvites(context.Background())
						if err == nil {
							encoded, _ = json.Marshal(got)
						}
					}
					if (err == nil) != tc.valid {
						t.Fatalf("complete=%v error=%v", tc.valid, err)
					}
					if kind != "devices" && tc.valid && string(encoded) != "[]" {
						t.Fatalf("empty %s listing encodes as %s, want []", kind, encoded)
					}
				})
			}
		})
	}
}

func TestReview3BodylessMutationsRemainSuccessful(t *testing.T) {
	for _, status := range []int{http.StatusCreated, http.StatusNoContent, http.StatusPartialContent} {
		t.Run(fmt.Sprint(status), func(t *testing.T) {
			withInviteServer(t, func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(status) })
			client, err := inviteClientFn()
			if err != nil {
				t.Fatal(err)
			}
			ctx := context.Background()
			if err := client.DeleteDeviceInvite(ctx, "1001"); err != nil {
				t.Fatal(err)
			}
			if err := client.DeleteUserInvite(ctx, "1001"); err != nil {
				t.Fatal(err)
			}
			if err := client.ResendDeviceInvite(ctx, "1001"); err != nil {
				t.Fatal(err)
			}
			if err := client.ResendUserInvite(ctx, "1001"); err != nil {
				t.Fatal(err)
			}
			if _, err := client.CreateDeviceInvites(ctx, "n1", nil); err != nil {
				t.Fatal(err)
			}
			if _, err := client.CreateUserInvites(ctx, nil); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestUserInviteNullKeepsDeviceInvitesVisible(t *testing.T) {
	for _, users := range []string{"null", " \n null \t", "[]"} {
		t.Run(users, func(t *testing.T) {
			withInviteServer(t, func(w http.ResponseWriter, r *http.Request) {
				if r.Method != "GET" {
					t.Fatalf("unexpected mutation %s", r.Method)
				}
				switch r.URL.Path {
				case "/api/v2/tailnet/-/user-invites":
					io.WriteString(w, users)
				case "/api/v2/tailnet/-/devices":
					io.WriteString(w, `{"devices":[{"nodeId":"n1","hostname":"app"}]}`)
				case "/api/v2/device/n1/device-invites":
					io.WriteString(w, `[{"id":"123","inviteUrl":"https://fixture.invalid/invite"}]`)
				default:
					t.Errorf("unexpected path %s", r.URL.Path)
					w.WriteHeader(404)
				}
			})
			got, err := ListInvites(context.Background(), []DeviceTarget{{Service: "app", Hostname: "app", NodeID: "n1"}})
			if err != nil || !got.Complete || got.UserInvites == nil || len(got.UserInvites) != 0 || len(got.DeviceInvites) != 1 || got.DeviceInvites[0].ID != "123" {
				t.Fatalf("null user list hid device invitations: %+v %v", got, err)
			}
		})
	}
}

// Production 2026-10-20: 18 of 20 services answered device-invites with root
// null and 2 with arrays, and the whole listing came back incomplete.
func TestDeviceInviteNullListsAsCheckedEmpty(t *testing.T) {
	const arrayBody = `[{"id":"123","inviteUrl":"https://fixture.invalid/invite"}]`
	for _, tc := range []struct {
		name         string
		bodies       map[string]string // device-invites body per node ID
		wantComplete bool
		wantChecked  map[string]int // service -> invite_count, for checked targets
		wantInvites  int
	}{
		{"null-and-array", map[string]string{"n1": "null", "n2": " \n null \t", "n3": arrayBody}, true, map[string]int{"alpha": 0, "beta": 0, "gamma": 1}, 1},
		{"all-null", map[string]string{"n1": "null", "n2": "null", "n3": "null"}, true, map[string]int{"alpha": 0, "beta": 0, "gamma": 0}, 0},
		// Control: a blank 200 body is still unproven absence, so the same
		// layout with one blank target must stay incomplete.
		{"blank-control", map[string]string{"n1": "null", "n2": "", "n3": arrayBody}, false, map[string]int{"alpha": 0, "gamma": 1}, 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			withInviteServer(t, func(w http.ResponseWriter, r *http.Request) {
				if r.Method != "GET" {
					t.Fatalf("unexpected mutation %s", r.Method)
				}
				switch r.URL.Path {
				case "/api/v2/tailnet/-/user-invites":
					io.WriteString(w, "[]")
				case "/api/v2/tailnet/-/devices":
					io.WriteString(w, `{"devices":[{"nodeId":"n1","hostname":"alpha"},{"nodeId":"n2","hostname":"beta"},{"nodeId":"n3","hostname":"gamma"}]}`)
				case "/api/v2/device/n1/device-invites", "/api/v2/device/n2/device-invites", "/api/v2/device/n3/device-invites":
					io.WriteString(w, tc.bodies[r.URL.Path[len("/api/v2/device/"):len("/api/v2/device/n1")]])
				default:
					t.Errorf("unexpected path %s", r.URL.Path)
					w.WriteHeader(404)
				}
			})
			got, err := ListInvites(context.Background(), []DeviceTarget{
				{Service: "alpha", Hostname: "alpha", NodeID: "n1"},
				{Service: "beta", Hostname: "beta", NodeID: "n2"},
				{Service: "gamma", Hostname: "gamma", NodeID: "n3"},
			})
			if err != nil {
				t.Fatal(err)
			}
			if got.Complete != tc.wantComplete || got.DeviceInvites == nil || len(got.DeviceInvites) != tc.wantInvites || got.Count != tc.wantInvites {
				t.Fatalf("complete=%v device_invites=%d count=%d, want complete=%v device_invites=%d: %+v", got.Complete, len(got.DeviceInvites), got.Count, tc.wantComplete, tc.wantInvites, got)
			}
			if len(got.DeviceTargets) != 3 {
				t.Fatalf("device_targets = %+v, want one entry per service", got.DeviceTargets)
			}
			for _, target := range got.DeviceTargets {
				count, wantChecked := tc.wantChecked[target.Service]
				if target.Checked != wantChecked || target.InviteCount != count || (target.Error != nil) == wantChecked {
					t.Errorf("target %s = %+v, want checked=%v invite_count=%d", target.Service, target, wantChecked, count)
				}
				if !wantChecked && (target.Error == nil || target.Error.Code != registry.CodeInviteResponseInvalid) {
					t.Errorf("blank target %s = %+v, want %s", target.Service, target, registry.CodeInviteResponseInvalid)
				}
			}
		})
	}
}
