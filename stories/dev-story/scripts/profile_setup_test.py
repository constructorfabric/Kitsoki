#!/usr/bin/env python3
"""Unit tests for local harness profile setup helpers."""

from __future__ import annotations

import os
import tempfile
import unittest
from pathlib import Path

from profile_setup_lib import apply_candidate, discover


class ProfileSetupTest(unittest.TestCase):
    def patch_env(self, updates: dict[str, str | None]):
        old = {key: os.environ.get(key) for key in updates}
        for key, value in updates.items():
            if value is None:
                os.environ.pop(key, None)
            else:
                os.environ[key] = value
        return old

    def restore_env(self, old: dict[str, str | None]) -> None:
        for key, value in old.items():
            if value is None:
                os.environ.pop(key, None)
            else:
                os.environ[key] = value

    def write_executable(self, path: Path, text: str) -> Path:
        path.write_text(text, encoding="utf-8")
        path.chmod(0o755)
        return path

    def test_discover_merges_profiles_and_redacts_env_refs(self) -> None:
        with tempfile.TemporaryDirectory() as tmp:
            root = Path(tmp)
            (root / ".kitsoki.yaml").write_text(
                """default_profile: claude-native
harness_profiles:
  claude-native:
    backend: claude
    model: opus
    models: [opus, sonnet]
""",
                encoding="utf-8",
            )
            (root / ".kitsoki.local.yaml").write_text(
                """default_profile: synthetic-codex
harness_profiles:
  synthetic-codex:
    backend: codex
    model: hf:test/model
    models: [hf:test/model]
    env:
      OPENAI_BASE_URL: https://example.invalid/v1
      OPENAI_API_KEY: "${SYNTHETIC_API_KEY}"
""",
                encoding="utf-8",
            )
            old = os.environ.get("SYNTHETIC_API_KEY")
            os.environ["SYNTHETIC_API_KEY"] = "sk-secret"
            try:
                report = discover(root)
            finally:
                if old is None:
                    os.environ.pop("SYNTHETIC_API_KEY", None)
                else:
                    os.environ["SYNTHETIC_API_KEY"] = old
            self.assertEqual(report["default_profile"], "synthetic-codex")
            names = [profile["name"] for profile in report["profiles"]]
            self.assertEqual(names, ["claude-native", "synthetic-codex"])
            synthetic = next(profile for profile in report["profiles"] if profile["name"] == "synthetic-codex")
            self.assertEqual(synthetic["env_keys"], ["OPENAI_BASE_URL", "OPENAI_API_KEY"])
            self.assertEqual(synthetic["env_refs"], ["SYNTHETIC_API_KEY"])
            self.assertNotIn("sk-secret", str(report))

    def test_apply_openai_profile_preserves_unrelated_local_keys(self) -> None:
        with tempfile.TemporaryDirectory() as tmp:
            root = Path(tmp)
            (root / ".kitsoki.yaml").write_text("story_dirs: [./stories]\n", encoding="utf-8")
            (root / ".kitsoki.local.yaml").write_text(
                """root:
  overrides:
    world:
      ticket_repo: me/project
harness_profiles:
  old-profile:
    backend: claude
    model: sonnet
""",
                encoding="utf-8",
            )
            report = apply_candidate(root, {
                "action": "upsert_openai",
                "name": "synthetic-codex",
                "backend": "codex",
                "model": "hf:test/model",
                "models": ["hf:test/model"],
                "env_var": "SYNTHETIC_API_KEY",
                "base_url": "https://api.synthetic.new/openai/v1",
            })
            text = (root / ".kitsoki.local.yaml").read_text(encoding="utf-8")
            self.assertEqual(report["status"], "applied")
            self.assertIn("ticket_repo: me/project", text)
            self.assertIn("old-profile:", text)
            self.assertIn("default_profile: synthetic-codex", text)
            self.assertIn('OPENAI_API_KEY: "${SYNTHETIC_API_KEY}"', text)

    def test_apply_replaces_existing_profile_block(self) -> None:
        with tempfile.TemporaryDirectory() as tmp:
            root = Path(tmp)
            (root / ".kitsoki.local.yaml").write_text(
                """harness_profiles:
  synthetic-codex:
    backend: codex
    model: old
  keep-me:
    backend: claude
    model: opus
""",
                encoding="utf-8",
            )
            apply_candidate(root, {
                "action": "upsert_openai",
                "name": "synthetic-codex",
                "backend": "codex",
                "model": "new-model",
                "models": ["new-model"],
                "env_var": "OPENAI_API_KEY",
            })
            text = (root / ".kitsoki.local.yaml").read_text(encoding="utf-8")
            self.assertIn("model: new-model", text)
            self.assertNotIn("model: old", text)
            self.assertIn("keep-me:", text)

    def test_discover_reports_missing_referenced_env_as_not_ready(self) -> None:
        with tempfile.TemporaryDirectory() as tmp:
            root = Path(tmp)
            (root / ".kitsoki.local.yaml").write_text(
                """default_profile: openai-codex
harness_profiles:
  openai-codex:
    backend: codex
    model: gpt-5.5
    env:
      OPENAI_API_KEY: "${KITSOKI_PROFILE_TEST_MISSING_KEY}"
""",
                encoding="utf-8",
            )
            old = os.environ.pop("KITSOKI_PROFILE_TEST_MISSING_KEY", None)
            try:
                report = discover(root)
            finally:
                if old is not None:
                    os.environ["KITSOKI_PROFILE_TEST_MISSING_KEY"] = old
            profile = next(profile for profile in report["profiles"] if profile["name"] == "openai-codex")
            self.assertEqual(profile["readiness"], "env-missing")

    def test_claude_config_files_are_not_reported_as_logged_in(self) -> None:
        with tempfile.TemporaryDirectory() as tmp:
            root = Path(tmp) / "repo"
            root.mkdir()
            home = Path(tmp) / "home"
            (home / ".claude").mkdir(parents=True)
            (home / ".claude" / "settings.json").write_text('{"theme":"dark"}\n', encoding="utf-8")
            (home / ".claude.json").write_text('{"userID":"user-123","projects":{}}\n', encoding="utf-8")
            (root / ".kitsoki.yaml").write_text(
                """default_profile: claude-native
harness_profiles:
  claude-native:
    backend: claude
    model: opus
""",
                encoding="utf-8",
            )
            old = self.patch_env({
                "HOME": str(home),
                "KITSOKI_AGENT_CLAUDE_BIN": "/bin/echo",
                "ANTHROPIC_API_KEY": None,
                "ANTHROPIC_AUTH_TOKEN": None,
                "OPENAI_API_KEY": None,
                "SYNTHETIC_API_KEY": None,
            })
            try:
                report = discover(root)
            finally:
                self.restore_env(old)
            claude = next(source for source in report["backend_sources"] if source["backend"] == "claude")
            self.assertEqual(claude["logged_in"], "unknown")
            self.assertEqual(claude["auth_summary"], "file-present:~/.claude/settings.json, file-present:~/.claude.json")
            self.assertEqual(claude["auth_probe"]["status"], "unsupported")
            self.assertTrue(all(not source["credential"] for source in claude["auth_sources"]))
            profile = next(profile for profile in report["profiles"] if profile["name"] == "claude-native")
            self.assertEqual(profile["readiness"], "installed-auth-unknown")
            self.assertEqual(report["candidate_action"], "")

    def test_claude_auth_status_reports_logged_out(self) -> None:
        with tempfile.TemporaryDirectory() as tmp:
            root = Path(tmp) / "repo"
            root.mkdir()
            home = Path(tmp) / "home"
            home.mkdir()
            fake_claude = self.write_executable(
                Path(tmp) / "claude",
                """#!/bin/sh
if [ "$1" = "auth" ] && [ "$2" = "status" ] && [ "$3" = "--json" ]; then
  printf '%s\n' '{"loggedIn":false,"authMethod":"none","apiProvider":"firstParty"}'
  exit 1
fi
exit 2
""",
            )
            (root / ".kitsoki.yaml").write_text(
                """default_profile: claude-native
harness_profiles:
  claude-native:
    backend: claude
    model: opus
""",
                encoding="utf-8",
            )
            old = self.patch_env({
                "HOME": str(home),
                "KITSOKI_AGENT_CLAUDE_BIN": str(fake_claude),
                "ANTHROPIC_API_KEY": None,
                "ANTHROPIC_AUTH_TOKEN": None,
                "OPENAI_API_KEY": None,
                "SYNTHETIC_API_KEY": None,
            })
            try:
                report = discover(root)
            finally:
                self.restore_env(old)
            claude = next(source for source in report["backend_sources"] if source["backend"] == "claude")
            self.assertEqual(claude["logged_in"], "no")
            self.assertEqual(claude["auth_summary"], "probe:not-logged-in")
            self.assertEqual(claude["auth_probe"]["status"], "ok")
            self.assertIs(claude["auth_probe"]["logged_in"], False)
            self.assertEqual(claude["auth_probe"]["exit_code"], 1)
            profile = next(profile for profile in report["profiles"] if profile["name"] == "claude-native")
            self.assertEqual(profile["readiness"], "installed-auth-missing")
            self.assertEqual(report["candidate_action"], "")
            self.assertIn("claude auth probe reports not logged in", report["warnings"])

    def test_claude_auth_status_reports_logged_in(self) -> None:
        with tempfile.TemporaryDirectory() as tmp:
            root = Path(tmp) / "repo"
            root.mkdir()
            home = Path(tmp) / "home"
            home.mkdir()
            fake_claude = self.write_executable(
                Path(tmp) / "claude",
                """#!/bin/sh
if [ "$1" = "auth" ] && [ "$2" = "status" ] && [ "$3" = "--json" ]; then
  printf '%s\n' '{"loggedIn":true,"authMethod":"subscription","apiProvider":"firstParty"}'
  exit 0
fi
exit 2
""",
            )
            (root / ".kitsoki.yaml").write_text(
                """default_profile: claude-native
harness_profiles:
  claude-native:
    backend: claude
    model: opus
""",
                encoding="utf-8",
            )
            old = self.patch_env({
                "HOME": str(home),
                "KITSOKI_AGENT_CLAUDE_BIN": str(fake_claude),
                "ANTHROPIC_API_KEY": None,
                "ANTHROPIC_AUTH_TOKEN": None,
                "OPENAI_API_KEY": None,
                "SYNTHETIC_API_KEY": None,
            })
            try:
                report = discover(root)
            finally:
                self.restore_env(old)
            claude = next(source for source in report["backend_sources"] if source["backend"] == "claude")
            self.assertEqual(claude["logged_in"], "yes")
            self.assertEqual(claude["auth_summary"], "probe:logged-in")
            self.assertEqual(claude["auth_probe"]["status"], "ok")
            self.assertIs(claude["auth_probe"]["logged_in"], True)
            self.assertEqual(claude["auth_probe"]["auth_method"], "subscription")
            profile = next(profile for profile in report["profiles"] if profile["name"] == "claude-native")
            self.assertEqual(profile["readiness"], "installed")

    def test_apply_rejects_raw_secret_instead_of_env_var_name(self) -> None:
        with tempfile.TemporaryDirectory() as tmp:
            root = Path(tmp)
            with self.assertRaises(ValueError):
                apply_candidate(root, {
                    "action": "upsert_openai",
                    "name": "bad",
                    "backend": "codex",
                    "model": "gpt-5.5",
                    "env_var": "sk-raw-secret",
                })


if __name__ == "__main__":
    unittest.main()
