package cmd

import (
	"bytes"
	"io"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/anydoor7/tslink/internal/registry"
)

// Hold the existing command clock seam after the remote owner check. This
// schedules an ordinary committed owner change before the decision transaction.
func TestF12ReviewDecisionUsesCurrentOwner(t *testing.T) {
	for _, replace := range []bool{false, true} {
		name := "unchanged_owner_control"
		if replace {
			name = "owner_replaced_before_decision"
		}
		t.Run(name, func(t *testing.T) {
			paths, request := commandRequest(t)
			oldClock := peopleNowFn
			now := oldClock()
			entered, release := make(chan struct{}), make(chan struct{})
			var enterOnce, releaseOnce sync.Once
			unblock := func() { releaseOnce.Do(func() { close(release) }) }
			peopleNowFn = func() time.Time {
				enterOnce.Do(func() { close(entered) })
				<-release
				return now
			}
			actions := defaultMCPActions(paths, io.Discard)
			peopleNowFn = oldClock
			srv := httptest.NewServer(mcpHTTPControlPlaneHandler(t, []string{"owner"}, "owner", actions))
			defer srv.Close()
			defer unblock()
			done := make(chan []byte, 1)
			go func() {
				done <- portalRemoteCall(t, srv, "requests_approve", map[string]any{"id": request.ID, "for": "8h"})
			}()
			select {
			case <-entered:
			case <-time.After(5 * time.Second):
				t.Fatal("decision did not reach post-authorization clock")
			}
			if replace {
				if err := registry.SetPortal(paths.Registry, &registry.PortalConfig{Enabled: true, Hostname: "home", Owner: "replacement"}); err != nil {
					t.Fatal(err)
				}
			}
			unblock()
			var response []byte
			select {
			case response = <-done:
			case <-time.After(5 * time.Second):
				t.Fatal("decision did not finish")
			}
			reg, _, err := registry.Preflight(paths.Registry)
			if err != nil {
				t.Fatal(err)
			}
			t.Logf("replace=%t saved_owner=%s saved_status=%s people=%d owner_denial=%t", replace, reg.Portal.Owner, reg.Requests[0].Status, len(reg.People), bytes.Contains(response, []byte("access_request_owner_required")))
			if replace {
				if !bytes.Contains(response, []byte("access_request_owner_required")) || reg.Requests[0].Status != registry.RequestPending {
					t.Fatal("decision committed after its caller stopped being the current owner")
				}
			} else if bytes.Contains(response, []byte(`"isError":true`)) || reg.Requests[0].Status != registry.RequestApproved {
				t.Fatal("unchanged current owner could not approve")
			}
		})
	}
}

func TestF12ReviewRevokedOwnerCannotRestoreAuthorityRemotely(t *testing.T) {
	for _, restorer := range []string{"owner", "operator"} {
		t.Run(restorer, func(t *testing.T) {
			paths, request := commandRequest(t)
			if _, err := registry.RemovePerson(paths.Registry, "owner"); err != nil {
				t.Fatal(err)
			}
			actions := defaultMCPActions(paths, io.Discard)
			ownerServer := httptest.NewServer(mcpHTTPControlPlaneHandler(t, []string{"owner"}, "owner", actions))
			defer ownerServer.Close()
			restorerServer := ownerServer
			if restorer != "owner" {
				restorerServer = httptest.NewServer(mcpHTTPControlPlaneHandler(t, []string{restorer}, restorer, actions))
				defer restorerServer.Close()
			}
			args := map[string]any{"id": request.ID, "for": "8h"}
			initial := portalRemoteCall(t, ownerServer, "requests_approve", args)
			if !bytes.Contains(initial, []byte("access_request_owner_required")) {
				t.Fatal("revoked owner control was not denied")
			}
			restored := portalRemoteCall(t, restorerServer, "people_add", map[string]any{"who": "owner", "apps": []string{request.App}, "for": "8h"})
			final := portalRemoteCall(t, ownerServer, "requests_approve", args)
			reg, _, err := registry.Preflight(paths.Registry)
			if err != nil {
				t.Fatal(err)
			}
			revoked := false
			for _, p := range reg.People {
				if p.Login == "owner" {
					revoked = p.Revoked
				}
			}
			t.Logf("restorer=%s initial_owner_denied=true people_add_error=%t saved_owner=%s owner_revoked=%t saved_status=%s final_owner_denied=%t", restorer, bytes.Contains(restored, []byte(`"isError":true`)), reg.Portal.Owner, revoked, reg.Requests[0].Status, bytes.Contains(final, []byte("access_request_owner_required")))
			if !bytes.Contains(final, []byte("access_request_owner_required")) || reg.Requests[0].Status != registry.RequestPending {
				t.Fatal("remote people_add restored request approval authority for a revoked owner")
			}
		})
	}
}
