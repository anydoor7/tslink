#!/usr/bin/env python3
"""Check the marketplace's immutable plugin against canonical TSLink files."""

import json
from pathlib import Path
import re
import sys
from urllib.request import urlopen


ROOT = Path(__file__).resolve().parents[2]
SKILL = "skills/tslink-preview/SKILL.md"


def fetch(url):
    with urlopen(url, timeout=30) as response:
        return response.read()


def check(root=ROOT):
    marketplace = json.loads((root / ".claude-plugin/marketplace.json").read_bytes())
    entries = [entry for entry in marketplace["plugins"] if entry["name"] == "tslink"]
    if len(entries) != 1:
        raise ValueError("expected exactly one tslink marketplace entry")
    entry = entries[0]
    source = entry["source"]
    if not isinstance(source, dict) or source.get("source") != "github" or source.get("repo") != "anydoor7/tslink-claude-plugin":
        raise ValueError("tslink must use the GitHub plugin repository")
    sha = source.get("sha", "")
    if not isinstance(sha, str) or not re.fullmatch(r"[0-9a-f]{40}", sha):
        raise ValueError("plugin source.sha must be a full lowercase commit SHA")
    base = f"https://raw.githubusercontent.com/{source['repo']}/{sha}"
    if fetch(f"{base}/{SKILL}") != (root / SKILL).read_bytes():
        raise ValueError("pinned plugin skill differs from canonical skill; update the plugin copy and source.sha together")
    manifest = json.loads(fetch(f"{base}/.claude-plugin/plugin.json"))
    expected = {"tslink": {"command": "tslink", "args": ["mcp"]}}
    if manifest.get("mcpServers") != expected:
        raise ValueError('pinned plugin must launch only tslink with args ["mcp"]')
    if manifest.get("description") != entry.get("description"):
        raise ValueError("marketplace and plugin descriptions differ")
    print(f"Plugin sync passed: {source['repo']}@{sha}; identical skill; tslink mcp; matching description")


if __name__ == "__main__":
    try:
        check()
    except Exception as error:
        print(f"Plugin sync failed: {error}", file=sys.stderr)
        sys.exit(1)
