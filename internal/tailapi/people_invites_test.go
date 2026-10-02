package tailapi

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestPeopleInviteRemoteFailureModes(t *testing.T) {
	for _, tc := range []struct {
		name, devices, invites   string
		listStatus, deleteStatus int
		wantState                string
		wantError                bool
		wantDeletes              int
	}{
		{"pending", `{"devices":[{"nodeId":"n1","hostname":"photos"}]}`, `[{"id":"1001"}]`, 200, 200, "revoked", false, 1},
		{"accepted", `{"devices":[{"nodeId":"n1","hostname":"photos"}]}`, `[{"id":"1001","accepted":true}]`, 200, 200, "accepted", false, 0},
		{"missing", `{"devices":[{"nodeId":"n1","hostname":"photos"}]}`, `[]`, 200, 200, "revoked", false, 0},
		{"wrong-node", `{"devices":[{"nodeId":"foreign","hostname":"photos"}]}`, `[{"id":"1001"}]`, 200, 200, "", true, 0},
		{"partial-list", `{"devices":[{"nodeId":"n1","hostname":"photos"}]}`, `[`, 200, 200, "", true, 0},
		{"list-failure", `{"devices":[{"nodeId":"n1","hostname":"photos"}]}`, `{"message":"failed"}`, 503, 200, "", true, 0},
		{"invalid-remote-id", `{"devices":[{"nodeId":"n1","hostname":"photos"}]}`, `[{"id":"../bad"}]`, 200, 200, "", true, 0},
		{"delete-failure", `{"devices":[{"nodeId":"n1","hostname":"photos"}]}`, `[{"id":"1001"}]`, 200, 503, "", true, 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			deletes := 0
			api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method == "DELETE" {
					deletes++
					w.WriteHeader(tc.deleteStatus)
					io.WriteString(w, `{}`)
					return
				}
				if r.URL.Path == "/api/v2/tailnet/-/devices" {
					io.WriteString(w, tc.devices)
					return
				}
				if r.URL.Path == "/api/v2/device/n1/device-invites" {
					w.WriteHeader(tc.listStatus)
					io.WriteString(w, tc.invites)
					return
				}
				t.Errorf("unexpected route %s %s", r.Method, r.URL.Path)
			}))
			defer api.Close()
			t.Setenv(APIBaseURLEnv, api.URL)
			t.Setenv("TSLINK_API_KEY", "tskey-api-FAKE-test")
			state, e := RevokePendingDeviceInvite(context.Background(), DeviceTarget{Service: "photos", Hostname: "photos", NodeID: "n1"}, "1001")
			if (e != nil) != tc.wantError || state != tc.wantState || deletes != tc.wantDeletes {
				t.Fatal(state, e, deletes)
			}
		})
	}
}
