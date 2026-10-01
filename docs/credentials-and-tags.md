# Credentials And Tags

## Credential tiers

TSLink has two authentication tiers:

- **Tier 1: zero credential (default)**: a user-owned node with no advertised tags and no remote ACL edits. This is the least-privilege path for a quick page or ephemeral share. Each fresh service node has its own enrollment URL; a one-service quick share takes one browser click. User-owned Tailscale node keys expire, so a node left running for months can eventually require re-authentication.
- **Tier 2: stored credential (opt-in)**: preserves tagged, per-service startup for durable multi-service installations. Run `tslink login` only when you need this tier. If Tier 1 services are already enrolled, restart `tslink serve` after login; TSLink records the transition and replaces their user-owned node state on the next credentialed start so tsnet cannot silently ignore the new auth key.

Tier 2 accepts one of these administrative credential types:

- **API access token** (`tskey-api-*`): generate at [Admin → Keys](https://login.tailscale.com/admin/settings/keys). Use this for the most complete automation today, including tag and device management through the Tailscale API. It expires periodically.
- **OAuth client secret** (`tskey-client-*`): generate at [Admin → OAuth](https://login.tailscale.com/admin/settings/oauth). It does not expire, but TSLink's current Tailscale tag/device automation is narrower in this mode because those operations use the Tailscale REST API. Use it only after validating your required tag/device operations.

`tslink login` guides you through either Tier 2 credential path. It does not perform a disposable browser login first. Credentials are stored in the system keychain first (macOS Keychain / Linux secret service / Windows Credential Manager). On macOS and Linux, restricted-permission file fallback succeeds only when TSLink can prove any stale keychain credential is absent or has been removed. A completely unreachable or uncertain keychain makes login fail rather than risk replacing an existing credential with an unproven file value; restore keychain access and retry. Headless operation alone does not guarantee fallback. Windows has no file fallback, because TSLink cannot prove a user-only DACL locally, so `tslink login` fails there when Credential Manager is unavailable. A running Tier 1 daemon remains unchanged until it is restarted; on the next credentialed start, existing per-service tsnet state is cleared and each service re-enrolls with its derived auth key and configured tags.

For non-interactive setup, prefer stdin. Environment variables are acceptable only when they are pre-injected by a secret manager before the command starts; do not inline secret values in the shell command because they can land in shell history:

```bash
printf %s "$TSLINK_API_KEY" | tslink login --api-key-stdin
printf %s "$TSLINK_CLIENT_SECRET" | tslink login --client-secret-stdin
```

The compatible `--api-key` and `--client-secret` flags remain available, but command-line arguments can be visible to other local processes.

## Tag Management

TSLink manages local service tags by default. Ordinary remote tag ACL mutation requires `--manage-acl`; acknowledged Funnel services use the separate default-on policy provisioning described above.

On the zero-credential Tier 1 path, registry tags remain configured but are not advertised by the user-owned node, and no remote tag/ACL API is called. The following tag behavior applies to the stored-credential Tier 2 path.

- **Default tag**: every service gets `tag:tsmain` applied automatically when `--tags` is not specified.
- **Remote ACL reads**: `tslink tags pull` fetches remote ACL tags only in API access token mode; OAuth-only mode skips the remote read and reports that an API access token is required.
- **Remote ACL writes**: `tslink login --manage-acl`, `tslink serve --manage-acl`, and `tslink tags delete-remote --manage-acl` opt in to typed whole-policy ACL writes with a machine-readable side-effect plan. This flag is for ordinary tag management; acknowledged Funnel services use the separate default-on policy provisioning described above.
- **Strict tag grammar**: tags must match `tag:<lowercase-hyphen-name>` with lowercase letters, numbers, and hyphens. Migrate legacy tags such as `tag:Web`, `tag:db_main`, or `web` with `tslink tags set <service> tag:<lowercase-hyphen-name>` or by editing `registry.json`. Invalid legacy tags fail `tslink serve` validation and must be fixed before the gateway starts.
- **Runtime auth refresh**: tag, ephemeral, and effective control-server URL changes restart affected nodes with fresh per-service auth material. A zero-credential to stored-credential login records a pending identity transition; restart `tslink serve` to clear the old user-owned node state and re-enroll with tagged credentials. Legacy `authkey` file changes also require a restart.

Use `tslink tags` to inspect and customize tag assignments:

```bash
# See all services and their tags
tslink tags list

# Fetch tags currently defined in your Tailscale ACL (requires API access token; skipped in OAuth-only mode)
tslink tags pull

# Add a tag to a specific service (node restarts automatically)
tslink tags add myapp tag:production

# Replace all tags on a service
tslink tags set myapp tag:webserver

# Change the default tag applied to new services
tslink tags set-default tag:myteam

# Remove an ACL tag owner rule globally after local safety checks and explicit ACL-management opt-in
tslink tags delete-remote tag:old-tag --force --manage-acl
```
