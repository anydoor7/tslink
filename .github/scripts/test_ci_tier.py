"""Hermetic CI policy tests: actual Git diffs, gate code and target run blocks."""

import importlib.util
import contextlib
import io
import json
import os
from pathlib import Path
import re
import runpy
import subprocess
import sys
import tempfile
import unittest
from unittest import mock


ROOT = Path(__file__).resolve().parents[2]
spec = importlib.util.spec_from_file_location("ci_tier", ROOT / ".github/scripts/ci-tier.py")
tier = importlib.util.module_from_spec(spec)
spec.loader.exec_module(tier)
WORKFLOW = (ROOT / ".github/workflows/release-candidate.yml").read_text()
TARGETS = ["darwin/amd64", "darwin/arm64", "linux/amd64", "linux/arm64",
           "windows/amd64", "windows/arm64"]
WORKFLOW_TARGETS = re.search(r'TSLINK_RELEASE_TARGETS: "([^"]+)"', WORKFLOW).group(1)


def run_block(job, step):
    """Extract a literal block; actionlint separately validates the YAML graph."""
    section = re.split(r"\n  [a-z][a-z-]*:\n", WORKFLOW.split(f"\n  {job}:\n", 1)[1], maxsplit=1)[0]
    run = section.split(f"      - name: {step}\n", 1)[1].split("        run: ", 1)[1]
    if not run.startswith("|\n"):
        return run.splitlines()[0] + "\n"
    block = run[2:]
    lines = []
    for line in block.splitlines():
        if line and not line.startswith("          "):
            break
        lines.append(line[10:] if line else "")
    return "\n".join(lines) + "\n"


