---
name: development-guide
description: "Develop, test, ship, and install changes in agent-conversation-handoff with its established Go, GitHub Release, and account-session conventions. Use for implementation, bugfix, maintenance, or release requests in this repository."
---

# Development Guide

Work from the current repository state. Do not ask for routine implementation
choices when the request and existing code establish a clear direction; inspect,
make the smallest coherent change, test it, and report the result.

## Implementation

- Preserve unrelated user changes. Never reset, restore, or rewrite them.
- This is a Go project. Follow existing package boundaries and run `gofmt` for
  changed Go files.
- Use a failing automated test before changing behavior. For a regression,
  first reproduce the reported behavior or gather evidence that identifies the
  responsible boundary.
- The interactive menu uses `fzf`; test full flows through its fake harness in
  `internal/menu/menu_test.go` when a change affects navigation or launch
  arguments.
- Account identities and their provider session directories are separate.
  Never copy, overwrite, or delete provider session history to make resume
  work. A cross-account or cross-provider conversation uses handoff, not
  resume.
- Provider launch behavior must preserve the selected account environment and
  the user's project-specific shell wrapper. Do not bypass it with an ad hoc
  binary invocation.
- Tests never touch the real home directory. Use `t.Setenv("HOME",
  t.TempDir())` and pass explicit registry paths. For `uninstall.Run`, also
  inject `Options.Executable` and `Options.TempDir` so the run never looks
  at the test binary or the real temp directory. Registry, share-settings
  and trust-sync tests follow the same rule.
- Every account list goes through `registry.DisplayOrder` or
  `registry.Rows`, so archived accounts sort last. Do not sort accounts by ID
  string: `claude-10` would come before `claude-2`.
- `ach usage record` runs inside a status line. It prints nothing to stdout
  and exits 0 on unusable input; only a failed snapshot write may fail it.

## Completion

- Run focused tests during development, then `go test ./...` before handoff.
- When the user asks to commit, save, ship, release, or install, complete the
  full applicable path without repeated confirmation: conventional commit,
  push, next appropriate semantic tag, wait for the GitHub release workflow,
  verify the published artifact, and install it.
- Use the `release` skill to publish and the `install` skill to install.
  Amend fixes into the original commit only when the user requests a fixup.
- For release installers, verify the current-platform artifact against its
  published checksum. Keep a timestamped backup before replacing an existing
  executable. If the installer cannot complete in the execution environment,
  use the verified release artifact as the fallback and state that in the
  report.

Ask only when the requested outcome is genuinely ambiguous, conflicts with
existing changes, or needs authority beyond the user's request.
