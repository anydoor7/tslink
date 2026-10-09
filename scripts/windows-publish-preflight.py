#!/usr/bin/env python3
"""Read-only checks before GoReleaser can publish a stable release."""
import http.client
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
    except (urllib.error.URLError, http.client.HTTPException, OSError, ValueError):
        # Never echo response bodies, headers or credentials.
        raise ValueError("GitHub readback failed for " + path) from None


def main():
    app_token = os.environ.get("HOMEBREW_TAP_GITHUB_TOKEN", "")
    if not app_token:
        raise ValueError("Package-manager App token must be present")
    # Installation tokens get no `permissions` key from GET /repos; list the
    # repositories this token covers instead. Contents write is guaranteed by
    # the mint step (permission-contents: write fails otherwise).
    installation, _ = get("/installation/repositories?per_page=100", app_token)
    covered = {r.get("full_name"): r for r in installation.get("repositories", [])}
    for name in ("homebrew-tap", "scoop-bucket"):
        repo = covered.get("anydoor7/" + name)
        if repo is None or repo.get("archived") or repo.get("private"):
            raise ValueError(name + " must be a public, unarchived repository covered by the App token")
        get("/repos/anydoor7/" + name + "/branches/main", app_token)
    print("Package-manager repository and token readback passed")


if __name__ == "__main__":
    try:
        main()
    except ValueError as error:
        print("::error::" + str(error), file=sys.stderr)
        sys.exit(1)
