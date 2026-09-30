package errcode

import "testing"

func TestAuthClassIncludesAuthorization(t *testing.T) {
	row, ok := Lookup(AuthError)
	if !ok || row.Description != "authentication or authorization required or rejected" {
		t.Errorf("auth class description = %q", row.Description)
	}
	for _, code := range []string{AuthError, "mcp_elevated_invite_refused", "api_forbidden", "invite_api_forbidden"} {
		if exit := ExitFor(code); exit != 3 {
			t.Errorf("%s exits %d, want authorization class 3", code, exit)
		}
	}
}
