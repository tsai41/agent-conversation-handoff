---
name: install
description: "Install the published agent-conversation-handoff release with install.sh and verify the installed binary against the release checksum. Use when the user asks to install, upgrade, or update the local command."
---

# Install

Install the published release, not a local build. `install.sh` downloads the
current-platform asset (`ach-darwin-arm64` or `ach-darwin-amd64`) and its
`SHA256SUMS`, checks the checksum, backs up an existing regular file at the
destination to `<path>.bak-$(date +%Y%m%d-%H%M%S)`, and installs the new one.

## Workflow

1. Find the command name the user already uses. Keep it: pass it as the
   `COMMAND` environment variable. If none is established, the default is
   `ach`. `BIN_DIR` defaults to `~/bin`.
2. Run `COMMAND=<name> ./install.sh` from the repository, or `make install
   COMMAND=<name>`. Both download the latest release; neither builds locally.
3. Verify the installed file, not just that the installer ran: compute
   `shasum -a 256 <installed-path>` and compare it with the matching line of
   the release's `SHA256SUMS`. Download it with `d=$(mktemp -d); gh release
   download --repo tsai41/agent-conversation-handoff --pattern SHA256SUMS
   --dir "$d"` and read `"$d/SHA256SUMS"`, or fetch
   `https://github.com/tsai41/agent-conversation-handoff/releases/latest/download/SHA256SUMS`.
4. If the installer cannot finish in the current environment, download the
   same asset and `SHA256SUMS` to a temporary directory, verify the checksum,
   back up the existing executable to `<path>.bak-$(date +%Y%m%d-%H%M%S)`
   (the name `ach uninstall` recognises and cleans up), then move the verified
   file into place. Say that this fallback was used.
5. Report the installed path, the published release version, the checksum
   result, and the backup path when one was made.

An existing symlink at the destination is replaced without a backup; mention
it if the destination was a symlink.
