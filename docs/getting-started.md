# Getting Started

## Before you begin

You need Go 1.26.6+, Git, a Tailscale account with
[MagicDNS and HTTPS enabled](https://tailscale.com/docs/how-to/set-up-https-certificates),
and a receiving device signed into your tailnet. Tailnet policy must allow the connection.
TSLink embeds Tailscale on the publishing host, so no separate Tailscale installation is needed there.

## Install

The installation and page-creation examples below use **bash or zsh**.
For Windows requirements, see [platform support](platforms.md).

Prebuilt releases and a Homebrew cask
have not been published; install from source:

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
