---
name: release-and-install
description: "Publish a verified versioned CLI or app release, then install and verify the released artifact. Use for requests to release, tag, install, or upgrade a project after changes are ready."
---

# Release And Install

Release a version only after the requested changes are committed, the worktree
is understood, and relevant verification has passed. Inspect the repository's
own release definition before choosing a version or running commands; do not
invent a tag format, publishing tool, package registry, or installer.

## Release workflow

1. Inspect the working tree, recent tags, release workflow, build targets, and
   installer. Preserve unrelated user changes. Confirm the existing commit's
   Change-Id is unchanged when the repository uses them.
2. Run the checks appropriate to the changed code. Stop on failures.
3. Before any external mutation, state the intended version and release path.
   Creating a commit, pushing, tagging, publishing, and modifying a local
   installation each require the user's current request or explicit approval.
4. Use the repository's documented release trigger. If a tag triggers CI,
   push the verified commit first, then create and push the selected tag.
   Pick semantic version increments from the change type only when the
   project's versioning convention is established; otherwise ask.
5. Wait for the publishing job to finish and verify that the release is public
   and its expected artifacts are present. Treat a failed or cancelled job as
   a stop condition; report it rather than installing an unverified build.

## Install workflow

Use the project's installer when it completes reliably. Verify the installed
artifact, not merely that the installer started:

- Prefer a release-provided checksum, signature, or version command.
- If replacing an existing executable, preserve it as a timestamped backup
  unless the user explicitly requests otherwise.
- If the installer cannot finish in the current environment, download the
  same published artifact to a temporary directory, verify its checksum, and
  perform the documented replacement steps. Say that this fallback was used.
- Report the installed path, published version, verification evidence, and
  backup path when applicable.

Do not use this skill for deployments to servers, app stores, or registries
without an explicit request; use the relevant deployment workflow instead.