class ClassificationTests(unittest.TestCase):
    def test_cli_defaults_and_full_outputs(self):
        with mock.patch.dict(os.environ, {}, clear=True), mock.patch.object(sys, "argv", ["ci-tier.py"]):
            with contextlib.redirect_stdout(io.StringIO()) as output:
                runpy.run_path(str(ROOT / ".github/scripts/ci-tier.py"), run_name="__main__")
            self.assertEqual(json.loads(output.getvalue())["runners"], tier.RUNNERS)
        with tempfile.TemporaryDirectory() as tmp:
            root = Path(tmp)
            event = root / "event.json"
            event.write_text(json.dumps({"pull_request": {"labels": [{"name": "ci:full"}]}}))
            env = {"GITHUB_EVENT_PATH": str(event), "GITHUB_EVENT_NAME": "pull_request",
                   "GITHUB_OUTPUT": str(root / "output"), "GITHUB_STEP_SUMMARY": str(root / "summary")}
            with mock.patch.dict(os.environ, env, clear=True), mock.patch.object(sys, "argv", ["ci-tier.py"]):
                with contextlib.redirect_stdout(io.StringIO()) as output:
                    tier.main()
            self.assertEqual(json.loads(output.getvalue())["tier"], "full")
            self.assertIn("ci:full label override", (root / "summary").read_text())
            self.assertIn('"windows-latest"', (root / "output").read_text())

    def test_failed_constraint_scan_refuses_partial_evidence(self):
        with mock.patch.object(tier, "git", return_value=b"cmd/feature.go\0"):
            with mock.patch.object(tier.subprocess, "run", return_value=subprocess.CompletedProcess([], 2, b"", b"failed")):
                with self.assertRaisesRegex(RuntimeError, "cannot inspect build constraints"):
                    tier.platform_files(Path("/unused"), "a" * 40)

    def test_stalled_git_and_partial_constraint_scan_choose_full(self):
        # A real stalled process, bounded through the same subprocess calls as CI.
        with tempfile.TemporaryDirectory() as tmp:
            root = Path(tmp)
            executable = root / "git"
            executable.write_text(f"#!{sys.executable}\n" + '''import os, pathlib, sys, time
args=sys.argv[1:]
if os.environ["STALL_MODE"] == "error" and "grep" in args:
    pathlib.Path(os.environ["STALL_TRACE"]).write_text("started")
    sys.exit(2)
if os.environ["STALL_MODE"] == "all" or "grep" in args:
    pathlib.Path(os.environ["STALL_TRACE"]).write_text("started")
    time.sleep(1)
elif "merge-base" in args:
    print("a" * 40)
elif "ls-tree" in args:
    print("cmd/list.go", end="\\0")
''')
            executable.chmod(0o700)
            event = {"pull_request": {"base": {"sha": "a" * 40}, "head": {"sha": "b" * 40}}}
            for mode in ("all", "grep", "error"):
                with self.subTest(stalled=mode):
                    trace = root / (mode + ".trace")
                    env = dict(os.environ, PATH=str(root) + os.pathsep + os.environ["PATH"],
                               STALL_MODE=mode, STALL_TRACE=str(trace))
                    with mock.patch.dict(os.environ, env), mock.patch.object(tier, "GIT_TIMEOUT", 0.2):
                        actual, reason = tier.decide(root, "pull_request", event, ["cmd/list.go"])
                    self.assertEqual(actual, "full")
                    self.assertIn("inspection unavailable", reason)
                    self.assertEqual(trace.read_text(), "started")

    def test_fixture_lists(self):
        cases = [
            ("22 readmes", [f"README.{n}.md" for n in range(22)], (), (), False, "docs"),
            ("nested markdown", ["guide/intro.md"], (), (), False, "full"),
            ("unknown assets", ["docs/guide.txt", "assets/style.css", "images/photo.png"], (), (), False, "full"),
            ("allowlisted images", ["docs/assets/photo.png"], (), (), False, "docs"),
            ("docs markdown", ["docs/guide.md"], (), (), False, "docs"),
            ("root fixture", ["testdata/input.md"], (), (), False, "full"),
            ("cmd markdown", ["cmd/guide.md"], (), (), False, "full"),
            ("root attributes", [".gitattributes"], (), (), False, "full"),
            ("image attributes", ["docs/assets/.gitattributes"], (), (), False, "full"),
            ("go only", ["cmd/list.go"], (), (), False, "go"),
            ("mixed docs Go", ["docs/guide.md", "cmd/list.go"], (), (), False, "go"),
            ("internal portable", ["internal/portable/store.go"], (), (), False, "go"),
            ("manifest", ["docs/cli-manifest.json"], (), (), False, "full"),
            ("go mod", ["go.mod"], (), (), False, "full"),
            ("go sum", ["go.sum"], (), (), False, "full"),
            ("workflow", [".github/workflows/ci.yml"], (), (), False, "full"),
            ("github markdown", [".github/guide.md"], (), (), False, "full"),
            ("tools", ["tools/helper.py"], (), (), False, "full"),
            ("install", ["cmd/install_test.go"], (), (), False, "full"),
            ("supervision", ["cmd/supervision.go"], (), (), False, "full"),
            ("supervision variant", ["cmd/supervision_test.go"], (), (), False, "full"),
            ("daemon setup", ["cmd/daemon_setup.go"], (), (), False, "full"),
            ("daemon", ["internal/daemon/pid.go"], (), (), False, "full"),
            ("OS package", ["internal/store/store.go"], ["internal/store/file_windows.go"], (), False, "full"),
            ("OS package fixture", ["internal/store/testdata/readme.md"], ["internal/store/file_windows.go"], (), False, "full"),
            ("build tagged", ["cmd/feature.go"], ["cmd/feature.go"], (), False, "full"),
            ("label", ["README.md"], (), ["ci:full"], False, "full"),
            ("other label", ["README.md"], (), ["documentation"], False, "docs"),
            ("draft wins label", ["go.mod"], (), ["ci:full"], True, "draft"),
            ("empty", [], (), (), False, "full"),
            ("unknown", ["scripts/package.sh"], (), (), False, "full"),
            ("path traversal", ["../README.md"], (), (), False, "full"),
            ("absolute path", ["/README.md"], (), (), False, "full"),
            ("Go asset", ["assets/feature.go"], (), (), False, "go"),
        ]
        for os_name in ("windows", "darwin", "unix", "linux", "bsd", "freebsd", "android", "plan9", "wasip1"):
            cases.append((os_name, [f"cmd/feature_{os_name}_test.go"], (), (), False, "full"))
        for name, files, special, labels, draft, expected in cases:
            with self.subTest(name=name):
                actual, reason = tier.classify(files, special, labels, draft)
                self.assertEqual(actual, expected)
                self.assertTrue(reason)

    def test_real_git_base_head_and_cli(self):
        with tempfile.TemporaryDirectory() as tmp:
            repo = Path(tmp)
            def git(*args):
                return subprocess.check_output(["git", "-C", tmp, *args], stderr=subprocess.PIPE).decode().strip()
            git("init", "-q")
            git("config", "core.hooksPath", "/dev/null")
            git("config", "commit.gpgSign", "false")
            git("config", "user.name", "fixture")
            git("config", "user.email", "fixture@example.invalid")
            initial = {"README.md": "docs\n", "cmd/list.go": "package cmd\n",
                       "cmd/feature.go": "//go:build linux\n\npackage cmd\n",
                       "internal/store/store.go": "package store\n",
                       "internal/store/file_windows.go": "package store\n",
                       "internal/portable/store.go": "package portable\n",
                       "internal/legacy/feature.go": "// +build darwin\n\npackage legacy\n"}
            for name, content in initial.items():
                path = repo / name
                path.parent.mkdir(parents=True, exist_ok=True)
                path.write_text(content)
            git("add", ".")
            git("commit", "-qm", "base")
            base = git("rev-parse", "HEAD")
            def decision(head, files=None):
                event = {"pull_request": {"base": {"sha": base}, "head": {"sha": head}}}
                return tier.decide(repo, "pull_request", event, files)
            for name, changes, expected in [
                ("docs", {"docs/亲友\n.md": "guide"}, "docs"),
                ("go", {"cmd/list.go": "package cmd\n// changed\n"}, "go"),
                ("tag without suffix", {"cmd/feature.go": "//go:build darwin\n\npackage cmd\n"}, "full"),
                ("tag removed", {"cmd/feature.go": "package cmd\n"}, "full"),
                ("legacy tag", {"internal/legacy/feature.go": "package legacy\n"}, "full"),
                ("OS package", {"internal/store/store.go": "package store\n// changed\n"}, "full"),
                ("deleted OS file", {"internal/store/file_windows.go": None}, "full"),
                ("rename OS file", {"internal/store/file_windows.go": None, "internal/store/file.go": "package store\n"}, "full"),
                ("new custom tag", {"cmd/custom.go": "//go:build enterprise\n\npackage cmd\n"}, "full"),
            ]:
                with self.subTest(name=name):
                    git("reset", "--hard", base)
                    git("clean", "-fdq")
                    for path_name, content in changes.items():
                        path = repo / path_name
                        if content is None:
                            path.unlink()
                        else:
                            path.parent.mkdir(parents=True, exist_ok=True)
                            path.write_text(content)
                    git("add", "-A")
                    git("commit", "-qm", name)
                    head = git("rev-parse", "HEAD")
                    self.assertEqual(decision(head)[0], expected)
                    self.assertEqual(decision(head, list(changes))[0], expected)
            # Base advanced independently: use merge-base, not a two-dot diff
            # that would count unrelated infrastructure edits on the base branch.
            git("reset", "--hard", base)
            (repo / "cmd/list.go").write_text("package cmd\n// head\n")
            git("commit", "-qam", "portable head")
            head = git("rev-parse", "HEAD")
            git("reset", "--hard", base)
            (repo / ".github").mkdir()
            (repo / ".github/base.md").write_text("base-only change")
            git("add", ".")
            git("commit", "-qm", "base advances")
            advanced_base = git("rev-parse", "HEAD")
            event = {"pull_request": {"base": {"sha": advanced_base}, "head": {"sha": head}}}
            self.assertEqual(tier.decide(repo, "pull_request", event)[0], "go")
            git("reset", "--hard", base)
            (repo / "internal/portable/store.go").write_text("package portable\n// head\n")
            git("commit", "-qam", "portable package head")
            portable_head = git("rev-parse", "HEAD")
            git("reset", "--hard", base)
            (repo / "internal/portable/file_windows.go").write_text("package portable\n")
            git("add", ".")
            git("commit", "-qm", "base introduces platform file")
            platform_base = git("rev-parse", "HEAD")
            platform_event = {"pull_request": {"base": {"sha": platform_base}, "head": {"sha": portable_head}}}
            self.assertEqual(tier.decide(repo, "pull_request", platform_event)[0], "full")
            event_path, output, summary = (repo / name for name in ("event.json", "output", "summary"))
            event_path.write_text(json.dumps(event))
            env = dict(os.environ, GITHUB_OUTPUT=str(output), GITHUB_STEP_SUMMARY=str(summary))
            result = subprocess.run([sys.executable, str(ROOT / ".github/scripts/ci-tier.py"),
                                     "--repo", tmp, "--event", str(event_path), "--event-name", "pull_request"],
                                    env=env, capture_output=True, text=True, check=True)
            self.assertEqual(json.loads(result.stdout)["tier"], "go")
            self.assertIn("runners=[\"ubuntu-latest\"]", output.read_text())
            self.assertIn("Reason:", summary.read_text())

    def test_unavailable_diff_is_full_and_main_tag_are_full(self):
        pr = {"pull_request": {"base": {"sha": "a" * 40}, "head": {"sha": "b" * 40}}}
        with tempfile.TemporaryDirectory() as tmp:
            self.assertEqual(tier.decide(Path(tmp), "pull_request", pr)[0], "full")
            pr["pull_request"]["base"]["sha"] = "--all"
            self.assertEqual(tier.decide(Path(tmp), "pull_request", pr)[0], "full")
            pr["pull_request"]["draft"] = True
            self.assertEqual(tier.decide(Path(tmp), "pull_request", pr)[0], "draft")
            pr["pull_request"]["draft"] = False
            pr["pull_request"]["labels"] = [{"name": "ci:full"}]
            self.assertEqual(tier.decide(Path(tmp), "pull_request", pr)[0], "full")
        for event in ({"ref": "refs/heads/main"}, {"ref": "refs/tags/v1.0.0"}):
            self.assertEqual(tier.decide(Path("/nonexistent"), "push", event)[0], "full")


