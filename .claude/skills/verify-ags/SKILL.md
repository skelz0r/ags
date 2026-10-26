---
name: verify-ags
description: Prove that a change to ags (the launchd daemon, the CLI and the TUI of this repository) works on this worktree's build without touching the real install - run the tests the diff reaches with bin/qa, launch an isolated sandbox (own AGS_HOME, HOME, daemon, fake agent commands, no notifications), capture CLI and TUI screens, check side effects, clean up. Use before saying a change to ags works, when asked to "vérifie", "teste en vrai", "lance le daemon", "montre la TUI", or when a result looks off.
---

# verify-ags

Everything below runs from the worktree root. The real install
(`~/.config/ags`, `~/.local/state/ags`, `~/Library/LaunchAgents/dev.ags.plist`,
the loaded `dev.ags`) is never written: every QA command goes through
the sandbox wrapper `tmp/qa/sandbox/ags`, never through a bare `ags`
(the one in PATH is the real install).

macOS only: the doctor's real-install check reads the LaunchAgent and
the default `~/.config` and `~/.local/state`, with no `XDG_*` set. On
Linux, or with XDG variables set, it does not prove isolation: check
by hand, or test in a disposable VM.

## Launch

```sh
.claude/skills/verify-ags/scripts/sandbox up
```

Builds `./cmd/ags` of this worktree into `tmp/qa/sandbox/bin/ags`, then
on first run seeds `tmp/qa/sandbox/`:

- `agshome/` as `AGS_HOME`, `home/` as `HOME` (the wrapper sets both);
- a machine config whose commands are `sh`/`sleep` stand-ins (`record`
  echoes its stdin, `failing` exits 3, `slow` sleeps 20s) and
  `notifications.channels: []`;
- `ags install` (LaunchAgent written under `agshome/LaunchAgents`, not
  loaded since `AGS_HOME` is set), project `agsqa` (`proj/`, its own git
  repo) approved, with jobs `every-minute` (cron `* * * * *`, the daemon
  runs it within ~90s), `audit`, `broken`, `nothing-to-do`, and one
  manual run of the last three (succeeded, failed exit 3, skipped by
  precheck);
- `ags daemon` in the background, pid in `daemon.pid`, log
  `agshome/state/daemon.log`.

Rerun `up` after every code change: it rebuilds and restarts the daemon,
keeping the seeded state. Usable means the doctor passes, not only that
the pid exists.

## Doctor

```sh
.claude/skills/verify-ags/scripts/doctor
```

Read-only, one line per check, `FAIL ... -> fix: <command>`. Run it after
`up`, before any proof and whenever a result looks off.

- `build`: the binary was built from this worktree's HEAD and current
  uncommitted sources (`sandbox fingerprint`), so a proof never runs
  stale or another checkout's code.
- `isolation`: the wrapper points `AGS_HOME` at the sandbox.
- `daemon`: the pid the sandbox started runs `tmp/qa/sandbox/bin/ags
  daemon` and is the pid the sandbox state names.
- `ags *`: the sandbox's own `ags doctor` (service file written, not
  loaded, daemon ticking, machine config, project approved).
- `outbound command *`: every machine command resolves to `sh`, `echo`,
  `cat`, `sleep`, `true` or `false`: no real agent (claude, codex) can
  start, so no cost and no repository touched.
- `outbound notifications`: `channels: []`, no macOS notification.
- `real install`: the real config, registry and plist do not mention
  the sandbox, the real history has no `agsqa` run, the loaded `dev.ags`
  is not the sandbox binary.
- `tests isolation`: no test calls `cli.Main`, `notify.Desktop` or
  `paths.FromEnv`: tests go through `agstest.New(t)`.
- `tmux`: needed for TUI captures.

## Driving it

- CLI: `tmp/qa/sandbox/ags <command> [--json]`, e.g. `runs`, `jobs
  --project agsqa`, `run agsqa/audit --dry-run`, `show <run id>`,
  `validate tmp/qa/sandbox/proj`. To test a config change, edit
  `tmp/qa/sandbox/proj/.ags.yml` or `.ags.local.yml` (the daemon reloads
  it) or `agshome/config/config.yml` (keep commands to stand-ins and
  `channels: []`: the doctor fails otherwise).
- TUI: `.claude/skills/verify-ags/scripts/sandbox tui <name> [keys...]`
  opens it in a detached tmux (160x32, own socket), sends the keys
  (tmux names: `Enter`, `Escape`, `Down`, letters), saves the screen as
  text in `tmp/qa/evidence/tui/<name>.txt`. Screens: projects (start),
  `Enter` jobs of the selected project, `R` all runs, `Enter` on a run its
  detail, `A` approve, `r` run now, `p` pause, `e` edit.
- Time: the daemon ticks every 30s on the real clock. Scheduling logic
  (catch-up, DST, missed runs) is proven by the tests, which drive the
  clock (`agstest.World.Tick`), not by waiting in the sandbox.

## Proof, in this order

1. `bin/qa` on the diff (default: merge-base with `origin/main` to the
   working tree; `bin/qa <commit>` for one commit; `--all`). It runs every
   test package whose dependencies include a touched package (embedded
   `skills/` files belong to the root package; `go.mod`/`go.sum` select
   everything; no Go file touched falls back to `go test ./...`), prints
   why each was picked, the touched packages no test reaches, and the
   tests naming a changed exported func. `QA_SCREENSHOTS=1` also captures
   `doctor`, `projects`, `jobs`, `runs` and four TUI screens of the
   sandbox (started and stopped by `bin/qa` if it was not up). Read the 2
   or 3 captures the change shows.
2. A behaviour no test covers in `internal/cli` or `internal/daemon`:
   write the test with `agstest` (`New`, `SharedFile`, `Register`,
   `CLI`, `Daemon`, `Tick`, `Runs`, `Notes.Count`), it stays as the
   proof. `internal/tui` and `cmd/ags` have no test: captures are their
   proof.
3. By hand in the sandbox only what tests cannot see: the real launchd
   daemon loop and its 30s tick, real processes (timeouts, kill of a
   stubborn child), the TUI rendering, files on disk. Check the side
   effect, not only the screen: the run directory
   (`agshome/state/runs/agsqa/<job>/<id>/` with `prompt.md`, `stdout`,
   `stderr`, `precheck.log`), `agshome/state/history.jsonl`,
   `state.json`, the edited `.ags.yml`, `daemon.log`.

`ags install` against the real LaunchAgent (launchctl bootstrap) is
never part of a QA: only the user runs it, from the main checkout.

## Evidence

`tmp/qa/evidence/` (gitignored, survives cleanup): one directory per
`bin/qa` run (`changed-files.txt`, `go-test.log` in `-v`, captures),
`tui/` for hand captures. Quote the relevant lines in the report.

## Cleanup

```sh
.claude/skills/verify-ags/scripts/sandbox down
```

Stops the daemon by its pid file after checking the pid still runs the
sandbox binary, kills the sandbox's own tmux socket, deletes
`tmp/qa/sandbox`, keeps `tmp/qa/evidence`. Never `pkill ags`: the real
daemon runs under the same name.
