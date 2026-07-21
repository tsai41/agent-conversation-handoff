---
name: release
description: Cut a new release of agent-conversation-handoff (ach/ccs) — tag, push, wait for the GitHub Actions build, then update the local binary. Triggers "release", "重新 release", "發 release", "cut a release", "出新版".
---

# agent-conversation-handoff Release

This repo has no version string in code and no changelog file. A release is
just a `vX.Y.Z` git tag; `.github/workflows/release.yml` does the rest (builds
`ach-darwin-arm64` / `ach-darwin-amd64` via `make build`, publishes a GitHub
Release with `generate_release_notes: true`).

## Steps

1. **Check what's new**: `git tag --sort=-creatordate | head -1` for the last
   tag, then `git log <last-tag>..HEAD --oneline` for what's shipping.
2. **Pick the version bump** (semver, no `v1.0.0`-style code constant to
   update — only the tag matters):
   - fix/docs/refactor only → patch
   - new feature, backward compatible → minor
   - breaking change → major
   Confirm the exact `vX.Y.Z` with the user before tagging (AskUserQuestion) —
   don't guess silently.
3. **Verify before tagging**: `go build ./... && go test ./...` must pass.
4. **Tag and push** (push is a shared/visible action — confirm with the user
   first):
   ```
   git tag -a vX.Y.Z -m "vX.Y.Z"
   git push origin vX.Y.Z
   ```
5. **Watch the workflow** until it finishes (do not just fire-and-forget):
   ```
   gh run list --repo tsai41/agent-conversation-handoff --workflow=release.yml --limit 1 --json status,conclusion
   ```
   Poll every few seconds until `status` is `completed`; check `conclusion`
   is `success`. If it fails, `gh run view <run-id> --log-failed` before
   retrying.
6. **Install locally** (optional, only if the user asked to update their own
   machine too):
   ```
   make install
   ```
   This downloads the just-published asset via `gh release download`,
   backs up any existing non-symlink binary at `$BIN_DIR/$COMMAND`
   (default `~/bin/ccs`) to `<path>.bak-<timestamp>`, then installs the new
   one. Safe to re-run.

## Gotchas

- `release.yml` only triggers on tag push (`push: tags: v*`), not on branch
  push — pushing commits to `main` alone does nothing.
- The workflow builds macOS binaries only (`ach-darwin-arm64`,
  `ach-darwin-amd64`); there's no Linux/Windows asset.
- `make install` needs `gh` authenticated (repo is private) plus `fzf`,
  and either `claude` or `codex` on PATH — it hard-fails with a Chinese
  error message pointing at whichever is missing.
