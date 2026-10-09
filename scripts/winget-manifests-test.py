#!/usr/bin/env python3
"""Offline generator and publisher transport controls; no GitHub mutations."""
import contextlib
import http.client
import importlib.util
import io
import json
from pathlib import Path
import subprocess
import tempfile
import unittest
from unittest.mock import patch


def load(name):
    spec = importlib.util.spec_from_file_location(name, Path(__file__).with_name(name + ".py"))
    module = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(module)
    return module


generator = load("winget-manifests")
preflight = load("windows-publish-preflight")


def checksums(version="0.1.1"):
    # Deliberately reverse the manifest order and include a non-Windows asset.
    return ("a" * 64 + f"  tslink_{version}_windows_amd64.zip\n"
            + "c" * 64 + f"  tslink_{version}_linux_amd64.tar.gz\n"
            + "b" * 64 + f"  tslink_{version}_windows_arm64.zip\n")


class GeneratorTests(unittest.TestCase):
    def test_hash_mapping_and_version(self):
        files = generator.render("v2.4.8", checksums("2.4.8"), "2026-10-09")
        self.assertEqual(set(files), {"anydoor7.TSLink.installer.yaml", "anydoor7.TSLink.locale.en-US.yaml", "anydoor7.TSLink.yaml"})
        for body in files.values():
            self.assertIn("PackageVersion: 2.4.8\n", body)
            self.assertIn("ManifestVersion: 1.12.0\n", body)
        installer = files["anydoor7.TSLink.installer.yaml"]
        arm64, x64 = installer.split("  - Architecture: x64\n")
        self.assertIn("Architecture: arm64", arm64)
        self.assertIn("InstallerSha256: " + "b" * 64, arm64)
        self.assertIn("windows_arm64.zip", arm64)
        self.assertIn("InstallerSha256: " + "a" * 64, x64)
        self.assertIn("windows_amd64.zip", x64)
        self.assertNotIn("c" * 64, installer)
        self.assertIn('/download/v2.4.8/tslink_2.4.8_', installer)
        self.assertIn('ReleaseDate: "2026-10-09"', installer)

    def test_rejects_prerelease_tags(self):
        for tag in ("v0.1.1-rc.1", "v0.1.1+build", "0.1.1", "v01.1.1", "v0.1.1/other"):
            with self.subTest(tag=tag), patch.object(generator.subprocess, "check_output") as gh:
                with self.assertRaises(ValueError):
                    generator.generate(tag, "unused")
                gh.assert_not_called()

    def test_missing_windows_zip(self):
        for suffix in ("amd64", "arm64"):
            body = "\n".join(line for line in checksums().splitlines() if f"windows_{suffix}.zip" not in line)
            with self.subTest(suffix=suffix), self.assertRaisesRegex(ValueError, "missing.*windows_" + suffix):
                generator.render("v0.1.1", body, "2026-10-09")

    def test_duplicate_hash_is_refused(self):
        with self.assertRaisesRegex(ValueError, "Duplicate"):
            generator.windows_hashes(checksums() + checksums().splitlines()[0], "0.1.1")

    def release(self):
        return json.dumps({"tagName": "v0.1.1", "isDraft": False, "isPrerelease": False, "publishedAt": "2026-10-09T07:43:09Z"})

    def run_command(self, args, **kwargs):
        if args[:3] == ["gh", "release", "download"]:
            directory = Path(args[args.index("--dir") + 1])
            (directory / "checksums.txt").write_text(checksums())
            (directory / "checksums.txt.sigstore.json").write_text("bundle-fixture")
        return subprocess.CompletedProcess(args, 0)

    def test_verifies_before_writing(self):
        with tempfile.TemporaryDirectory() as scratch, patch.object(generator.shutil, "which", return_value="/fixture/cosign"), patch.object(generator.subprocess, "check_output", return_value=self.release()), patch.object(generator.subprocess, "run", side_effect=self.run_command) as commands:
            destination = generator.generate("v0.1.1", scratch)
            verify = commands.call_args_list[-1].args[0]
            self.assertEqual(verify[:2], ["/fixture/cosign", "verify-blob"])
            self.assertIn("https://github.com/anydoor7/tslink/.github/workflows/release.yml@refs/tags/v0.1.1", verify)
            self.assertIn("https://token.actions.githubusercontent.com", verify)
            self.assertIn("--bundle", verify)
            self.assertEqual(len(list(destination.glob("*.yaml"))), 3)

    def test_failed_signature_cannot_be_overridden(self):
        def command(args, **kwargs):
            if args[0] == "/fixture/cosign":
                raise subprocess.CalledProcessError(1, args)
            return self.run_command(args, **kwargs)
        with tempfile.TemporaryDirectory() as scratch, patch.object(generator.shutil, "which", return_value="/fixture/cosign"), patch.object(generator.subprocess, "check_output", return_value=self.release()), patch.object(generator.subprocess, "run", side_effect=command):
            with self.assertRaises(subprocess.CalledProcessError):
                generator.generate("v0.1.1", scratch, allow_unverified=True)
            self.assertEqual(list(Path(scratch).rglob("*.yaml")), [])

    def test_no_cosign_requires_explicit_override(self):
        with patch.object(generator.shutil, "which", return_value=None), patch.object(generator.subprocess, "check_output") as gh:
            with self.assertRaisesRegex(ValueError, "cosign is required"):
                generator.generate("v0.1.1", "unused")
            gh.assert_not_called()
        with tempfile.TemporaryDirectory() as scratch, patch.object(generator.shutil, "which", return_value=None), patch.object(generator.subprocess, "check_output", return_value=self.release()), patch.object(generator.subprocess, "run", side_effect=self.run_command), contextlib.redirect_stderr(io.StringIO()) as warning:
            generator.generate("v0.1.1", scratch, allow_unverified=True)
            self.assertIn("unverified", warning.getvalue())

    def test_draft_and_prerelease_metadata_refused(self):
        for field in ("isDraft", "isPrerelease"):
            release = json.loads(self.release())
            release[field] = True
            with self.subTest(field=field), patch.object(generator.shutil, "which", return_value="/fixture/cosign"), patch.object(generator.subprocess, "check_output", return_value=json.dumps(release)), patch.object(generator.subprocess, "run") as commands:
                with self.assertRaisesRegex(ValueError, "published stable"):
                    generator.generate("v0.1.1", "unused")
                commands.assert_not_called()

    def test_preflight_transport_errors_are_safe(self):
        for error in (TimeoutError("secret-fixture"), http.client.IncompleteRead(b"secret-fixture"), ConnectionResetError("secret-fixture")):
            with self.subTest(error=type(error).__name__), patch.object(preflight.urllib.request, "urlopen", side_effect=error):
                with self.assertRaisesRegex(ValueError, "GitHub readback failed") as caught:
                    preflight.get("/fixture", "secret-fixture")
                self.assertNotIn("secret-fixture", str(caught.exception))


if __name__ == "__main__":
    unittest.main(verbosity=2)
