# Sharing

## Share in one command

`tslink share` infers whether its argument is a directory, a regular file, a
bare port, or `host:port`. It registers the service without overwriting an
existing name, starts the daemon when needed, waits for an exact runtime URL,
and prints only that URL to stdout. Shares use ephemeral nodes by default.

```bash
mkdir -p tslink-demo && printf '<h1>TSLink demo</h1>\n' > tslink-demo/index.html
tslink share ./tslink-demo --name demo
tslink share ./tslink-demo/index.html --name demo-file  # serves only this file
tslink share ./tslink-demo --name demo-persistent --ephemeral=false
```

To share a port instead, start a local app listening on that port first, then run
`tslink share 3000` or `tslink share localhost:8080 --name preview`.

The two path forms differ in reachable surface, and the difference is the
service's own boundary rather than a listing preference. A directory target
serves every file under it and directories without an `index.html` render a
listing. A regular-file target serves that one file: its URL is the file, the
service root redirects to it, and every other path answers 404, including the
file's siblings in the same directory. The registry records the narrowing in
the file service's `file` field; an entry without that field is a directory
share.

Neither form adds an HTTP caller restriction: tailnet policy governs reachability,
and `tslink share` has no `--allow` flag. To limit readers of
a directory, register it with `tslink add <name> --dir <directory> --allow
<principal>` instead, or pass `allow` to the MCP `share` tool.

TSLink refuses to serve its own configuration directory, a directory inside
it, or a directory that contains it (with the default layout that includes your
home directory), with `path_exposes_config_dir`; `add --dir`, `share`, the MCP
tools and the daemon all apply the check. It compares paths after resolving
symbolic links, so a hard link that you place outside the configuration
directory is served like any other file.

On a credential-free first run, the one stdout line is the Tailscale
authorization URL and stderr gives the exact `tslink url <name> --wait`
continuation. With `--json`, this is a successful `status:"needs_login"`
result containing `auth_url`, not an authentication error. Retrying the same
target reuses its existing service instead of creating suffixed orphan nodes.
