import json
import os
import subprocess
import tempfile
import unittest
from pathlib import Path


ROOT = Path(__file__).resolve().parents[1]
ACH = ROOT / "bin" / "ach"


class MenuTest(unittest.TestCase):
    def test_cancelled_first_run_does_not_leave_an_empty_registry(self):
        with tempfile.TemporaryDirectory() as temp_dir:
            home = Path(temp_dir)
            (home / ".claude").mkdir()
            registry = home / ".config" / "agent-conversation-handoff" / "accounts.json"
            fake_bin = home / "bin"
            fake_bin.mkdir()
            fzf = fake_bin / "fzf"
            fzf.write_text("#!/usr/bin/env bash\nexit 130\n", encoding="utf-8")
            fzf.chmod(0o755)
            env = dict(os.environ, HOME=str(home), PATH=f"{fake_bin}:{os.environ['PATH']}")

            result = subprocess.run(
                ["bash", str(ACH), "menu", "--registry", str(registry)],
                cwd=home,
                text=True,
                capture_output=True,
                env=env,
            )

            self.assertNotEqual(result.returncode, 0)
            self.assertFalse(registry.exists())

    def test_first_run_imports_only_confirmed_existing_homes(self):
        with tempfile.TemporaryDirectory() as temp_dir:
            home = Path(temp_dir)
            (home / ".claude").mkdir()
            (home / ".codex").mkdir()
            registry = home / ".config" / "agent-conversation-handoff" / "accounts.json"
            fake_bin = home / "bin"
            fake_bin.mkdir()
            state = home / "state"
            fzf = fake_bin / "fzf"
            fzf.write_text(
                "#!/usr/bin/env bash\n"
                f"n=$(cat '{state}' 2>/dev/null || printf 0)\n"
                "values=('import' 'skip' 'claude-1')\n"
                "printf '%s\\n' \"${values[$n]}\"\n"
                f"printf '%s' $((n + 1)) > '{state}'\n",
                encoding="utf-8",
            )
            fzf.chmod(0o755)
            claude = fake_bin / "claude"
            claude.write_text("#!/usr/bin/env bash\nexit 0\n", encoding="utf-8")
            claude.chmod(0o755)
            codex = fake_bin / "codex"
            codex.write_text("#!/usr/bin/env bash\nexit 0\n", encoding="utf-8")
            codex.chmod(0o755)
            env = dict(os.environ, HOME=str(home), PATH=f"{fake_bin}:{os.environ['PATH']}")

            result = subprocess.run(["bash", str(ACH), "menu", "--registry", str(registry)], cwd=home, input="kile@example.com\n", text=True, capture_output=True, env=env)

            self.assertEqual(result.returncode, 0, result.stderr)
            data = json.loads(registry.read_text(encoding="utf-8"))
            self.assertEqual([item["home"] for item in data["accounts"]], [str((home / ".claude").resolve())])
            self.assertEqual(data["accounts"][0]["alias"], "kile@example.com")

    def test_single_registered_account_launches_with_its_provider_environment(self):
        with tempfile.TemporaryDirectory() as temp_dir:
            home = Path(temp_dir)
            account_home = home / ".claude"
            account_home.mkdir()
            registry = home / "accounts.json"
            registry.write_text(
                json.dumps(
                    {
                        "version": 1,
                        "next_number": {"claude": 2, "codex": 1},
                        "accounts": [
                            {"id": "claude-1", "provider": "claude", "number": 1, "home": str(account_home), "alias": "kile@example.com"}
                        ],
                    }
                ),
                encoding="utf-8",
            )
            fake_bin = home / "bin"
            fake_bin.mkdir()
            fzf = fake_bin / "fzf"
            fzf.write_text("#!/usr/bin/env bash\nprintf '%s\\n' 'claude-1'\n", encoding="utf-8")
            fzf.chmod(0o755)
            capture = home / "launch"
            claude = fake_bin / "claude"
            claude.write_text(f"#!/usr/bin/env bash\nprintf '%s' \"${{CLAUDE_CONFIG_DIR-}}\" > '{capture}'\n", encoding="utf-8")
            claude.chmod(0o755)
            env = dict(os.environ, HOME=str(home), PATH=f"{fake_bin}:{os.environ['PATH']}")

            result = subprocess.run(
                ["bash", str(ACH), "menu", "--registry", str(registry)],
                cwd=home,
                text=True,
                capture_output=True,
                env=env,
            )

            self.assertEqual(result.returncode, 0, result.stderr)
            self.assertEqual(capture.read_text(encoding="utf-8"), "")

    def test_handoff_menu_selects_source_session_and_different_target(self):
        with tempfile.TemporaryDirectory() as temp_dir:
            home = Path(temp_dir)
            project = home / "project"
            project.mkdir()
            claude_home = home / ".claude"
            codex_home = home / ".codex"
            session_dir = claude_home / "projects" / str(project.resolve()).replace("/", "-")
            session_dir.mkdir(parents=True)
            codex_home.mkdir()
            session = session_dir / "source.jsonl"
            session.write_text(json.dumps({"type": "user", "sessionId": "uuid-123", "message": {"content": "continue this"}}) + "\n", encoding="utf-8")
            registry = home / "accounts.json"
            registry.write_text(
                json.dumps(
                    {
                        "version": 1,
                        "next_number": {"claude": 2, "codex": 2},
                        "accounts": [
                            {"id": "claude-1", "provider": "claude", "number": 1, "home": str(claude_home), "alias": ""},
                            {"id": "codex-1", "provider": "codex", "number": 1, "home": str(codex_home), "alias": ""},
                        ],
                    }
                ),
                encoding="utf-8",
            )
            fake_bin = home / "bin"
            fake_bin.mkdir()
            state = home / "state"
            selections = ["action:handoff", "claude-1", str(session), "codex-1"]
            fzf = fake_bin / "fzf"
            fzf.write_text(
                "#!/usr/bin/env bash\n"
                f"n=$(cat '{state}' 2>/dev/null || printf 0)\n"
                f"values=({' '.join(repr(item) for item in selections)})\n"
                "printf '%s\\n' \"${values[$n]}\"\n"
                f"printf '%s' $((n + 1)) > '{state}'\n",
                encoding="utf-8",
            )
            fzf.chmod(0o755)
            capture = home / "codex-launch"
            codex = fake_bin / "codex"
            codex.write_text(
                f"#!/usr/bin/env bash\nif [ \"${{1-}} ${{2-}}\" = 'login status' ]; then exit 0; fi\nprintf '%s' \"$CODEX_HOME|$1\" > '{capture}'\n",
                encoding="utf-8",
            )
            codex.chmod(0o755)
            claude = fake_bin / "claude"
            claude.write_text("#!/usr/bin/env bash\nexit 0\n", encoding="utf-8")
            claude.chmod(0o755)
            env = dict(os.environ, HOME=str(home), PATH=f"{fake_bin}:{os.environ['PATH']}")

            result = subprocess.run(["bash", str(ACH), "menu", "--registry", str(registry)], cwd=project, text=True, capture_output=True, env=env)

            self.assertEqual(result.returncode, 0, result.stderr)
            launched = capture.read_text(encoding="utf-8")
            self.assertTrue(launched.startswith(f"{codex_home.resolve()}|"))
            self.assertIn("transcript.md", launched)

    def test_account_settings_unregisters_without_deleting_home(self):
        with tempfile.TemporaryDirectory() as temp_dir:
            home = Path(temp_dir)
            first_home = home / ".claude"
            second_home = home / ".claude-2"
            first_home.mkdir()
            second_home.mkdir()
            registry = home / "accounts.json"
            registry.write_text(
                json.dumps(
                    {
                        "version": 1,
                        "next_number": {"claude": 3, "codex": 1},
                        "accounts": [
                            {"id": "claude-1", "provider": "claude", "number": 1, "home": str(first_home), "alias": ""},
                            {"id": "claude-2", "provider": "claude", "number": 2, "home": str(second_home), "alias": ""},
                        ],
                    }
                ),
                encoding="utf-8",
            )
            fake_bin = home / "bin"
            fake_bin.mkdir()
            state = home / "state"
            fzf = fake_bin / "fzf"
            fzf.write_text(
                "#!/usr/bin/env bash\n"
                f"n=$(cat '{state}' 2>/dev/null || printf 0)\n"
                "values=('action:accounts' 'remove' 'claude-2' 'confirm' 'back' 'claude-1')\n"
                "printf '%s\\n' \"${values[$n]}\"\n"
                f"printf '%s' $((n + 1)) > '{state}'\n",
                encoding="utf-8",
            )
            fzf.chmod(0o755)
            claude = fake_bin / "claude"
            claude.write_text("#!/usr/bin/env bash\nexit 0\n", encoding="utf-8")
            claude.chmod(0o755)
            env = dict(os.environ, HOME=str(home), PATH=f"{fake_bin}:{os.environ['PATH']}")

            result = subprocess.run(["bash", str(ACH), "menu", "--registry", str(registry)], cwd=home, text=True, capture_output=True, env=env)

            self.assertEqual(result.returncode, 0, result.stderr)
            self.assertTrue(second_home.is_dir())
            self.assertEqual([item["id"] for item in json.loads(registry.read_text(encoding="utf-8"))["accounts"]], ["claude-1"])

    def test_account_settings_adds_next_account_and_starts_official_login(self):
        with tempfile.TemporaryDirectory() as temp_dir:
            home = Path(temp_dir)
            first_home = home / ".claude"
            first_home.mkdir()
            registry = home / "accounts.json"
            registry.write_text(json.dumps({"version": 1, "next_number": {"claude": 2, "codex": 1}, "accounts": [{"id": "claude-1", "provider": "claude", "number": 1, "home": str(first_home), "alias": ""}]}), encoding="utf-8")
            fake_bin = home / "bin"
            fake_bin.mkdir()
            state = home / "state"
            fzf = fake_bin / "fzf"
            fzf.write_text(
                "#!/usr/bin/env bash\n"
                f"n=$(cat '{state}' 2>/dev/null || printf 0)\n"
                "values=('action:accounts' 'add' 'claude' 'confirm')\n"
                "printf '%s\\n' \"${values[$n]}\"\n"
                f"printf '%s' $((n + 1)) > '{state}'\n",
                encoding="utf-8",
            )
            fzf.chmod(0o755)
            capture = home / "login"
            claude = fake_bin / "claude"
            claude.write_text(f"#!/usr/bin/env bash\nprintf '%s' \"$CLAUDE_CONFIG_DIR|$1 $2\" > '{capture}'\n", encoding="utf-8")
            claude.chmod(0o755)
            env = dict(os.environ, HOME=str(home), PATH=f"{fake_bin}:{os.environ['PATH']}")

            result = subprocess.run(["bash", str(ACH), "menu", "--registry", str(registry)], cwd=home, input="karen@example.com\n", text=True, capture_output=True, env=env)

            self.assertEqual(result.returncode, 0, result.stderr)
            second_home = (home / ".claude-2").resolve()
            self.assertEqual(capture.read_text(encoding="utf-8"), f"{second_home}|auth login")
            second = json.loads(registry.read_text(encoding="utf-8"))["accounts"][1]
            self.assertEqual((second["id"], second["alias"], second["home"]), ("claude-2", "karen@example.com", str(second_home)))

    def test_account_settings_renames_alias(self):
        with tempfile.TemporaryDirectory() as temp_dir:
            home = Path(temp_dir)
            account_home = home / ".claude"
            account_home.mkdir()
            registry = home / "accounts.json"
            registry.write_text(json.dumps({"version": 1, "next_number": {"claude": 2, "codex": 1}, "accounts": [{"id": "claude-1", "provider": "claude", "number": 1, "home": str(account_home), "alias": "old"}]}), encoding="utf-8")
            fake_bin = home / "bin"
            fake_bin.mkdir()
            state = home / "state"
            fzf = fake_bin / "fzf"
            fzf.write_text(
                "#!/usr/bin/env bash\n"
                f"n=$(cat '{state}' 2>/dev/null || printf 0)\n"
                "values=('action:accounts' 'rename' 'claude-1' 'back' 'claude-1')\n"
                "printf '%s\\n' \"${values[$n]}\"\n"
                f"printf '%s' $((n + 1)) > '{state}'\n",
                encoding="utf-8",
            )
            fzf.chmod(0o755)
            claude = fake_bin / "claude"
            claude.write_text("#!/usr/bin/env bash\nexit 0\n", encoding="utf-8")
            claude.chmod(0o755)
            env = dict(os.environ, HOME=str(home), PATH=f"{fake_bin}:{os.environ['PATH']}")

            result = subprocess.run(["bash", str(ACH), "menu", "--registry", str(registry)], cwd=home, input="new@example.com\n", text=True, capture_output=True, env=env)

            self.assertEqual(result.returncode, 0, result.stderr)
            self.assertEqual(json.loads(registry.read_text(encoding="utf-8"))["accounts"][0]["alias"], "new@example.com")

    def test_account_settings_login_uses_selected_account_home(self):
        with tempfile.TemporaryDirectory() as temp_dir:
            home = Path(temp_dir)
            account_home = home / ".codex-2"
            account_home.mkdir()
            registry = home / "accounts.json"
            registry.write_text(json.dumps({"version": 1, "next_number": {"claude": 1, "codex": 3}, "accounts": [{"id": "codex-2", "provider": "codex", "number": 2, "home": str(account_home), "alias": ""}]}), encoding="utf-8")
            fake_bin = home / "bin"
            fake_bin.mkdir()
            state = home / "state"
            fzf = fake_bin / "fzf"
            fzf.write_text("#!/usr/bin/env bash\n" f"n=$(cat '{state}' 2>/dev/null || printf 0)\n" "values=('action:accounts' 'login' 'codex-2')\n" "printf '%s\\n' \"${values[$n]}\"\n" f"printf '%s' $((n + 1)) > '{state}'\n", encoding="utf-8")
            fzf.chmod(0o755)
            capture = home / "login"
            codex = fake_bin / "codex"
            codex.write_text(f"#!/usr/bin/env bash\nprintf '%s' \"$CODEX_HOME|$1\" > '{capture}'\n", encoding="utf-8")
            codex.chmod(0o755)
            env = dict(os.environ, HOME=str(home), PATH=f"{fake_bin}:{os.environ['PATH']}")

            result = subprocess.run(["bash", str(ACH), "menu", "--registry", str(registry)], cwd=home, text=True, capture_output=True, env=env)

            self.assertEqual(result.returncode, 0, result.stderr)
            self.assertEqual(capture.read_text(encoding="utf-8"), f"{account_home.resolve()}|login")

    def test_account_settings_imports_only_selected_candidate(self):
        with tempfile.TemporaryDirectory() as temp_dir:
            home = Path(temp_dir)
            claude_home = home / ".claude"
            codex_home = home / ".codex"
            claude_home.mkdir()
            codex_home.mkdir()
            registry = home / "accounts.json"
            registry.write_text(json.dumps({"version": 1, "next_number": {"claude": 2, "codex": 1}, "accounts": [{"id": "claude-1", "provider": "claude", "number": 1, "home": str(claude_home), "alias": ""}]}), encoding="utf-8")
            fake_bin = home / "bin"
            fake_bin.mkdir()
            state = home / "state"
            import_key = f"codex|{codex_home.resolve()}"
            fzf = fake_bin / "fzf"
            fzf.write_text("#!/usr/bin/env bash\n" f"n=$(cat '{state}' 2>/dev/null || printf 0)\n" f"values=('action:accounts' 'import' '{import_key}' 'back' 'claude-1')\n" "printf '%s\\n' \"${values[$n]}\"\n" f"printf '%s' $((n + 1)) > '{state}'\n", encoding="utf-8")
            fzf.chmod(0o755)
            for command in ("claude", "codex"):
                executable = fake_bin / command
                executable.write_text("#!/usr/bin/env bash\nexit 0\n", encoding="utf-8")
                executable.chmod(0o755)
            env = dict(os.environ, HOME=str(home), PATH=f"{fake_bin}:{os.environ['PATH']}")

            result = subprocess.run(["bash", str(ACH), "menu", "--registry", str(registry)], cwd=home, input="personal\n", text=True, capture_output=True, env=env)

            self.assertEqual(result.returncode, 0, result.stderr)
            imported = json.loads(registry.read_text(encoding="utf-8"))["accounts"][1]
            self.assertEqual((imported["provider"], imported["home"], imported["alias"]), ("codex", str(codex_home.resolve()), "personal"))


if __name__ == "__main__":
    unittest.main()
