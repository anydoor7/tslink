#!/usr/bin/env python3
"""Choose a PR tier from immutable Git trees; uncertainty costs a full gate."""

import argparse
import fnmatch
import html
import json
import os
from pathlib import Path, PurePosixPath
import re
import subprocess
import sys


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


def tree_entries(repo, ref):
    entries = {}
    for record in git(repo, "ls-tree", "-r", "-z", ref).decode().split("\0"):
        if record:
            metadata, name = record.split("\t", 1)
            entries[name] = metadata.split()[0]
    return entries


def release_sensitive(repo, ref, entries, files):
    """Match raw Git bytes, including comments; false positives only cost CI."""
    candidates = {name.removeprefix("./") for name in files if is_docs(name.removeprefix("./"))}
    sensitive = {}
    workflow = ".github/workflows/release-candidate.yml"
    for name in entries:
        config = PurePosixPath(name).name.startswith(".goreleaser")
        if not config and name != workflow:
            continue
        source = git(repo, "show", f"{ref}:{name}")
        # Retain the old fail-closed fixtures with coarse lexical guards, never
        # by deriving payloads or interpreting YAML indentation/quoting.
        if config and (not name.endswith((".yml", ".yaml")) or re.search(
                rb"(?m)^\s*src\s*:[^*?\[\n]*$|^\s*-\s*['\"]?\{\{|\.\./|''", source)):
            raise ValueError("uncertain release metadata")
        if name == workflow and (b"Required licence and project documents present" not in source
                or b"required_license=(" not in source
                or not re.search(rb"\bbundled_(?:docs|documents)=\(", source)):
            raise ValueError("release payload check unavailable")
        for line in source.splitlines():
            if any(char in line for char in (b"*", b"?", b"[")) and (
                    b"docs" in line or b".md" in line
                    or re.search(rb"\b(?:files|src|contents)['\"]?\s*:", line)):
                sensitive.update((path, f"release metadata glob in {name}: {line!r}") for path in candidates)
        for path in candidates:
            for text in (path, PurePosixPath(path).name):
                if os.fsencode(text) in source:
                    sensitive[path] = f"release metadata substring {text!r} in {name}"
                    break
    return sensitive


def go_referenced_docs(repo, ref, files):
    """Conservatively catch runtime and test inputs in raw Go Git objects."""
    sensitive = {}
    matches = {}
    for name in files:
        path = name.removeprefix("./")
        if not is_docs(path):
            continue
        stem = re.split(r"[._]", PurePosixPath(path).name, maxsplit=1)[0]
        # Every path/basename match also contains its stem. One literal stem
        # scan therefore covers all three, including dynamically built names.
        if stem not in matches:
            if not stem or "\n" in stem:
                # grep treats newlines as separate patterns even with -F.
                # Raw matching also finds an empty stem in an empty Go blob.
                entries = tree_entries(repo, ref)
                matches[stem] = any(os.fsencode(stem) in git(repo, "show", f"{ref}:{source}")
                                    for source in entries if source.endswith(".go"))
            else:
                result = subprocess.run(
                    ["git", "-C", str(repo), "grep", "-a", "-q", "-F", "-e", stem,
                     ref, "--", "*.go"], capture_output=True, check=False, timeout=GIT_TIMEOUT,
                )
                if result.returncode not in (0, 1):
                    raise RuntimeError("cannot inspect Go document references")
                matches[stem] = result.returncode == 0
        if matches[stem]:
            sensitive[name] = f"Go source substring {stem!r} for {path!r} in {ref}"
    return sensitive


