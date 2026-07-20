import json
import os
import subprocess
import tempfile
import unittest
from pathlib import Path


ROOT = Path(__file__).resolve().parents[1]
ACH = ROOT / "bin" / "ach"


class RegistryHandoffTest(unittest.TestCase):
    def fixture(self, root, target_authenticated):
        project = root / "project"
        project.mkdir()
        claude_home = root / ".claude"
        codex_home = root / ".codex"
        claude_home.mkdir()
        codex_home.mkdir()
        session = root / "source.jsonl"
        session.write_text(json.dumps({"type": "user", "sessionId": "source-uuid", "message": {"content": "handoff me"}}) + "\n", encoding="utf-8")
        registry = root / "accounts.json"
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
        fake_bin = root / "bin"
        fake_bin.mkdir()
        codex = fake_bin / "codex"
        codex.write_text(f"#!/usr/bin/env bash\nexit {0 if target_authenticated else 1}\n", encoding="utf-8")
        codex.chmod(0o755)
        return project, session, registry, fake_bin

    def run_handoff(self, project, session, registry, fake_bin, source_id="claude-1", target_id="codex-1"):
        env = dict(os.environ, HOME=str(project.parent), PATH=f"{fake_bin}:{os.environ['PATH']}")
        return subprocess.run(
            [
                "bash", str(ACH), "handoff", "--registry", str(registry),
                "--source-id", source_id, "--target-id", target_id,
                "--session", str(session), "--no-launch",
            ],
            cwd=project,
            text=True,
            capture_output=True,
            env=env,
        )

    def test_unauthenticated_target_stops_before_creating_artifact(self):
        with tempfile.TemporaryDirectory() as temp_dir:
            project, session, registry, fake_bin = self.fixture(Path(temp_dir), False)

            result = self.run_handoff(project, session, registry, fake_bin)

            self.assertNotEqual(result.returncode, 0)
            self.assertIn("not authenticated", result.stderr)
            self.assertFalse((project / ".agent-handoffs").exists())

    def test_authenticated_target_creates_artifact_without_touching_source(self):
        with tempfile.TemporaryDirectory() as temp_dir:
            project, session, registry, fake_bin = self.fixture(Path(temp_dir), True)
            before = session.read_bytes()

            result = self.run_handoff(project, session, registry, fake_bin)

            self.assertEqual(result.returncode, 0, result.stderr)
            self.assertEqual(session.read_bytes(), before)
            artifact = next(path for path in (project / ".agent-handoffs").iterdir() if path.is_dir())
            self.assertEqual((artifact / "source.jsonl").read_bytes(), before)

    def test_source_account_cannot_be_the_target(self):
        with tempfile.TemporaryDirectory() as temp_dir:
            project, session, registry, fake_bin = self.fixture(Path(temp_dir), True)

            result = self.run_handoff(project, session, registry, fake_bin, target_id="claude-1")

            self.assertNotEqual(result.returncode, 0)
            self.assertIn("must be different", result.stderr)
            self.assertFalse((project / ".agent-handoffs").exists())


if __name__ == "__main__":
    unittest.main()
