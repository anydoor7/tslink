# Getting Started

## Before you begin

You need a Tailscale account with
[MagicDNS and HTTPS enabled](https://tailscale.com/docs/how-to/set-up-https-certificates)
and a receiving device signed into your tailnet. Tailnet policy must allow the connection.
TSLink embeds Tailscale on the publishing host, so no separate Tailscale installation is needed there.
Building from source also needs Go 1.27.1+ and Git. macOS hosts require
macOS 13 Ventura or later ([Go release notes](https://go.dev/doc/go1.27#darwin)).

## Install

The installation and page-creation examples below use **bash or zsh**.
For Windows requirements, see [platform support](platforms.md).

On macOS and Linux, install the Homebrew cask. Keep the fully qualified name:
Homebrew 6 and later refuse an unqualified cask from a third-party tap they do not trust,
and naming the cask in full trusts it. The macOS binary is signed with a Developer ID
certificate and notarized by Apple.

```bash
brew install --cask anydoor7/tap/tslink
```

To upgrade, run `brew upgrade --cask tslink`. If TSLink runs as a background service,
run `tslink install` again afterwards so the service starts the upgraded binary.

On Windows, download `tslink_<version>_windows_<arch>.zip` from the
[latest release](https://github.com/anydoor7/tslink/releases/latest), check it against
`checksums.txt`, and run `tslink install` from the extracted folder to start TSLink at sign-in.
The zip is not Authenticode-signed; its integrity comes from the Sigstore-signed checksums
and build-provenance attestations described in [Verify a release](verify-release.md).
Linux `.deb` and `.rpm` packages are on the same release page; see
[Release artifacts](release-artifacts.md).

To build from source instead:

```bash
git clone https://github.com/anydoor7/tslink.git
cd tslink
go install .
export PATH="$PATH:$(go env GOPATH)/bin"
```

## Share your first page

TSLink can serve this page directly, without a separate web server:

```bash
mkdir -p tslink-demo
printf '<h1>Hello from TSLink</h1>\n' > tslink-demo/index.html
tslink share ./tslink-demo --name demo
```

On first use, `share` may print a Tailscale enrollment URL. Open it to authorize the node.
If the tailnet requires device approval, an administrator must also approve it.
Then retrieve the service URL and open it on a permitted tailnet device:

```bash
tslink url demo --wait
```

No API token is required for this first share. `share` installs and starts the background
service when needed. Linux requires a working systemd user manager; see
[daemon lifecycle](daemon-lifecycle.md) for manual operation.

## More Examples

The proxy examples below require a local app already listening on the specified port.

```bash
# Expose a file directory
tslink add documents --dir ~/Documents

# Expose a database via TCP proxy
tslink add mydb --tcp localhost:5432

# Ephemeral node (auto-removed from tailnet when stopped)
tslink add demo --proxy localhost:8080 --ephemeral

# Identity-aware HTTP access control (proxy/file only)
tslink add internal --proxy localhost:9090 --allow user@example.com,tag:admin

# Public exposure via Tailscale Funnel (requires explicit acknowledgement).
# Public for 24h by default; --funnel-ttl 90m or until 2030-06-01T18:00:00Z; presets 1h, 8h, 24h, 3d, 7d
# ("never" is stored as "funnel_expires_at": "never")
tslink add public --proxy localhost:3000 --funnel --public

# ACL tags for Tailscale network policy
tslink add api --proxy localhost:8000 --tags tag:webserver,tag:production
```

Funnel ACL auto-provisioning is enabled by default for an acknowledged public service. It may write the shared `tag:tslink-funnel` tag owner and a `nodeAttrs` Funnel grant in the tailnet policy file. Pass `--no-auto-provision` to `tslink add --funnel`, `tslink serve`, or `tslink install` to disable the corresponding service or daemon setup path; the required tailnet policy must then exist already. Ordinary tag ACL writes still require `--manage-acl`.
