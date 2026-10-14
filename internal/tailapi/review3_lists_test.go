package tailapi

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"testing"
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
				{"null", "null", 200, kind == "user-invites"},
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
					switch kind {
					case "devices":
						_, err = client.ListDevices(context.Background())
					case "device-invites":
						_, err = client.ListDeviceInvites(context.Background(), "n1")
					case "user-invites":
						_, err = client.ListUserInvites(context.Background())
					}
					if (err == nil) != tc.valid {
						t.Fatalf("complete=%v error=%v", tc.valid, err)
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