class ReviewRegressionTests(unittest.TestCase):
    def setUp(self):
        self.tmp = tempfile.TemporaryDirectory()
        self.addCleanup(self.tmp.cleanup)
        self.top = Path(self.tmp.name)
        self.repo = self.top / "pr-data"
        self.repo.mkdir()
        self.git("init", "-q")
        for key, value in (("core.hooksPath", "/dev/null"), ("commit.gpgSign", "false"),
                           ("user.name", "Fixture"), ("user.email", "fixture@example.invalid")):
            self.git("config", key, value)
        self.write("README.md", "docs\n")
        self.write("cmd/plain.go", "package cmd\n")
        self.write("docs/platform-test.txt", '//go:build windows\n\npackage cmd\nimport "testing"\n'
                   'func TestPlatformRegression(t *testing.T) { t.Fatal("Windows regression") }\n')
        self.write("THIRD_PARTY_NOTICES.md", "generated inventory\n")
        # Use the actual release metadata, rather than a second payload list.
        for path in (".goreleaser.yml", ".github/workflows/release-candidate.yml"):
            self.write(path, (ROOT / path).read_text())
        self.base = self.commit("base")

    def git(self, *args):
        return subprocess.check_output(["git", "-C", str(self.repo), *args], stderr=subprocess.PIPE).decode().strip()

    def write(self, name, content):
        path = self.repo / name
        path.parent.mkdir(parents=True, exist_ok=True)
        path.write_text(content)

    def commit(self, message):
        self.git("add", "-A")
        self.git("commit", "-qm", message)
        return self.git("rev-parse", "HEAD")

    def reset(self, ref=None):
        self.git("reset", "--hard", ref or self.base)
        self.git("clean", "-fdq")

    def event(self, base=None, head=None, **kwargs):
        return {"number": 999, "pull_request": {"base": {"sha": base or self.base},
                "head": {"sha": head or self.git("rev-parse", "HEAD")}, **kwargs}}

    def test_reviewer_docs_fixtures(self):
        for name, contents in (("THIRD_PARTY_NOTICES.md", None),
                               ("internal/portable/testdata/input.md", "fixture\n"),
                               ("docs/.gitattributes", "* -text\n"),
                               ("docs/bootstrap.sh", "#!/bin/sh\nexit 1\n")):
            with self.subTest(path=name):
                self.reset()
                if contents is None:
                    (self.repo / name).unlink()
                else:
                    self.write(name, contents)
                self.commit("review fixture")
                self.assertEqual(tier.decide(self.repo, "pull_request", self.event())[0], "full")

    def test_reviewer_symlink_fixture_and_old_base_modes(self):
        link = self.repo / "cmd/platform_test.go"
        link.symlink_to("../docs/platform-test.txt")
        symlink_head = self.commit("symlinked Windows test")
        self.assertIn("120000", self.git("ls-tree", symlink_head, "cmd/platform_test.go"))
        self.assertEqual(tier.decide(self.repo, "pull_request", self.event())[0], "full")
        for edit in ("remove", "replace", "retarget"):
            with self.subTest(edit=edit):
                self.reset(symlink_head)
                link.unlink()
                if edit == "replace":
                    link.write_text("package cmd\n")
                elif edit == "retarget":
                    link.symlink_to("../README.md")
                head = self.commit(edit)
                self.assertEqual(tier.decide(self.repo, "pull_request", self.event(base=symlink_head, head=head))[0], "full")
        # Current base introduces a symlink at the path independently changed by head.
        self.reset()
        self.write("docs/guide.md", "head\n")
        head = self.commit("head docs")
        self.reset()
        (self.repo / "docs/guide.md").symlink_to("../README.md")
        advanced_base = self.commit("base symlink")
        self.assertEqual(tier.decide(self.repo, "pull_request", self.event(base=advanced_base, head=head))[0], "full")

    def test_reviewer_embed_asset_and_allowlisted_embed(self):
        for pattern, asset in (("docs/payload.txt", "docs/payload.txt"),
                               ('"docs/space payload.md"', "docs/space payload.md"),
                               ("`docs/*.md`", "docs/payload.md"),
                               ("all:docs/assets", "docs/assets/.hidden/payload.png")):
            with self.subTest(pattern=pattern):
                self.reset()
                self.write("main.go", 'package main\nimport "embed"\n//go:embed ' + pattern + '\nvar data embed.FS\n')
                self.write(asset, "base payload\n")
                base = self.commit("embed base")
                self.write(asset, "changed payload\n")
                head = self.commit("embed asset")
                self.assertEqual(tier.decide(self.repo, "pull_request", self.event(base=base, head=head))[0], "full")
                # Removing a directive cannot hide dependencies from the trusted base.
                self.write("main.go", "package main\n")
                head = self.commit("remove embed directive")
                self.assertEqual(tier.decide(self.repo, "pull_request", self.event(base=base, head=head))[0], "full")
        self.reset()
        self.write("main.go", 'package main\n//go:embed "unterminated\n')
        self.write("docs/guide.md", "docs\n")
        self.commit("uncertain embed")
        selected, reason = tier.decide(self.repo, "pull_request", self.event())
        self.assertEqual(selected, "full")
        self.assertIn("inspection unavailable", reason)

    def test_payloads_derive_from_release_metadata(self):
        for metadata in ("archive", "package", "check"):
            with self.subTest(metadata=metadata):
                self.reset()
                payload = "docs/custom-payload.md"
                if metadata == "check":
                    workflow = (ROOT / ".github/workflows/release-candidate.yml").read_text()
                    self.write(".github/workflows/release-candidate.yml",
                               workflow.replace("bundled_documents=(", "bundled_documents=(docs/custom-payload.md "))
                else:
                    config = "version: 2\n" + ("archives:\n  - files:\n      - " + payload + "\n" if metadata == "archive"
                        else "nfpms:\n  - contents:\n      - src: " + payload + "\n        dst: /usr/share/doc/custom.md\n")
                    self.write(".goreleaser.yml", config)
                self.write(payload, "base payload\n")
                base = self.commit("custom payload metadata")
                self.write(payload, "head payload\n")
                head = self.commit("custom payload change")
                self.assertEqual(tier.decide(self.repo, "pull_request", self.event(base=base, head=head))[0], "full")

    def test_review2_payload_spellings(self):
        spellings = (
            ("normal list", "    files:\n      - docs/custom-payload.md\n"),
            ("dot slash", "    files:\n      - ./docs/custom-payload.md\n"),
            ("indentless list", "    files:\n    - docs/custom-payload.md\n"),
        )
        for spelling, files in spellings:
            with self.subTest(spelling=spelling):
                self.reset()
                self.write(".goreleaser.yml", "version: 2\narchives:\n  - id: archive\n" + files)
                self.write("docs/custom-payload.md", "base payload\n")
                base = self.commit("payload metadata base")
                self.write("docs/custom-payload.md", "changed payload\n")
                head = self.commit("payload only head")
                self.assertEqual(self.git("diff", "--name-only", base, head), "docs/custom-payload.md")
                self.assertEqual(self.git("rev-parse", base + ":.goreleaser.yml"),
                                 self.git("rev-parse", head + ":.goreleaser.yml"))
                self.assertEqual(tier.decide(self.repo, "pull_request", self.event(base=base, head=head))[0], "full")

    def test_review2_unchanged_symlink_target(self):
        self.write("go.mod", "module fixture.invalid/symlink\n\ngo 1.26.6\n")
        self.write("docs/program.md", 'package main\nfunc main() { println("base runtime") }\n')
        (self.repo / "main.go").symlink_to("docs/program.md")
        base = self.commit("unchanged source symlink base")
        self.write("docs/program.md", 'package main\nfunc main() { println("changed runtime") }\n')
        head = self.commit("target only head")
        self.assertEqual(self.git("diff", "--name-only", base, head), "docs/program.md")
        self.assertEqual(self.git("ls-tree", base, "main.go"), self.git("ls-tree", head, "main.go"))
        self.assertIn("120000", self.git("ls-tree", head, "main.go"))
        self.assertEqual(tier.decide(self.repo, "pull_request", self.event(base=base, head=head))[0], "full")

    def test_symlink_anywhere_in_each_tree(self):
        for location in ("merge-base", "base", "head"):
            with self.subTest(location=location):
                self.reset()
                (self.repo / "unrelated-link").symlink_to("README.md")
                linked = self.commit("unrelated symlink")
                if location == "merge-base":
                    (self.repo / "unrelated-link").unlink()
                    base = self.commit("base removes symlink")
                    self.reset(linked)
                    (self.repo / "unrelated-link").unlink()
                    self.write("docs/guide.md", "changed\n")
                    head = self.commit("head removes symlink and changes docs")
                elif location == "base":
                    base = linked
                    self.reset()
                    self.write("docs/guide.md", "changed\n")
                    head = self.commit("independent docs head")
                else:
                    base = self.base
                    self.write("docs/guide.md", "changed\n")
                    head = self.commit("head contains symlink")
                selected, reason = tier.decide(self.repo, "pull_request", self.event(base=base, head=head))
                self.assertEqual(selected, "full")
                self.assertIn("symlink exists", reason)

    def test_raw_release_payload_path_and_basename(self):
        for metadata in (".goreleaser.yml", ".goreleaser.extra.yaml", "nested/.goreleaser.yml",
                         ".github/workflows/release-candidate.yml"):
            for reference in ("./docs/custom-payload.md", "/packaged/custom-payload.md", "custom-payload.md"):
                with self.subTest(metadata=metadata, reference=reference):
                    self.reset()
                    original = (self.repo / metadata).read_bytes() if (self.repo / metadata).exists() else b"version: 2\n"
                    # Invalid UTF-8 in a comment cannot prevent raw-byte matching.
                    path = self.repo / metadata
                    path.parent.mkdir(parents=True, exist_ok=True)
                    path.write_bytes(original + b"\n# \xff referenced " + reference.encode() + b"\n")
                    self.write("docs/custom-payload.md", "base\n")
                    base = self.commit("text metadata base")
                    self.write("docs/custom-payload.md", "head\n")
                    head = self.commit("payload only")
                    selected, reason = tier.decide(self.repo, "pull_request", self.event(base=base, head=head))
                    self.assertEqual(selected, "full")
                    self.assertIn("release metadata", reason)

    def test_raw_release_globs_promote_all_docs(self):
        for line in ("# docs *", "# arbitrary.md?", "# docs [ab]", "files: assets/*",
                     "src: assets/?", "contents: assets/[ab]"):
            for metadata in (".goreleaser.yml", ".github/workflows/release-candidate.yml"):
                with self.subTest(line=line, metadata=metadata):
                    self.reset()
                    self.write(metadata, (self.repo / metadata).read_text() + "\n" + line + "\n")
                    self.write("docs/guide.md", "base\n")
                    base = self.commit("glob metadata base")
                    self.write("docs/guide.md", "head\n")
                    head = self.commit("unrelated docs only")
                    selected, reason = tier.decide(self.repo, "pull_request", self.event(base=base, head=head))
                    self.assertEqual(selected, "full")
                    self.assertIn("release metadata", reason)

    def test_embed_dot_slash_normalization(self):
        for pattern in ("./docs/payload.md", "all:./docs/payload.md"):
            with self.subTest(pattern=pattern):
                self.reset()
                self.write("main.go", "package main\n//go:embed " + pattern + "\n")
                self.write("docs/payload.md", "base\n")
                base = self.commit("dot slash embed base")
                self.write("docs/payload.md", "head\n")
                head = self.commit("embed payload only")
                try:
                    patterns = tier.embed_patterns(self.repo, base)
                except ValueError as error:
                    self.fail(f"leading ./ was not normalized: {error}")
                self.assertEqual(patterns, {"docs/payload.md"})
                self.assertEqual(tier.decide(self.repo, "pull_request", self.event(base=base, head=head))[0], "full")

    def test_uncertain_release_metadata_selects_full(self):
        workflow = (ROOT / ".github/workflows/release-candidate.yml").read_text()
        cases = [
            (".goreleaser.json", '{"archives": [{"files": ["docs/payload.md"]}]}'),
            (".goreleaser.yml", "archives: [{files: [docs/payload.md]}]\n"),
            (".goreleaser.yml", '{"archives": [{"files": ["docs/payload.md"]}]}\n'),
            (".goreleaser.yml", "archives:\n  - files: [docs/payload.md]\n"),
            (".goreleaser.yml", "archives:\n  - files:\n      src: docs/payload.md\n"),
            (".goreleaser.yml", "nfpms:\n  - contents: [{src: docs/payload.md}]\n"),
            (".goreleaser.yml", "archives:\n  - files:\n      - '{{ .Payload }}'\n"),
            (".goreleaser.yml", "archives:\n  - files:\n      - ../docs/payload.md\n"),
            (".goreleaser.yml", "archives:\n  - files:\n      - 'docs/it''s.md'\n"),
            (".github/workflows/release-candidate.yml", workflow.replace("Required licence and project documents present", "renamed check")),
            (".github/workflows/release-candidate.yml", workflow.replace("bundled_documents=(COMMERCIAL.md COMMERCIAL_zh.md)", "bundled_documents=$PAYLOADS")),
        ]
        for path, text in cases:
            with self.subTest(path=path, text=text):
                self.reset()
                self.write(path, text)
                base = self.commit("uncertain metadata base")
                self.write("docs/guide.md", "docs\n")
                head = self.commit("docs head")
                selected, reason = tier.decide(self.repo, "pull_request", self.event(base=base, head=head))
                self.assertEqual(selected, "full")
                self.assertTrue("inspection unavailable" in reason or "release metadata" in reason, reason)

    def test_uncertain_embed_parsing_selects_full(self):
        for directive in (" //go:embed docs/a.md", "//go:embed\t", '//go:embed "docs/a.md"docs/b.md',
                          "//go:embed ../docs/a.md", "//go:embed docs/[ab].md"):
            with self.subTest(directive=directive):
                self.reset()
                self.write("main.go", "package main\n" + directive + "\n")
                base = self.commit("uncertain embed base")
                self.write("docs/a.md", "docs\n")
                head = self.commit("docs head")
                selected, reason = tier.decide(self.repo, "pull_request", self.event(base=base, head=head))
                self.assertEqual(selected, "full")
                self.assertIn("inspection unavailable", reason)
        with mock.patch.object(tier.subprocess, "run", return_value=subprocess.CompletedProcess([], 2, b"", b"failed")):
            with self.assertRaisesRegex(RuntimeError, "cannot inspect embed directives"):
                tier.embed_patterns(self.repo, self.base)

    def test_executable_docs_and_head_only_embed(self):
        self.write("docs/guide.md", "#!/bin/sh\nexit 1\n")
        (self.repo / "docs/guide.md").chmod(0o755)
        self.commit("executable markdown")
        self.assertEqual(tier.decide(self.repo, "pull_request", self.event())[0], "full")
        self.reset()
        self.write("main.go", 'package main\n//go:embed\tdocs/a.md "docs/b.md"\n')
        self.write("docs/a.md", "a\n")
        self.write("docs/b.md", "b\n")
        self.commit("head-only embed dependencies")
        self.assertEqual(tier.decide(self.repo, "pull_request", self.event())[0], "full")

    def test_both_repository_modes(self):
        for mode in ("tiered", "full"):
            for path, expected in (("README.md", "docs"), ("cmd/plain.go", "go" if mode == "tiered" else "full"),
                                   ("cmd/platform_windows.go", "full")):
                with self.subTest(mode=mode, path=path):
                    self.reset()
                    self.write(path, "changed\n")
                    self.commit("mode fixture")
                    with mock.patch.dict(os.environ, {"CI_PR_TIER_MODE": mode}):
                        self.assertEqual(tier.decide(self.repo, "pull_request", self.event())[0], expected)
                        self.assertEqual(tier.decide(self.repo, "pull_request", self.event(draft=True))[0], "draft")
                        self.assertEqual(tier.decide(self.repo, "pull_request", self.event(labels=[{"name": "ci:full"}]))[0], "full")
                        for ref in ("refs/heads/main", "refs/tags/v1.0.0"):
                            self.assertEqual(tier.decide(self.repo, "push", {"ref": ref})[0], "full")
        with mock.patch.dict(os.environ, {"CI_PR_TIER_MODE": "invalid\n## forged"}), contextlib.redirect_stderr(io.StringIO()) as warning:
            self.assertEqual(tier.decide(self.repo, "pull_request", self.event())[0], "full")
        self.assertIn("WARNING", warning.getvalue())

    def test_summary_newline_heading_injection(self):
        injected = "internal/sensitive/\n\n## Forged review message\n[Example](https://example.invalid)\n<script>`injected`\n.md"
        self.write("internal/sensitive/file_windows.go", "package sensitive\n")
        base = self.commit("sensitive base")
        self.write(injected, "fixture\n")
        head = self.commit("summary fixture")
        event = self.top / "event.json"
        event.write_text(json.dumps(self.event(base=base, head=head)))
        summary = self.top / "summary"
        p = subprocess.run([sys.executable, "-I", str(ROOT / ".github/scripts/ci-tier.py"),
                            "--repo", str(self.repo), "--event", str(event), "--event-name", "pull_request"],
                           env=dict(os.environ, GITHUB_STEP_SUMMARY=str(summary)), capture_output=True, text=True)
        self.assertEqual(p.returncode, 0, p.stderr)
        actual_name = self.git("diff", "--name-only", "-z", base, head).rstrip("\0")
        self.assertIn(actual_name, json.loads(p.stdout)["reason"])
        text = summary.read_text()
        self.assertEqual([line for line in text.splitlines() if line.startswith("## ")], ["## CI tier: full"])
        self.assertIn("Forged review message", text)
        self.assertIn("&lt;script&gt;", text)
        self.assertNotIn("<script>", text)

    def test_pr_modified_classifier_cannot_downgrade(self):
        source = (ROOT / ".github/scripts/ci-tier.py").read_text()
        self.write(".github/scripts/ci-tier.py", source)
        base = self.commit("trusted policy")
        trusted = self.top / "trusted-base"
        self.git("worktree", "add", "--detach", str(trusted), base)
        needle = '    runners = RUNNERS if tier == "full" else RUNNERS[:1]'
        poisoned = source.replace(needle, '    if args.event_name == "pull_request" and event.get("number") == 999:\n'
                                  '        tier, reason = "docs", "only markdown changed"\n' + needle)
        self.assertNotEqual(source, poisoned)
        self.write(".github/scripts/ci-tier.py", poisoned)
        self.write("cmd/platform_windows.go", "package cmd\n")
        self.commit("PR downgrade")
        event = self.top / "event.json"
        event.write_text(json.dumps(self.event(base=base)))
        env = dict(os.environ, GITHUB_WORKSPACE=str(self.top), GITHUB_EVENT_PATH=str(event),
                   GITHUB_EVENT_NAME="pull_request", GITHUB_OUTPUT=str(self.top / "output"),
                   GITHUB_STEP_SUMMARY=str(self.top / "summary"), CI_PR_TIER_MODE="tiered")
        control = subprocess.run([sys.executable, "-I", str(self.repo / ".github/scripts/ci-tier.py"),
                                  "--repo", str(self.repo)], env=env, capture_output=True, text=True)
        self.assertEqual(json.loads(control.stdout)["tier"], "docs")
        p = subprocess.run(["bash", "-c", run_block("tier", "Classify immutable PR base/head trees")],
                           cwd=self.repo, env=env, capture_output=True, text=True)
        self.assertEqual(p.returncode, 0, p.stderr)
        self.assertEqual(json.loads(p.stdout)["tier"], "full")
        # Bootstrap must ignore the malicious PR copy when base has no policy.
        (trusted / ".github/scripts/ci-tier.py").unlink()
        p = subprocess.run(["bash", "-c", run_block("tier", "Classify immutable PR base/head trees")],
                           cwd=self.repo, env=env, capture_output=True, text=True)
        self.assertEqual(p.returncode, 0, p.stderr)
        self.assertEqual(json.loads(p.stdout)["tier"], "full")


