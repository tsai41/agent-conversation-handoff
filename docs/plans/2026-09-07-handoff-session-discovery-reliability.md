---
execution:
  scope: be-only
  route: plan-execution-controller
  repos:
    - role: backend
      path: $HOME/go/src/agent-conversation-handoff
interfaces:
  contract: n/a
  flow:
    diagram: n/a
    scenarios: inline
    journey: inline
    coverage: all-mapped
  rollout:
    merge_order: n/a
    deploy_order: n/a
    flag: none
---

# Handoff Session Discovery Reliability

## Goal

Make the handoff source-session picker explain how many conversations it found, distinguish an empty project from a scan failure, handle special characters in project paths, and make installation and artifact publication more resilient.

## Scope and red lines

- Do not migrate, delete, or share provider `projects/` or `sessions/` data.
- Do not change provider authentication data or the handoff artifact format.
- Preserve direct conversation-ID lookup when project scanning is empty or fails.

## Current-state evidence

| Area | Current implementation | Gap |
|---|---|---|
| Candidate cap | `internal/session/session.go:34,112-126` | Returns only five candidates, losing total count. |
| Scan failure | `internal/menu/menu.go:346-349` | Converts every scan error to an empty list. |
| Claude path enumeration | `internal/session/session.go:101` | Ignores `filepath.Glob` errors for paths containing glob metacharacters. |
| Installer replacement | `install.sh:32-39` | Moves the existing binary before download and has no checksum validation. |
| Artifact naming | `internal/handoff/handoff.go:119-124` | Same source in the same second collides. |

## Implementation plan

1. Return candidate metadata: total readable sessions plus the capped rows; render `找到 N 筆，顯示最新 M 筆` in the source picker.
2. Preserve and display scan errors with the source home and project path; retain direct ID entry.
3. Replace Claude `filepath.Glob` enumeration with directory reads and add a special-character path regression test.
4. Download release assets to a temporary file, verify a release checksum manifest, then atomically replace the installed binary; make artifact destination allocation collision-safe.

## Scenarios

1. User selects a source with two sessions: picker says it found and displays two.
2. User selects a source with more than five sessions: picker says the total and displays five newest.
3. User selects a source with no project sessions: picker explains that condition and still accepts an ID.
4. Project paths containing glob metacharacters list their sessions.
5. Failed install leaves the previous binary runnable; invalid checksum does not install an asset.
6. Two identical handoffs created in one second both publish separate artifacts.

## Test plan

- Test first in `internal/session` and `internal/menu` for all session-count, empty, failure, and special-path cases.
- Add handoff collision tests and installer shell smoke coverage where practical.
- Before each release run `gofmt -d`, `go vet ./...`, and `make test`.

## Commit boundaries

1. `feat(session): report source session totals and scan status`
2. `fix(session): enumerate Claude sessions without glob paths`
3. `fix(install): verify release assets before replacement`
4. `fix(handoff): avoid artifact name collisions`
