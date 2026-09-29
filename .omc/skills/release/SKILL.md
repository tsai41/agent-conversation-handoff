---
name: release
description: Cut a new release of agent-conversation-handoff (ach) — tag, push, wait for the GitHub Actions build, then update the local binary. Triggers "release", "重新 release", "發 release", "cut a release", "出新版".
---

# agent-conversation-handoff Release

Follow `skills/release/SKILL.md` to publish (version choice, push, tag, wait
for `release.yml`, verify assets), then `skills/install/SKILL.md` if the user
also wants the local binary updated (default `~/bin/ach`, or the user's
existing `COMMAND`).

## Gotchas

- `release.yml` only triggers on tag push (`push: tags: v*`), not on branch
  push.
- The workflow builds macOS binaries only; there is no Linux/Windows asset.
- `make install` and `install.sh` need `fzf`, `curl` (or an authenticated
  `gh`), and either `claude` or `codex` on PATH, and refuse non-macOS systems.