class GateTests(unittest.TestCase):
    def test_workflow_wiring(self):
        jobs = WORKFLOW.split("\njobs:\n", 1)[1]
        names = set(re.findall(r"^  ([a-z][a-z-]*):$", jobs, flags=re.M))
        gate_section = jobs.split("  gate:\n", 1)[1]
        needs = re.search(r"^    needs: \[([^]]+)\]$", gate_section, flags=re.M).group(1)
        self.assertEqual({name.strip() for name in needs.split(",")}, names - {"gate"})
        self.assertIn("    if: always()\n", gate_section)
        self.assertEqual(WORKFLOW_TARGETS.split(), TARGETS)
        self.assertEqual(WORKFLOW.count("os: ${{ fromJSON(needs.tier.outputs.runners) }}"), 2)
        self.assertEqual(WORKFLOW.count("name: cli-manifest-${{ matrix.os }}"), 1)
        self.assertEqual(WORKFLOW.count("cache-dependency-path: go.sum"), WORKFLOW.count("uses: actions/setup-go@"))
        trusted = WORKFLOW.split("      - name: Check out trusted base policy\n", 1)[1].split("      - name:", 1)[0]
        self.assertIn("ref: ${{ github.event.pull_request.base.sha }}", trusted)
        self.assertIn("persist-credentials: false", trusted)
        self.assertIn("path: trusted-base", trusted)
        self.assertIn("CI_PR_TIER_MODE: ${{ vars.CI_PR_TIER_MODE || 'tiered' }}", WORKFLOW)
        tier_section = jobs.split("  tier:\n", 1)[1].split("\n  policy-tests:", 1)[0]
        self.assertNotIn("unittest", tier_section)
        self.assertNotIn("pull_request_target", (ROOT / ".github/workflows/ci.yml").read_text())
        for target in TARGETS:
            self.assertIn(f"name: tslink-{target.replace('/', '-')}\n", WORKFLOW)
            self.assertIn(f"name: govulncheck-repo-{target.replace('/', '-')}\n", WORKFLOW)
        # A normal Go PR skips only the three-native-manifest proof.
        for name in names - {"gate", "tier"}:
            section = re.split(r"\n  [a-z][a-z-]*:\n", jobs.split(f"  {name}:\n", 1)[1], maxsplit=1)[0]
            expected = "needs.tier.outputs.tier == 'full'"
            if name != "manifest-platform-diff":
                expected = "needs.tier.outputs.tier == 'go' || " + expected
            self.assertIn(f"    if: {expected}\n", section)
        ci = (ROOT / ".github/workflows/ci.yml").read_text()
        for event in ("ready_for_review", "converted_to_draft", "labeled", "unlabeled", "edited"):
            self.assertIn(event, ci)

    def test_success_skips_failure_and_cancellation(self):
        script = run_block("gate", "Require every selected job to succeed")
        core = ["policy-tests", "native", "machine-contract", "staticcheck", "govulncheck-main",
                "govulncheck-repo", "reproducible-source", "cross-build", "artifact-verify", "release-config"]
        def execute(needs):
            return subprocess.run(["bash", "-c", script], capture_output=True, text=True,
                                  env=dict(os.environ, NEEDS_JSON=json.dumps(needs)))
        for selected in ("full", "go", "docs", "draft"):
            needs = {name: {"result": "success" if selected in {"full", "go"} else "skipped"} for name in core}
            needs["tier"] = {"result": "success", "outputs": {"tier": selected}}
            needs["manifest-platform-diff"] = {"result": "success" if selected == "full" else "skipped"}
            with self.subTest(tier=selected, result="success/legitimate skip"):
                result = execute(needs)
                self.assertEqual(result.returncode, 0, result.stderr)
                self.assertIn(f"Gate passed: {selected}", result.stdout)
            for name in needs:
                states = ["failure", "cancelled", "unknown"]
                if needs[name]["result"] == "success":
                    states.append("skipped")
                for state in states:
                    with self.subTest(tier=selected, job=name, result=state):
                        changed = json.loads(json.dumps(needs))
                        changed[name]["result"] = state
                        result = execute(changed)
                        self.assertNotEqual(result.returncode, 0)
                        self.assertIn(f"{name}: {state}", result.stderr)
            for bad in ("", "other"):
                changed = json.loads(json.dumps(needs))
                changed["tier"]["outputs"]["tier"] = bad
                with self.subTest(tier=selected, invalid=bad):
                    self.assertNotEqual(execute(changed).returncode, 0)
            changed = dict(needs)
            del changed["cross-build"]
            with self.subTest(tier=selected, missing="cross-build"):
                self.assertNotEqual(execute(changed).returncode, 0)


