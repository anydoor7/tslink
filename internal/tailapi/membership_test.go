package tailapi

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"

	tailscale "tailscale.com/client/tailscale/v2"
)

func TestMembershipDirectory(t *testing.T) {
	for _, tc := range []struct {
		name, body string
		code       int
		want       Membership
	}{
		{"idle-member-with-no-devices", `{"users":[{"id":"1","tailnetId":"t","loginName":" ALICE@example.com ","type":"member","status":"idle","deviceCount":0}]}`, 200, MembershipMember},
		{"shared-is-not-member", `{"users":[{"id":"1","tailnetId":"t","loginName":"alice@example.com","type":"shared","status":"active"}]}`, 200, MembershipGuest},
		{"absent", `{"users":[]}`, 200, MembershipGuest},
		{"suspended", `{"users":[{"id":"1","tailnetId":"t","loginName":"alice@example.com","type":"member","status":"suspended"}]}`, 200, ""},
		{"needs-approval", `{"users":[{"id":"1","tailnetId":"t","loginName":"alice@example.com","type":"member","status":"needs-approval"}]}`, 200, ""},
		{"missing-directory", `{}`, 200, ""},
		{"null-directory", `{"users":null}`, 200, ""},
		{"partial-record", `{"users":[{"loginName":"someone@example.com"}]}`, 200, ""},
		{"forbidden", `{}`, 403, ""},
		{"truncated", `{"users":[`, 200, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			requests := 0
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				requests++
				if r.Method != "GET" || r.URL.Path != "/api/v2/tailnet/-/users" || r.URL.RawQuery != "" {
					t.Errorf("request=%s %s", r.Method, r.URL)
				}
				w.WriteHeader(tc.code)
				fmt.Fprint(w, tc.body)
			}))
			defer srv.Close()
			base, _ := url.Parse(srv.URL)
			old := membershipClientFn
			t.Cleanup(func() { membershipClientFn = old })
			membershipClientFn = func() (*tailscale.Client, error) {
				return &tailscale.Client{BaseURL: base, Tailnet: "-", APIKey: "fixture"}, nil
			}
			got, err := ResolveMembership(context.Background(), "Alice@example.com", "")
			if got != tc.want || (err != nil) != (tc.want == "") || requests != 1 {
				t.Fatalf("membership=%q err=%v requests=%d", got, err, requests)
			}
			if _, err := ResolveMembership(context.Background(), "Alice@example.com", "https://headscale.example.invalid"); err == nil || requests != 1 {
				t.Fatal("custom control used the SaaS directory")
			}
		})
	}
}
