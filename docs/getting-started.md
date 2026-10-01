# Getting Started

## Install

Requires Go 1.26.6 or newer.

```bash
go install github.com/anydoor7/tslink@latest

# The binary lands in $(go env GOPATH)/bin, which is not on PATH by default:
export PATH="$PATH:$(go env GOPATH)/bin"
```

Homebrew and prebuilt archives arrive with the first tagged release. Until then,
installing from source is the supported path. Building from a clone works too:

```bash
git clone https://github.com/anydoor7/tslink.git && cd tslink && go install .
```

To share a page after installation:

```bash
mkdir -p tslink-demo && printf '<h1>TSLink demo</h1>\n' > tslink-demo/index.html
tslink share ./tslink-demo --name demo
```

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
# Public for 24h by default; --funnel-ttl 1h|8h|24h|72h|7d|never
# ("never" is stored as "funnel_expires_at": "never")
tslink add public --proxy localhost:3000 --funnel --public

# ACL tags for Tailscale network policy
tslink add api --proxy localhost:8000 --tags tag:webserver,tag:production
```

Funnel ACL auto-provisioning is enabled by default for an acknowledged public service. It may write the shared `tag:tslink-funnel` tag owner and a `nodeAttrs` Funnel grant in the tailnet policy file. Pass `--no-auto-provision` to `tslink add --funnel`, `tslink serve`, or `tslink install` to disable the corresponding service or daemon setup path; the required tailnet policy must then exist already. Ordinary tag ACL writes still require `--manage-acl`.