def embed_patterns(repo, ref):
    result = subprocess.run(
        ["git", "-C", str(repo), "grep", "-I", "-l", "-z", "-F", "//go:embed", ref, "--", "*.go"],
        capture_output=True, check=False, timeout=GIT_TIMEOUT,
    )
    if result.returncode not in (0, 1):
        raise RuntimeError("cannot inspect embed directives")
    patterns = set()
    for entry in result.stdout.decode().split("\0"):
        if not entry:
            continue
        name = entry.split(":", 1)[1]
        source = git(repo, "show", f"{ref}:{name}").decode()
        for line in source.splitlines():
            if "//go:embed" not in line:
                continue
            if not line.startswith("//go:embed ") and not line.startswith("//go:embed\t"):
                raise ValueError("uncertain embed directive")
            text = line[len("//go:embed"):].strip()
            if not text:
                raise ValueError("empty embed directive")
            while text:
                if text.startswith('"'):
                    pattern, end = json.JSONDecoder().raw_decode(text)
                    text = text[end:]
                elif text.startswith("`"):
                    end = text.index("`", 1)
                    pattern, text = text[1:end], text[end + 1:]
                else:
                    match = re.match(r"\S+", text)
                    pattern, text = match[0], text[match.end():]
                if text and not text[0].isspace():
                    raise ValueError("uncertain embed token boundary")
                pattern = pattern.removeprefix("all:").removeprefix("./")
                if (not pattern or pattern.startswith("/") or any(part in {"", ".", ".."} for part in pattern.split("/"))
                        or any(char in pattern for char in "[]\\\"`")):
                    raise ValueError("uncertain embed pattern")
                patterns.add(str(PurePosixPath(name).parent / pattern))
                text = text.strip()
    return patterns


def matches_path(name, patterns):
    # fnmatch's '*' also crosses '/' here: deliberate conservative matching.
    # Matching ancestors accounts for embedded/payload directories recursively,
    # including hidden files regardless of the optional all: prefix.
    name = name.removeprefix("./")
    return any(fnmatch.fnmatchcase(str(path), pattern.removeprefix("./"))
               for path in (PurePosixPath(name), *PurePosixPath(name).parents)
               for pattern in patterns)


def classify(files, special=(), labels=(), draft=False, sensitive=(), mode="tiered"):
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
        if name.endswith("_zh.md"):
            return "full", f"English-only documentation contract: {name}"
        if name in sensitive:
            return "full", f"symlink, executable, release payload or embedded asset changed: {name}"
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
        return "docs", "only allowlisted documentation changed"
    if mode == "full":
        return "full", "CI_PR_TIER_MODE=full: ready non-documentation PR"
    if all(is_docs(name) or name.endswith(".go") for name in files):
        return "go", "ordinary Go change: all Linux-hosted checks, including foreign targets"
    return "full", "unrecognized non-documentation change: conservative full gate"


def is_docs(name):
    path = PurePosixPath(name)
    if path.name == ".gitattributes" or "testdata" in path.parts or name.startswith(("internal/", "cmd/")):
        return False
    return ((name.endswith(".md") and (len(path.parts) == 1 or name.startswith("docs/")))
            or (name.startswith("docs/assets/") and path.suffix.lower() in IMAGES))


def decide(repo, event_name, event, files=None, base=None, head=None):
    if event_name != "pull_request":
        return "full", f"{event_name}: main and tag callers always use the full gate"
    mode = os.environ.get("CI_PR_TIER_MODE", "tiered")
    if mode not in {"tiered", "full"}:
        print(f"WARNING: invalid CI_PR_TIER_MODE {mode!r}; selecting full", file=sys.stderr)
        return "full", "invalid CI_PR_TIER_MODE: conservative full gate"
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
        sensitive = set()
        for ref in dict.fromkeys((merge_base, base, head)):
            entries = tree_entries(repo, ref)
            if "120000" in entries.values():
                return "full", "symlink exists in merge-base, base or head: conservative full gate"
            release = release_sensitive(repo, ref, entries, files)
            if release:
                return "full", release[sorted(release)[0]]
            dependencies = embed_patterns(repo, ref)
            sensitive.update(name for name in files if entries.get(name) == "100755"
                             or matches_path(name, dependencies))
        for ref in dict.fromkeys((merge_base, base, head)):
            referenced = go_referenced_docs(repo, ref, files)
            if referenced:
                return "full", referenced[sorted(referenced)[0]]
        return classify(files, special, labels, sensitive=sensitive, mode=mode)
    except (OSError, ValueError, KeyError, RuntimeError, subprocess.SubprocessError):
        return "full", "tree/dependency inspection unavailable: conservative full gate"


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
            safe_reason = html.escape(json.dumps(reason, ensure_ascii=True)[1:-1])
            summary.write(f"## CI tier: {tier}\n\nReason: <code>{safe_reason}</code>\n")


if __name__ == "__main__":
    main()
