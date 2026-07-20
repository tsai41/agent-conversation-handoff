import os
import subprocess
import tempfile
import unittest
from pathlib import Path


ROOT = Path(__file__).resolve().parents[1]


class InstallTest(unittest.TestCase):
    def test_make_install_creates_a_symlink_to_ccs_in_home_bin(self):
        with tempfile.TemporaryDirectory() as temp_dir:
            temp = Path(temp_dir)
            fake_bin = temp / "tools"
            fake_bin.mkdir()
            for command in ("claude", "fzf"):
                path = fake_bin / command
                path.write_text("#!/usr/bin/env bash\nexit 0\n", encoding="utf-8")
                path.chmod(0o755)
            env = dict(os.environ, HOME=str(temp / "home"), PATH=f"{fake_bin}:{os.environ['PATH']}")

            result = subprocess.run(["make", "install", "COMMAND=ccs"], cwd=ROOT, text=True, capture_output=True, env=env)

            installed = Path(env["HOME"]) / "bin" / "ccs"
            self.assertEqual(result.returncode, 0, result.stderr)
            self.assertTrue(installed.is_symlink())
            self.assertEqual(installed.resolve(), (ROOT / "bin" / "launcher").resolve())

    def test_installed_launcher_resolves_its_repo_bin_directory(self):
        with tempfile.TemporaryDirectory() as temp_dir:
            temp = Path(temp_dir)
            fake_bin = temp / "tools"
            fake_bin.mkdir()
            account_home = temp / "home" / ".claude"
            account_home.mkdir(parents=True)
            registry = temp / "home" / ".config" / "agent-conversation-handoff" / "accounts.json"
            registry.parent.mkdir(parents=True)
            registry.write_text(
                '{"version":1,"next_number":{"claude":2,"codex":1},"accounts":[{"id":"claude-1","provider":"claude","number":1,"home":"' + str(account_home) + '","alias":""}]}',
                encoding="utf-8",
            )
            fzf = fake_bin / "fzf"
            fzf.write_text("#!/usr/bin/env bash\nprintf '%s\\n' 'claude-1'\n", encoding="utf-8")
            fzf.chmod(0o755)
            capture = temp / "launched"
            claude = fake_bin / "claude"
            claude.write_text(f"#!/usr/bin/env bash\nprintf launched > '{capture}'\n", encoding="utf-8")
            claude.chmod(0o755)
            installed = temp / "ccs"
            installed.symlink_to(ROOT / "bin" / "launcher")
            env = dict(os.environ, HOME=str(temp / "home"), PATH=f"{fake_bin}:{os.environ['PATH']}")

            result = subprocess.run([str(installed)], text=True, capture_output=True, env=env)

            self.assertEqual(result.returncode, 0, result.stderr)
            self.assertTrue(capture.exists(), "installed launcher did not delegate to the registry menu")
            self.assertEqual(capture.read_text(encoding="utf-8"), "launched")


if __name__ == "__main__":
    unittest.main()