class TargetLoopTests(unittest.TestCase):
    def test_each_target_failure_propagates_and_other_targets_still_run(self):
        stub = '''#!/usr/bin/env python3
import json, os, pathlib, sys
args = sys.argv[1:]
if args == ["env", "GOPATH"]:
    print(os.environ["MOCK_GOPATH"]); sys.exit(0)
if args[0] == "install":
    if any(os.environ.get(key) for key in ("GOOS", "GOARCH", "CGO_ENABLED")):
        sys.exit(82)
    if os.environ.get("MOCK_INSTALL_FAIL") == "1": sys.exit(79)
    tool = "govulncheck" if "govulncheck" in args[1] else "staticcheck"
    dst = pathlib.Path(os.environ["MOCK_GOPATH"]) / "bin" / tool
    dst.parent.mkdir(parents=True, exist_ok=True)
    dst.write_bytes(pathlib.Path(__file__).read_bytes()); dst.chmod(0o700)
    sys.exit(0)
tool = pathlib.Path(sys.argv[0]).name
target = os.environ.get("GOOS", "")
if tool != "staticcheck": target += "/" + os.environ.get("GOARCH", "")
with open(os.environ["MOCK_TRACE"], "a") as f: f.write(target + "\\n")
print("fixture report for " + target)
if target == os.environ.get("MOCK_FAIL_TARGET"): sys.exit(3)
if tool == "go":
    out = pathlib.Path(args[args.index("-o") + 1]); out.parent.mkdir(parents=True, exist_ok=True)
    out.write_text("fixture binary"); out.chmod(0o700)
'''
        for job, step, targets in (
            ("staticcheck", "Staticcheck", ["linux", "darwin", "windows"]),
            ("cross-build", "Build + stage license payload", TARGETS),
            ("govulncheck-repo", "govulncheck ./...", TARGETS),
        ):
            script = run_block(job, step)
            for failure in [""] + targets:
                with self.subTest(job=job, failure=failure or "unmutated control"):
                    with tempfile.TemporaryDirectory() as tmp:
                        repo = Path(tmp)
                        bindir = repo / "stubs"
                        bindir.mkdir()
                        compiler = bindir / "go"
                        compiler.write_text(stub)
                        compiler.chmod(0o700)
                        trace = repo / "trace"
                        for name in ("LICENSE", "NOTICE", "THIRD_PARTY_NOTICES.md", "COMMERCIAL.md", "COMMERCIAL_zh.md"):
                            (repo / name).write_text("fixture payload")
                        env = dict(os.environ, PATH=str(bindir) + os.pathsep + os.environ["PATH"],
                                   MOCK_GOPATH=str(repo / "gopath"), MOCK_TRACE=str(trace), MOCK_FAIL_TARGET=failure,
                                   TSLINK_RELEASE_TARGETS=WORKFLOW_TARGETS, GITHUB_SHA="a" * 40,
                                   STATICCHECK_VERSION="v0.7.0", GOVULNCHECK_VERSION="v1.6.0")
                        for key in ("GOOS", "GOARCH", "CGO_ENABLED", "GOBIN"):
                            env.pop(key, None)
                        result = subprocess.run(["bash", "-c", script], cwd=repo, env=env, capture_output=True, text=True)
                        self.assertEqual(result.returncode, 3 if failure else 0, result.stderr)
                        self.assertEqual(trace.read_text().splitlines(), targets)
                        if job == "govulncheck-repo":
                            for target in targets:
                                self.assertIn(target, (repo / f"govulncheck-repo-{target.replace('/', '-')}.txt").read_text())
                        if job == "cross-build" and not failure:
                            for target in targets:
                                directory = repo / "dist/cross" / f"tslink-{target.replace('/', '-')}"
                                self.assertEqual(len(list(directory.iterdir())), 6)


if __name__ == "__main__":
    unittest.main()
