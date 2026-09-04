package credentials

// Admin-console pages an operator must visit by hand. TSLink cannot mint a
// user-owned access token or an OAuth client for the operator, so every
// credential-related error and expiry warning points here instead of only
// saying "run tslink login".
const (
	KeysPageURL  = "https://login.tailscale.com/admin/settings/keys"
	OAuthPageURL = "https://login.tailscale.com/admin/settings/oauth"
)

// NextAPIKeyBootstrap is the three-step recovery sequence for a missing,
// expired, or rejected user-owned tskey-api- token. Every error next list that
// used to say only "tslink login --api-key-stdin" references this one sequence.
func NextAPIKeyBootstrap() []string {
	return []string{
		"Open " + KeysPageURL + " and click \"Generate access token...\" (user-owned tskey-api-*, expires in <= 90 days)",
		"printf %s \"$TOKEN\" | tslink login --api-key-stdin --expires-in 90d   # macOS: pbpaste | tslink login --api-key-stdin",
		"tslink login --open-keys-page   # interactive only: opens the page for you",
	}
}

// NextOAuthBootstrap is the recovery sequence for adding a tailnet-owned OAuth
// client secret, the durable credential for daemon node authentication.
func NextOAuthBootstrap() []string {
	return []string{
		"Open " + OAuthPageURL + ", click \"+ credential\", choose \"OAuth client\", and copy the client secret (tskey-client-*)",
		"printf %s \"$SECRET\" | tslink login --client-secret-stdin   # macOS: pbpaste | tslink login --client-secret-stdin",
		"tslink login --open-oauth-page   # interactive only: opens the page for you",
	}
}

// NextAPIForbidden is the recovery sequence for an HTTP 403: the credential is
// valid but its user role or OAuth scopes do not permit the operation.
func NextAPIForbidden() []string {
	return []string{
		"Verify the tskey-api-* token belongs to a tailnet user whose role (owner or admin) permits this operation, or that the OAuth client grants the required scope",
		"tslink doctor --probe-remote --json",
	}
}
