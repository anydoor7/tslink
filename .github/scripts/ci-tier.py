#!/usr/bin/env python3
"""Choose a PR tier from immutable Git trees; uncertainty costs a full gate."""

import argparse
import json
import os
from pathlib import Path, PurePosixPath
import re
import subprocess


# Include all Go OS names, the conventional unix/bsd suffixes, and legacy tags.
OS_FILE = re.compile(
    r"_(?:aix|android|darwin|dragonfly|freebsd|hurd|illumos|ios|js|linux|"
    r"netbsd|openbsd|plan9|solaris|wasip1|windows|unix|bsd).*\.go$"
)
BUILD_TAG = r"^//(go:build[[:space:]]|[[:space:]]*\+build[[:space:]])"
IMAGES = {".png", ".jpg", ".jpeg", ".gif", ".svg", ".webp", ".ico", ".avif"}
RUNNERS = ["ubuntu-latest", "macos-latest", "windows-latest"]
GIT_TIMEOUT = 30


def git(repo, *args):
    return subprocess.check_output(["git", "-C", str(repo), *args], stderr=subprocess.PIPE, timeout=GIT_TIMEOUT)


def platform_files(repo, ref):
    files = git(repo, "ls-tree", "-r", "--name-only", "-z", ref).decode().split("\0")
    special = {name for name in files if OS_FILE.search(name)}
    result = subprocess.run(
        ["git", "-C", str(repo), "grep", "-I", "-l", "-z", "-E", BUILD_TAG,
         ref, "--", "*.go"], capture_output=True, check=False, timeout=GIT_TIMEOUT,
    )
    if result.returncode not in (0, 1):
        raise RuntimeError("cannot inspect build constraints")
    # Every build constraint is conservative: OS tags trigger full, and custom
    # tags also trigger full because their platform effect may be indirect.
    special.update(name.split(":", 1)[1] for name in result.stdout.decode().split("\0") if name)
    return special


def classify(files, special=(), labels=(), draft=False):
    if draft:
        return "draft", "draft PR: heavy checks deferred until ready for review"
    if "ci:full" in labels:
        return "full", "ci:full label override"
    if not files:
        return "full", "empty change set: conservative full gate"
    special = set(special)
    packages = {str(PurePosixPath(name).parent) for name in special if name.startswith("internal/")}
    for name in files:
        path = PurePosixPath(name)
        if path.is_absolute() or ".." in path.parts:
            return "full", "unrecognized path: conservative full gate"
        if name in {"go.mod", "go.sum", "docs/cli-manifest.json"}:
            return "full", f"module or generated manifest changed: {name}"
        if name.startswith((".github/", "tools/", "internal/daemon/", "cmd/install_",
                            "cmd/supervision", "cmd/daemon_setup")):
            return "full", f"platform or gate infrastructure changed: {name}"
        if OS_FILE.search(name) or name in special:
            return "full", f"OS-specific filename or build constraint: {name}"
        if name.startswith("internal/") and any(str(parent) in packages for parent in path.parents):
            return "full", f"package has OS-specific files in base or head: {name}"
    if all(is_docs(name) for name in files):
        return "docs", "only markdown, docs, images or assets changed"
    if all(is_docs(name) or name.endswith(".go") for name in files):
        return "go", "ordinary Go change: all Linux-hosted checks, including foreign targets"
    return "full", "unrecognized non-documentation change: conservative full gate"


def is_docs(name):
    if name.endswith(".go"):
        return False
    return (name.endswith(".md") or name.startswith(("docs/", "images/", "assets/"))
            or PurePosixPath(name).suffix.lower() in IMAGES)


def decide(repo, event_name, event, files=None, base=None, head=None):
    if event_name != "pull_request":
        return "full", f"{event_name}: main and tag callers always use the full gate"
    pr = event["pull_request"]
    labels = [label["name"] for label in pr.get("labels", [])]
    if pr.get("draft") or "ci:full" in labels:
        return classify([], labels=labels, draft=pr.get("draft", False))
    try:
        base = base or pr["base"]["sha"]
        head = head or pr["head"]["sha"]
        # SHA-only arguments cannot become Git options/revision expressions.
        if not all(re.fullmatch(r"[0-9a-f]{40}", ref) for ref in (base, head)):
            raise ValueError("base/head are not immutable SHAs")
        merge_base = git(repo, "merge-base", base, head).decode().strip()
        if files is None:
            files = git(repo, "diff", "--no-renames", "--name-only", "-z",
                        merge_base, head, "--").decode().split("\0")
            files = [name for name in files if name]
        # Union catches deletions, renames and removal of the final OS file/tag.
        special = (platform_files(repo, merge_base) | platform_files(repo, base)
                   | platform_files(repo, head))
        return classify(files, special, labels)
    except (OSError, ValueError, KeyError, RuntimeError, subprocess.SubprocessError):
        return "full", "diff/build-constraint inspection unavailable: conservative full gate"


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--repo", type=Path, default=Path.cwd())
    parser.add_argument("--event", type=Path, default=os.environ.get("GITHUB_EVENT_PATH"))
    parser.add_argument("--event-name", default=os.environ.get("GITHUB_EVENT_NAME", "push"))
    args = parser.parse_args()
    event = json.loads(args.event.read_text()) if args.event else {}
    tier, reason = decide(args.repo, args.event_name, event)
    runners = RUNNERS if tier == "full" else RUNNERS[:1]
    print(json.dumps({"tier": tier, "reason": reason, "runners": runners}))
    if os.environ.get("GITHUB_OUTPUT"):
        with open(os.environ["GITHUB_OUTPUT"], "a") as output:
            output.write(f"tier={tier}\nrunners={json.dumps(runners)}\n")
    if os.environ.get("GITHUB_STEP_SUMMARY"):
        with open(os.environ["GITHUB_STEP_SUMMARY"], "a") as summary:
            summary.write(f"## CI tier: {tier}\n\nReason: {reason}\n")


if __name__ == "__main__":
    main()
