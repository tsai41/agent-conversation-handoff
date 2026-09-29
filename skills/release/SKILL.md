---
name: release
description: "Publish a verified versioned release of agent-conversation-handoff: choose the version, push the verified commit, tag v*, wait for the release workflow, and verify the published assets. Use when the user asks to release, tag, or cut a new version."
---

# Release

Release only after the requested changes are committed, the worktree is
understood, and `go test ./...` has passed. A release is a `vX.Y.Z` git tag:
`.github/workflows/release.yml` builds `ach-darwin-arm64` and
`ach-darwin-amd64` with `make build` and publishes them with `SHA256SUMS`.
There is no version constant in code and no changelog file.

## Workflow

1. Inspect the working tree, the latest tag (`git tag --sort=-creatordate |
   head -1`), and `git log <last-tag>..HEAD --oneline`. Preserve unrelated
   user changes.
2. Run `go build ./... && go test ./...`. Stop on failure.
3. Choose the version by change type: fix, docs, or refactor only is a patch;
   a backward-compatible feature is a minor; a breaking change is a major.
   State the intended `vX.Y.Z` before any external mutation.
4. Push the verified commit first, then create and push the tag:
   `git tag -a vX.Y.Z -m "vX.Y.Z"` and `git push origin vX.Y.Z`. The workflow
   triggers on tag push only; pushing commits alone releases nothing.
5. Wait for the run for this tag to finish: `gh run list
   --workflow=release.yml --branch vX.Y.Z --limit 1 --json
   status,conclusion,headSha` until `status` is `completed`, and check that
   `headSha` matches `git rev-parse vX.Y.Z^{commit}`. A failed
   or cancelled run is a stop condition: read `gh run view <run-id>
   --log-failed`, report it, and do not install an unverified build.
6. Verify the release is public and lists `ach-darwin-arm64`,
   `ach-darwin-amd64`, and `SHA256SUMS`: `gh release view vX.Y.Z --json
   assets`.

To install the published release afterwards, use the `install` skill.
