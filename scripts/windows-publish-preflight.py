#!/usr/bin/env python3
"""Read-only checks before GoReleaser can publish a stable release."""
import json
import os
import sys
import urllib.error
import urllib.request


def get(path, token):
    base = os.environ.get("GITHUB_API_URL", "https://api.github.com").rstrip("/")
    request = urllib.request.Request(base + path, headers={
        "Authorization": "Bearer " + token,
        "Accept": "application/vnd.github+json",
        "X-GitHub-Api-Version": "2022-11-28",
    })
    try:
        with urllib.request.urlopen(request, timeout=15) as response:
            return json.load(response), response.headers
    except (urllib.error.URLError, ValueError):
        # Never echo response bodies, headers or credentials.
        raise ValueError("GitHub readback failed for " + path) from None


def main():
    app_token = os.environ.get("HOMEBREW_TAP_GITHUB_TOKEN", "")
    winget_token = os.environ.get("WINGET_GITHUB_TOKEN", "")
    if not app_token or not winget_token:
        raise ValueError("Package-manager tokens must both be present")
    for name in ("homebrew-tap", "scoop-bucket"):
        repo, _ = get("/repos/anydoor7/" + name, app_token)
        if repo.get("archived") or repo.get("private") or not repo.get("permissions", {}).get("push"):
            raise ValueError(name + " must be public, writable and unarchived")
        get("/repos/anydoor7/" + name + "/branches/main", app_token)
    _, headers = get("/user", winget_token)
    scopes = {s.strip() for s in headers.get("X-OAuth-Scopes", "").split(",") if s.strip()}
    if scopes != {"public_repo"}:
        raise ValueError("WINGET_GITHUB_TOKEN must be a classic PAT with only public_repo scope")
    fork, _ = get("/repos/anydoor7/winget-pkgs", winget_token)
    if (not fork.get("fork") or fork.get("parent", {}).get("full_name") != "microsoft/winget-pkgs"
            or fork.get("private") or fork.get("archived")
            or not fork.get("permissions", {}).get("push")):
        raise ValueError("anydoor7/winget-pkgs must be a writable public fork of microsoft/winget-pkgs")
    upstream, _ = get("/repos/microsoft/winget-pkgs", winget_token)
    if upstream.get("default_branch") != "master" or upstream.get("archived"):
        raise ValueError("microsoft/winget-pkgs must be unarchived with default branch master")
    print("Package-manager repository and token readback passed")


if __name__ == "__main__":
    try:
        main()
    except ValueError as error:
        print("::error::" + str(error), file=sys.stderr)
        sys.exit(1)
