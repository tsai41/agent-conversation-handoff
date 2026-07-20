import hashlib
import json
import subprocess
import tempfile
import unittest
from pathlib import Path


ROOT = Path(__file__).resolve().parents[1]
COMMAND = ROOT / "bin" / "ach"


class HandoffTest(unittest.TestCase):
    def test_creates_complete_snapshot_without_changing_source(self):
        with tempfile.TemporaryDirectory() as temp_dir:
            temp = Path(temp_dir)
            source = temp / "claude-1.jsonl"
            source.write_text(
                "\n".join(
                    [
                        json.dumps({"type": "user", "message": {"content": "Implement safe handoff"}}),
                        json.dumps({"type": "assistant", "message": {"content": [{"type": "text", "text": "I will inspect the sessions."}]}}),
                        json.dumps({"type": "bridge-session", "sessionId": "account-1-only"}),
                    ]
                )
                + "\n",
                encoding="utf-8",
            )
            before = hashlib.sha256(source.read_bytes()).hexdigest()
            target = temp / "claude-2-project"

            result = subprocess.run(
                ["bash", str(COMMAND), "create", "--source", str(source), "--target", str(target)],
                text=True,
                capture_output=True,
            )

            self.assertEqual(result.returncode, 0, result.stderr)
            self.assertEqual(hashlib.sha256(source.read_bytes()).hexdigest(), before)

            handoff = target / ".agent-handoffs"
            artifacts = [path for path in handoff.iterdir() if path.is_dir()]
            self.assertEqual(len(artifacts), 1)
            artifact = artifacts[0]
            self.assertEqual((artifact / "source.jsonl").read_bytes(), source.read_bytes())
            self.assertIn("Implement safe handoff", (artifact / "transcript.md").read_text(encoding="utf-8"))
            self.assertIn("account-1-only", (artifact / "source.jsonl").read_text(encoding="utf-8"))

    def test_create_renders_a_codex_response_item_transcript(self):
        with tempfile.TemporaryDirectory() as temp_dir:
            temp = Path(temp_dir)
            source = temp / "codex.jsonl"
            source.write_text(
                json.dumps({"type": "response_item", "payload": {"role": "user", "content": [{"type": "input_text", "text": "Continue the migration"}]}})
                + "\n"
                + json.dumps({"type": "response_item", "payload": {"role": "assistant", "content": [{"type": "output_text", "text": "Migration inspected."}]}})
                + "\n",
                encoding="utf-8",
            )
            target = temp / "project"

            result = subprocess.run(
                ["bash", str(COMMAND), "create", "--source", str(source), "--target", str(target), "--source-type", "codex"],
                text=True,
                capture_output=True,
            )

            self.assertEqual(result.returncode, 0, result.stderr)
            transcript = next(path for path in (target / ".agent-handoffs").iterdir() if path.is_dir()) / "transcript.md"
            rendered = transcript.read_text(encoding="utf-8")
            self.assertIn("# Codex Conversation Transcript", rendered)
            self.assertIn("Continue the migration", rendered)

    def test_artifacts_are_private_and_ignored_by_git(self):
        with tempfile.TemporaryDirectory() as temp_dir:
            project = Path(temp_dir) / "project"
            project.mkdir()
            subprocess.run(["git", "init", "-q"], cwd=project, check=True)
            source = Path(temp_dir) / "source.jsonl"
            source.write_text(json.dumps({"type": "user", "message": {"content": "private context"}}) + "\n", encoding="utf-8")

            result = subprocess.run(["bash", str(COMMAND), "create", "--source", str(source), "--target", str(project)], text=True, capture_output=True)

            self.assertEqual(result.returncode, 0, result.stderr)
            root = project / ".agent-handoffs"
            artifact = next(path for path in root.iterdir() if path.is_dir())
            self.assertEqual(root.stat().st_mode & 0o777, 0o700)
            self.assertEqual(artifact.stat().st_mode & 0o777, 0o700)
            for path in artifact.iterdir():
                self.assertEqual(path.stat().st_mode & 0o777, 0o600)
            status = subprocess.run(["git", "status", "--short"], cwd=project, text=True, capture_output=True, check=True)
            self.assertEqual(status.stdout, "")


if __name__ == "__main__":
    unittest.main()
