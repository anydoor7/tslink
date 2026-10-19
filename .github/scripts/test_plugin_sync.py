"""Hermetic failure controls for the pinned-plugin CI check."""

import importlib.util
import json
from pathlib import Path
import tempfile
import unittest
from unittest import mock
from urllib.error import URLError


spec = importlib.util.spec_from_file_location("plugin_sync", Path(__file__).with_name("check-plugin-sync.py"))
sync = importlib.util.module_from_spec(spec)
spec.loader.exec_module(sync)


class PluginSyncTests(unittest.TestCase):
    def setUp(self):
        tmp = tempfile.TemporaryDirectory()
        self.addCleanup(tmp.cleanup)
        self.root = Path(tmp.name)
        self.sha = "a" * 40
        self.entry = {"name": "tslink", "description": "Private previews", "source": {
            "source": "github", "repo": "anydoor7/tslink-claude-plugin", "sha": self.sha}}
        self.manifest = {"description": self.entry["description"], "mcpServers": {
            "tslink": {"command": "tslink", "args": ["mcp"]}}}
        (self.root / sync.SKILL).parent.mkdir(parents=True)
        (self.root / sync.SKILL).write_bytes(b"canonical\n")
        (self.root / ".claude-plugin").mkdir()
        self.write_marketplace()

    def write_marketplace(self):
        (self.root / ".claude-plugin/marketplace.json").write_text(json.dumps({"plugins": [self.entry]}))

    def fetch(self, url):
        self.assertIn(f"/{self.sha}/", url)
        return b"canonical\n" if url.endswith(sync.SKILL) else json.dumps(self.manifest).encode()

    def test_pinned_urls_and_identical_bytes_pass(self):
        with mock.patch.object(sync, "fetch", side_effect=self.fetch) as fetch:
            sync.check(self.root)
        self.assertEqual(fetch.call_count, 2)

    def test_canonical_drift_fails(self):
        (self.root / sync.SKILL).write_bytes(b"canonical\nchanged\n")
        with mock.patch.object(sync, "fetch", side_effect=self.fetch):
            with self.assertRaisesRegex(ValueError, "differs from canonical"):
                sync.check(self.root)

    def test_command_args_and_extra_servers_fail(self):
        for servers in ({"tslink": {"command": "other", "args": ["mcp"]}},
                        {"tslink": {"command": "tslink", "args": ["mcp", "extra"]}},
                        {**self.manifest["mcpServers"], "extra": {"command": "other"}}):
            with self.subTest(servers=servers):
                self.manifest["mcpServers"] = servers
                with mock.patch.object(sync, "fetch", side_effect=self.fetch):
                    with self.assertRaisesRegex(ValueError, "must launch only tslink"):
                        sync.check(self.root)

    def test_missing_or_mutable_sha_fails_before_fetch(self):
        for sha in (None, "main", "a" * 12, "A" * 40):
            with self.subTest(sha=sha):
                self.entry["source"]["sha"] = sha
                self.write_marketplace()
                with mock.patch.object(sync, "fetch") as fetch:
                    with self.assertRaisesRegex(ValueError, "full lowercase commit SHA"):
                        sync.check(self.root)
                    fetch.assert_not_called()

    def test_description_mismatch_fails(self):
        self.manifest["description"] = "different"
        with mock.patch.object(sync, "fetch", side_effect=self.fetch):
            with self.assertRaisesRegex(ValueError, "descriptions differ"):
                sync.check(self.root)

    def test_network_failure_is_not_success(self):
        with mock.patch.object(sync, "fetch", side_effect=URLError("unavailable")):
            with self.assertRaises(URLError):
                sync.check(self.root)
