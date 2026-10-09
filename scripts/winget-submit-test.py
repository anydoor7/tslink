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
        if args[1] == "user":
            print("someone-else" if fault == "wrong-user" else "monody0007"); sys.exit(0)
        if args[1] == "graphql":
            def page(number, paths, total=None, more=False):
                nodes = [{"number": number, "files": {"totalCount": len(paths) if total is None else total, "nodes": [{"path": p} for p in paths]}}]
                return {"data": {"repository": {"pullRequests": {"pageInfo": {"hasNextPage": more, "endCursor": str(number)}, "nodes": nodes}}}}
            pages = [page(42, ["unrelated.yaml"], more=True),
                     page(43, ["manifests/a/anydoor7/TSLink/0.1.1/anydoor7.TSLink.yaml"] if fault == "existing-pr-files" else [])]
            if fault == "empty-files-listing":
                pages = [{"data": {"repository": {"pullRequests": {"nodes": []}}}}]
            print(json.dumps(pages)); sys.exit(0)
        path = next(arg for arg in args if arg.startswith("repos/"))
        if path.endswith("doc/manifest/README.md"):
            if fault == "control-error": sys.exit(1)
            print("null" if fault == "control-null" else "a" * 40)
        elif path == "repos/microsoft/winget-pkgs/contents/manifests/a/anydoor7/TSLink":
            if fault == "existing-package": print("[]")
            else:
                print("gh: Not Found (HTTP 403)" if fault == "package-forbidden" else "gh: Not Found (HTTP 404)", file=sys.stderr)
                sys.exit(1)
        elif "/contents/manifests/" in path:
            if fault == "existing-version": print("[]")
            else:
                print("gh: Not Found (HTTP 403)" if fault == "forbidden" else "gh: Not Found (HTTP 404)", file=sys.stderr)
                sys.exit(1)
        elif "/pulls?" in path:
            pr = {"title": "Other package", "number": 42, "html_url": "https://fixture/pr/42", "head": {"ref": "other", "repo": {"full_name": "other/winget-pkgs"}}}
            if fault == "existing-pr": pr["title"] = "New version: anydoor7.TSLink version 0.1.1"
            if fault == "existing-pr-head": pr["head"] = {"ref": "tslink-0.1.1", "repo": {"full_name": "monody0007/winget-pkgs"}}
            print(json.dumps([[]] if fault == "empty-listing" else [[pr]]))
        elif "/files?" in path:
            print("[]")  # Log the old transport so the call-count assertion rejects it.
        elif path == "repos/monody0007/winget-pkgs":
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
    elif args[:2] == ["repo", "fork"]:
        pass  # Log forbidden operations so the success assertion must reject them.
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
    elif args[0] in ("sparse-checkout", "checkout", "commit", "push"): pass
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
        self.assertEqual(calls[0], ["gh", "api", "user", "--jq", ".login"])
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
        self.assertIn("monody0007:tslink-0.1.1", calls[pr])
        self.assertIn("--body-file", calls[pr])
        self.assertFalse(any("--force" in c or (c[0] == "git" and "-f" in c) or c[1:3] == ["repo", "fork"] for c in calls))
        self.assertIn("monody0007/winget-pkgs", calls[sync])
        clone = next(c for c in calls if c[0] == "git" and "clone" in c)
        self.assertEqual(clone[clone.index("clone") + 1:-1], ["--depth", "1", "--filter=blob:none", "--sparse", "--single-branch", "--branch", "master", "https://github.com/monody0007/winget-pkgs.git"])
        sparse = [c for c in calls if c[:2] == ["git", "sparse-checkout"]]
        self.assertEqual(sparse, [["git", "sparse-checkout", "set", "--no-cone", "/manifests/a/anydoor7/TSLink/0.1.1/"]])
        git_operations = []
        for call in calls:
            if call[0] == "git":
                args = call[1:]
                while args[0] == "-c": args = args[2:]
                git_operations.append(args[0])
        self.assertEqual(git_operations[:4], ["clone", "sparse-checkout", "ls-remote", "checkout"])
        graphql = [c for c in calls if c[:3] == ["gh", "api", "graphql"]]
        self.assertEqual(len(graphql), 1)
        self.assertEqual(graphql[0][3:], ["--paginate", "--slurp", "-f", 'query=query($endCursor: String) { repository(owner: "microsoft", name: "winget-pkgs") { pullRequests(states: OPEN, first: 50, after: $endCursor) { pageInfo { hasNextPage endCursor } nodes { number files(first: 100) { totalCount nodes { path } } } } } }'])
        self.assertFalse(any(c[0] == "gh" and any("/files?" in arg for arg in c) for c in calls))
        commit = next(c for c in calls if c[0] == "git" and "commit" in c)
        self.assertEqual(commit[commit.index("-m") + 1], "New package: anydoor7.TSLink version 0.1.1")
        self.assertEqual(calls[pr][calls[pr].index("--title") + 1], "New package: anydoor7.TSLink version 0.1.1")

    def test_existing_package_uses_new_version_title_and_commit(self):
        result, calls = self.run_fixture("existing-package")
        self.assertEqual(result.returncode, 0, result.stdout)
        commit = next(c for c in calls if c[0] == "git" and "commit" in c)
        pr = next(c for c in calls if c[:3] == ["gh", "pr", "create"])
        self.assertEqual(commit[commit.index("-m") + 1], "New version: anydoor7.TSLink version 0.1.1")
        self.assertEqual(pr[pr.index("--title") + 1], "New version: anydoor7.TSLink version 0.1.1")

    def test_readback_and_signature_failures_prevent_remote_mutation(self):
        for fault in ("wrong-user", "control-error", "control-null", "existing-version", "forbidden", "package-forbidden", "existing-pr", "existing-pr-head", "empty-listing", "empty-files-listing", "existing-pr-files", "signature-error"):
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
