import fcntl
import json
import os
import subprocess
import tempfile
import time
import unittest
from pathlib import Path


ROOT = Path(__file__).resolve().parents[1]
ACH = ROOT / "bin" / "ach"


class AccountRegistryTest(unittest.TestCase):
    def run_ach(self, *args, home):
        env = dict(os.environ, HOME=str(home))
        return subprocess.run(
            ["bash", str(ACH), *args],
            text=True,
            capture_output=True,
            env=env,
        )

    def test_single_account_uses_provider_name_without_suffix(self):
        with tempfile.TemporaryDirectory() as temp_dir:
            home = Path(temp_dir)
            registry = home / "accounts.json"
            registry.write_text(
                json.dumps(
                    {
                        "version": 1,
                        "next_number": {"claude": 2, "codex": 2},
                        "accounts": [
                            {"id": "codex-1", "provider": "codex", "number": 1, "home": str(home / ".codex"), "alias": "personal"}
                        ],
                    }
                ),
                encoding="utf-8",
            )

            result = self.run_ach("accounts", "list", "--registry", str(registry), home=home)

            self.assertEqual(result.returncode, 0, result.stderr)
            self.assertEqual(result.stdout.strip(), "codex-1\tCodex")

    def test_multiple_accounts_keep_numbers_and_append_optional_alias(self):
        with tempfile.TemporaryDirectory() as temp_dir:
            home = Path(temp_dir)
            registry = home / "accounts.json"
            registry.write_text(
                json.dumps(
                    {
                        "version": 1,
                        "next_number": {"claude": 4, "codex": 1},
                        "accounts": [
                            {"id": "claude-1", "provider": "claude", "number": 1, "home": str(home / ".claude"), "alias": "kile@example.com"},
                            {"id": "claude-3", "provider": "claude", "number": 3, "home": str(home / ".claude-3"), "alias": ""},
                        ],
                    }
                ),
                encoding="utf-8",
            )

            result = self.run_ach("accounts", "list", "--registry", str(registry), home=home)

            self.assertEqual(result.returncode, 0, result.stderr)
            self.assertEqual(result.stdout.splitlines(), ["claude-1\tClaude · 1 · kile@example.com", "claude-3\tClaude · 3"])

    def test_add_creates_home_and_writes_registry_atomically(self):
        with tempfile.TemporaryDirectory() as temp_dir:
            home = Path(temp_dir)
            registry = home / ".config" / "ach" / "accounts.json"
            account_home = home / ".claude"

            result = self.run_ach(
                "accounts", "add", "--registry", str(registry),
                "--provider", "claude", "--home", str(account_home),
                "--alias", "kile@example.com", home=home,
            )

            self.assertEqual(result.returncode, 0, result.stderr)
            self.assertTrue(account_home.is_dir())
            data = json.loads(registry.read_text(encoding="utf-8"))
            self.assertEqual(data["accounts"][0]["id"], "claude-1")
            self.assertEqual(data["accounts"][0]["alias"], "kile@example.com")
            self.assertEqual(data["next_number"]["claude"], 2)
            self.assertEqual(list(registry.parent.glob(".accounts.json.*.tmp")), [])

    def test_remove_unregisters_without_deleting_home_or_reusing_number(self):
        with tempfile.TemporaryDirectory() as temp_dir:
            home = Path(temp_dir)
            registry = home / "accounts.json"
            first_home = home / ".codex"
            second_home = home / ".codex-2"
            third_home = home / ".codex-3"
            self.assertEqual(self.run_ach("accounts", "add", "--registry", str(registry), "--provider", "codex", "--home", str(first_home), home=home).returncode, 0)
            self.assertEqual(self.run_ach("accounts", "add", "--registry", str(registry), "--provider", "codex", "--home", str(second_home), home=home).returncode, 0)

            removed = self.run_ach("accounts", "remove", "--registry", str(registry), "--id", "codex-2", home=home)
            added = self.run_ach("accounts", "add", "--registry", str(registry), "--provider", "codex", "--home", str(third_home), home=home)

            self.assertEqual(removed.returncode, 0, removed.stderr)
            self.assertEqual(added.returncode, 0, added.stderr)
            self.assertTrue(second_home.is_dir())
            data = json.loads(registry.read_text(encoding="utf-8"))
            self.assertEqual([item["id"] for item in data["accounts"]], ["codex-1", "codex-3"])

    def test_discover_lists_standard_and_legacy_homes_without_importing(self):
        with tempfile.TemporaryDirectory() as temp_dir:
            home = Path(temp_dir)
            for path in (home / ".claude", home / ".claude-2", home / ".codex", home / ".codex-2", home / ".codex-homes" / "legacy"):
                path.mkdir(parents=True)
            registry = home / "accounts.json"

            result = self.run_ach("accounts", "discover", "--registry", str(registry), home=home)

            self.assertEqual(result.returncode, 0, result.stderr)
            self.assertEqual(
                result.stdout.splitlines(),
                [
                    f"claude\t{(home / '.claude').resolve()}",
                    f"claude\t{(home / '.claude-2').resolve()}",
                    f"codex\t{(home / '.codex').resolve()}",
                    f"codex\t{(home / '.codex-2').resolve()}",
                    f"codex\t{(home / '.codex-homes' / 'legacy').resolve()}",
                ],
            )
            self.assertFalse(registry.exists())

    def test_rename_changes_only_the_optional_alias(self):
        with tempfile.TemporaryDirectory() as temp_dir:
            home = Path(temp_dir)
            registry = home / "accounts.json"
            account_home = home / ".claude"
            self.assertEqual(self.run_ach("accounts", "add", "--registry", str(registry), "--provider", "claude", "--home", str(account_home), home=home).returncode, 0)

            result = self.run_ach("accounts", "rename", "--registry", str(registry), "--id", "claude-1", "--alias", "kile@example.com", home=home)

            self.assertEqual(result.returncode, 0, result.stderr)
            account = json.loads(registry.read_text(encoding="utf-8"))["accounts"][0]
            self.assertEqual(account["alias"], "kile@example.com")
            self.assertEqual(account["home"], str(account_home.resolve()))

    def test_auth_status_uses_the_selected_account_home(self):
        with tempfile.TemporaryDirectory() as temp_dir:
            home = Path(temp_dir)
            registry = home / "accounts.json"
            account_home = home / ".codex-2"
            self.assertEqual(self.run_ach("accounts", "add", "--registry", str(registry), "--provider", "codex", "--home", str(account_home), home=home).returncode, 0)
            fake_bin = home / "bin"
            fake_bin.mkdir()
            codex = fake_bin / "codex"
            codex.write_text(
                f"#!/usr/bin/env bash\n[ \"$CODEX_HOME\" = \"{account_home.resolve()}\" ] && [ \"$1 $2\" = \"login status\" ]\n",
                encoding="utf-8",
            )
            codex.chmod(0o755)
            env = dict(os.environ, HOME=str(home), PATH=f"{fake_bin}:{os.environ['PATH']}")

            result = subprocess.run(
                ["bash", str(ACH), "accounts", "auth-status", "--registry", str(registry), "--id", "codex-1"],
                text=True,
                capture_output=True,
                env=env,
            )

            self.assertEqual(result.returncode, 0, result.stderr)
            self.assertEqual(result.stdout.strip(), "authenticated")

    def test_suggest_uses_default_home_then_next_stable_number(self):
        with tempfile.TemporaryDirectory() as temp_dir:
            home = Path(temp_dir)
            registry = home / "accounts.json"

            first = self.run_ach("accounts", "suggest-home", "--registry", str(registry), "--provider", "codex", home=home)
            self.assertEqual(first.returncode, 0, first.stderr)
            self.assertEqual(first.stdout.strip(), str((home / ".codex").resolve()))
            self.assertEqual(self.run_ach("accounts", "add", "--registry", str(registry), "--provider", "codex", "--home", first.stdout.strip(), home=home).returncode, 0)
            second = self.run_ach("accounts", "suggest-home", "--registry", str(registry), "--provider", "codex", home=home)
            self.assertEqual(second.stdout.strip(), str((home / ".codex-2").resolve()))

    def test_suggest_skips_existing_homes_independent_of_account_number(self):
        with tempfile.TemporaryDirectory() as temp_dir:
            home = Path(temp_dir)
            (home / ".codex").mkdir()
            (home / ".codex-2").mkdir()
            registry = home / "accounts.json"
            registry.write_text(
                json.dumps(
                    {
                        "version": 1,
                        "next_number": {"claude": 1, "codex": 8},
                        "accounts": [
                            {"id": "codex-7", "provider": "codex", "number": 7, "home": str(home / ".codex-homes" / "legacy"), "alias": ""}
                        ],
                    }
                ),
                encoding="utf-8",
            )

            result = self.run_ach("accounts", "suggest-home", "--registry", str(registry), "--provider", "codex", home=home)

            self.assertEqual(result.returncode, 0, result.stderr)
            self.assertEqual(result.stdout.strip(), str((home / ".codex-3").resolve()))

    def test_registry_rejects_incomplete_schema_without_traceback(self):
        with tempfile.TemporaryDirectory() as temp_dir:
            home = Path(temp_dir)
            registry = home / "accounts.json"
            invalid = {"version": 1, "accounts": []}
            registry.write_text(json.dumps(invalid), encoding="utf-8")

            result = self.run_ach("accounts", "list", "--registry", str(registry), home=home)

            self.assertNotEqual(result.returncode, 0)
            self.assertIn("unsupported account registry", result.stderr)
            self.assertNotIn("Traceback", result.stderr)
            self.assertEqual(len(list(home.glob("accounts.json.corrupt-*"))), 1)

    def test_registry_updates_wait_for_the_registry_lock(self):
        with tempfile.TemporaryDirectory() as temp_dir:
            home = Path(temp_dir)
            registry = home / "accounts.json"
            lock_path = registry.with_name(f"{registry.name}.lock")
            lock_path.parent.mkdir(parents=True, exist_ok=True)
            env = dict(os.environ, HOME=str(home))
            command = [
                "bash", str(ACH), "accounts", "add", "--registry", str(registry),
                "--provider", "claude", "--home", str(home / ".claude"),
            ]

            with lock_path.open("w", encoding="utf-8") as lock_file:
                fcntl.flock(lock_file, fcntl.LOCK_EX)
                process = subprocess.Popen(command, text=True, stdout=subprocess.PIPE, stderr=subprocess.PIPE, env=env)
                try:
                    time.sleep(0.25)
                    self.assertIsNone(process.poll(), "registry update ignored the held lock")
                finally:
                    fcntl.flock(lock_file, fcntl.LOCK_UN)
                stdout, stderr = process.communicate(timeout=5)

            self.assertEqual(process.returncode, 0, stderr)
            self.assertEqual(stdout.strip(), "claude-1")

    def test_invalid_registry_is_backed_up_and_never_rebuilt(self):
        with tempfile.TemporaryDirectory() as temp_dir:
            home = Path(temp_dir)
            registry = home / "accounts.json"
            invalid = b'{"version": 1, broken'
            registry.write_bytes(invalid)

            result = self.run_ach("accounts", "list", "--registry", str(registry), home=home)

            self.assertNotEqual(result.returncode, 0)
            self.assertEqual(registry.read_bytes(), invalid)
            backups = list(home.glob("accounts.json.corrupt-*"))
            self.assertEqual(len(backups), 1)
            self.assertEqual(backups[0].read_bytes(), invalid)
            self.assertIn(str(backups[0]), result.stderr)


if __name__ == "__main__":
    unittest.main()
