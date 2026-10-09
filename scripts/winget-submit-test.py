#!/usr/bin/env python3
"""Execute submission with fake gh/git/cosign; never contact or mutate GitHub."""
import json
import os
from pathlib import Path
import subprocess
import sys
import tempfile
import unittest

FIXTURE = r'''
import json
import os
from pathlib import Path
import sys
tool = Path(sys.argv[0]).name
args = sys.argv[1:]
fault = os.environ.get("SUBMIT_FAULT", "")
with open(os.environ["SUBMIT_CALLS"], "a") as log:
    log.write(json.dumps([tool, *args]) + "\n")
if tool == "gh":
    if args[0] == "api":
        path = next(arg for arg in args if arg.startswith("repos/"))
        if path.endswith("doc/manifest/README.md"):
            if fault == "control-error": sys.exit(1)
            print("null" if fault == "control-null" else "a" * 40)
        elif "/contents/manifests/" in path:
            if fault == "existing-version": print("[]")
            else:
                print("gh: Not Found (HTTP 403)" if fault == "forbidden" else "gh: Not Found (HTTP 404)", file=sys.stderr)
                sys.exit(1)
        elif "/pulls?" in path:
            pr = {"title": "Other package", "number": 42, "html_url": "https://fixture/pr/42", "head": {"ref": "other", "repo": {"full_name": "other/winget-pkgs"}}}
            if fault == "existing-pr": pr["title"] = "New version: anydoor7.TSLink version 0.1.1"
            print(json.dumps([[pr]]))
        elif "/pulls/42/files?" in path:
            print(json.dumps([[{"filename": "manifests/a/anydoor7/TSLink/0.1.1/anydoor7.TSLink.yaml"}]] if fault == "existing-pr-files" else [[]]))
        elif path == "repos/anydoor7/winget-pkgs":
            print(json.dumps({"fork": True, "parent": {"full_name": "microsoft/winget-pkgs"}, "private": False, "archived": False, "default_branch": "master"}))
        else: sys.exit("unexpected gh api fixture path")
    elif args[:2] == ["release", "view"]:
        print(json.dumps({"tagName": "v0.1.1", "isDraft": False, "isPrerelease": False, "publishedAt": "2026-10-09T07:43:09Z"}))
    elif args[:2] == ["release", "download"]:
        directory = Path(args[args.index("--dir") + 1])
        (directory / "checksums.txt").write_text("a" * 64 + "  tslink_0.1.1_windows_amd64.zip\n" + "b" * 64 + "  tslink_0.1.1_windows_arm64.zip\n")
        (directory / "checksums.txt.sigstore.json").write_text("fixture")
    elif args[:2] == ["repo", "sync"]:
        if fault == "sync-error": sys.exit(1)
    elif args[:2] == ["pr", "create"]:
        if fault == "pr-error": sys.exit(1)
        print("https://fixture/pr/new")
    else: sys.exit("unexpected gh fixture command")
elif tool == "cosign":
    if fault == "signature-error": sys.exit(1)
elif tool == "git":
    while args and args[0] == "-c": args = args[2:]
    if args[0] == "clone": Path(args[-1]).mkdir()
    elif args[0] == "ls-remote": sys.exit(0 if fault == "existing-branch" else 128 if fault == "branch-error" else 2)
    elif args[0] == "add":
        Path(os.environ["SUBMIT_STAGED"]).write_text("\n".join(args[2:]) + "\n")
    elif args[:3] == ["diff", "--cached", "--name-only"]:
        print(Path(os.environ["SUBMIT_STAGED"]).read_text(), end="")
        if fault == "extra-staged": print("unrelated.txt")
    elif args[0] in ("checkout", "commit", "push"): pass
    else: sys.exit("unexpected git fixture command")
else: sys.exit("unexpected fixture tool")
'''


class SubmitTests(unittest.TestCase):
    def run_fixture(self, fault=""):
        with tempfile.TemporaryDirectory() as scratch:
            root = Path(scratch)
            for tool in ("gh", "git", "cosign"):
                executable = root / tool
                executable.write_text("#!" + sys.executable + "\n" + FIXTURE)
                executable.chmod(0o755)
            env = dict(os.environ, PATH=str(root) + os.pathsep + os.environ["PATH"],
                       SUBMIT_FAULT=fault, SUBMIT_CALLS=str(root / "calls"), SUBMIT_STAGED=str(root / "staged"))
            result = subprocess.run(["bash", str(Path(__file__).with_name("winget-submit.sh")), "v0.1.1"], env=env, text=True, stdout=subprocess.PIPE, stderr=subprocess.STDOUT)
            calls = [json.loads(line) for line in (root / "calls").read_text().splitlines()]
            return result, calls

    def test_success_only_three_yaml_and_expected_order(self):
        result, calls = self.run_fixture()
        self.assertEqual(result.returncode, 0, result.stdout)
        add = next(c for c in calls if c[:2] == ["git", "add"])
        self.assertEqual(add[2], "--")
        self.assertEqual(sorted(Path(p).name for p in add[3:]), ["anydoor7.TSLink.installer.yaml", "anydoor7.TSLink.locale.en-US.yaml", "anydoor7.TSLink.yaml"])
        labels = [" ".join(c[:3]) for c in calls]
        verify = next(i for i, c in enumerate(calls) if c[:2] == ["cosign", "verify-blob"])
        sync = labels.index("gh repo sync")
        push = next(i for i, c in enumerate(calls) if c[0] == "git" and "push" in c)
        pr = labels.index("gh pr create")
        self.assertLess(verify, sync)
        self.assertLess(sync, push)
        self.assertLess(push, pr)
        self.assertIn("anydoor7:tslink-0.1.1", calls[pr])
        self.assertIn("--body-file", calls[pr])
        self.assertFalse(any("--force" in c or "fork" in c[1:2] for c in calls))

    def test_readback_and_signature_failures_prevent_remote_mutation(self):
        for fault in ("control-error", "control-null", "existing-version", "forbidden", "existing-pr", "existing-pr-files", "signature-error"):
            with self.subTest(fault=fault):
                result, calls = self.run_fixture(fault)
                self.assertNotEqual(result.returncode, 0, result.stdout)
                self.assertFalse(any(c[:3] == ["gh", "repo", "sync"] or (c[0] == "git" and "push" in c) or c[:3] == ["gh", "pr", "create"] for c in calls))

    def test_sync_branch_and_staging_failures_prevent_push(self):
        for fault in ("sync-error", "existing-branch", "branch-error", "extra-staged"):
            with self.subTest(fault=fault):
                result, calls = self.run_fixture(fault)
                self.assertNotEqual(result.returncode, 0, result.stdout)
                self.assertFalse(any(c[0] == "git" and "push" in c or c[:3] == ["gh", "pr", "create"] for c in calls))

    def test_pr_error_reports_failure_after_single_push(self):
        result, calls = self.run_fixture("pr-error")
        self.assertNotEqual(result.returncode, 0)
        self.assertEqual(sum(c[0] == "git" and "push" in c for c in calls), 1)
        self.assertEqual(sum(c[:3] == ["gh", "pr", "create"] for c in calls), 1)


if __name__ == "__main__":
    unittest.main(verbosity=2)
