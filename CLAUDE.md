# ags

Personal scheduler for coding agents on a Mac: a launchd daemon, a CLI
and a TUI, in Go. The specification is `docs/spec.md`, the landscape
review behind it `docs/veille.md`. The spec is the reference: change it
in the same branch when the implementation departs from it.

## Worktrees

```sh
bin/setup_worktree.sh <name> [branch]   # ../ags-worktrees/<name>, new branch from origin/main
```

## Checks

```sh
go build ./... && go vet ./... && go test ./...
```

Never touch the real `~/.config/ags`, `~/.local/state/ags` or the
installed LaunchAgent from tests or experiments: point the tool at a
temporary directory (`AGS_HOME`).

## QA

To prove a change on this worktree's build (sandboxed daemon, CLI, TUI
captures, `bin/qa` on the diff), use the `verify-ags` skill.
