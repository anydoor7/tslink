package tailapi

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
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
		{"unrelated", `{"devices":[{"nodeId":"n1","hostname":"photos"}]}`, `[{"id":"9999"}]`, 200, 200, "revoked", false, 0},
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
			t.Setenv("TSLINK_API_KEY", "tskey-api-<test-only-FAKE-test>")
			state, e := RevokePendingDeviceInvite(context.Background(), DeviceTarget{Service: "photos", Hostname: "photos", NodeID: "n1"}, "1001")
			if (e != nil) != tc.wantError || state != tc.wantState || deletes != tc.wantDeletes {
				t.Fatal(state, e, deletes)
			}
		})
	}
}

func TestPeopleDeletedTargetRequiresSuccessfulOwnershipProof(t *testing.T) {
	for _, tc := range []struct {
		name, body string
		status     int
		gone       bool
	}{
		{"empty", `{"devices":[]}`, 200, true},
		{"other-node", `{"devices":[{"nodeId":"n2","hostname":"finance"}]}`, 200, true},
		{"same-name-other-node", `{"devices":[{"nodeId":"foreign","hostname":"photos"}]}`, 200, false},
		{"same-node-renamed", `{"devices":[{"nodeId":"n1","hostname":"renamed"}]}`, 200, false},
		{"transient", `{"message":"unavailable"}`, 503, false},
		{"truncated", `{"devices":[`, 200, false},
		{"missing-array", `{}`, 200, false},
		{"null-array", `{"devices":null}`, 200, false},
		{"missing-node-proof", `{"devices":[{"hostname":"finance"}]}`, 200, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != "GET" || r.URL.Path != "/api/v2/tailnet/-/devices" {
					t.Errorf("unexpected remote mutation %s %s", r.Method, r.URL.Path)
				}
				w.WriteHeader(tc.status)
				io.WriteString(w, tc.body)
			}))
			defer api.Close()
			t.Setenv(APIBaseURLEnv, api.URL)
			t.Setenv("TSLINK_API_KEY", "tskey-api-<test-only-FAKE-test>")
			state, err := RevokePendingDeviceInvite(context.Background(), DeviceTarget{Service: "photos", Hostname: "photos", NodeID: "n1"}, "1001")
			if tc.gone {
				if err != nil || state != "target_gone" {
					t.Fatal(state, err)
				}
			} else if err == nil || state != "" {
				t.Fatal("unproven target became terminal", state, err)
			}
		})
	}
}

func TestPeopleInviteListAndCredentialFailureContracts(t *testing.T) {
	target := DeviceTarget{Service: "photos", Hostname: "photos", NodeID: "n1"}
	withInviteServer(t, func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/devices") {
			io.WriteString(w, `{"devices":[{"nodeId":"n1","hostname":"photos"}]}`)
		} else {
			io.WriteString(w, `[{"id":"1001"}]`)
		}
	})
	invites, err := ListDeviceInvitesForTarget(context.Background(), target)
	if err != nil || len(invites) != 1 || invites[0].ID != "1001" {
		t.Fatal(invites, err)
	}
	gone := target
	gone.Hostname = "missing"
	gone.NodeID = "missing"
	_, err = ListDeviceInvitesForTarget(context.Background(), gone)
	var evidence *PeopleInviteTargetGone
	if !errors.As(err, &evidence) || evidence.Target != gone || !strings.Contains(err.Error(), "gone") {
		t.Fatal("missing target evidence lost", err)
	}
	if _, err := RevokePendingDeviceInvite(context.Background(), target, "../bad"); err == nil {
		t.Fatal("invalid invite ID admitted")
	}
	old := inviteClientFn
	t.Cleanup(func() { inviteClientFn = old })
	unavailable := errors.New("isolated credential failure")
	inviteClientFn = func() (inviteAPI, error) { return nil, unavailable }
	if _, err := ListDeviceInvitesForTarget(context.Background(), target); !errors.Is(err, unavailable) {
		t.Fatal(err)
	}
	if _, err := RevokePendingDeviceInvite(context.Background(), target, "1001"); !errors.Is(err, unavailable) {
		t.Fatal(err)
	}
	unknown := &DeviceInviteOutcomeUnknown{Err: unavailable}
	if unknown.Error() != unavailable.Error() {
		t.Fatal(unknown)
	}
}
