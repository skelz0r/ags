# agent-scheduling: specification

A small personal scheduler for coding agents on a Mac. Register
projects, give each one jobs (a name, a schedule, a prompt), and a
daemon runs a command with that prompt at the right time. A TUI lists
every project and its jobs; a CLI and a few Claude Code skills let an
agent edit and check the config. The landscape behind these choices is
in [veille.md](veille.md).

The model is Orca's automations (name, prompt, provider, precheck)
with one change: instead of a provider, a job runs a **command**,
defined once in the machine config. In practice that command is the
Heraldr dispatcher, which hands the prompt to the project's `-main`
Claude in herdr, which then explores and dispatches through `/start` as
usual. `claude -p` is just another command.

## Decisions

- **Build a small tool**, do not extend DnzzL/herdr-automations: its
  scope is one global YAML and it starts agents itself. Here the tool
  only schedules and runs a command; how the agent is started belongs
  to the command.
- **Command, not provider.** Named commands live in the machine config;
  a job references one by name or uses the default. Changing how runs
  are started (Heraldr dispatcher, `claude -p`, Codex later) never
  touches the projects.
- **The command only dispatches.** A run is over when the command exits.
  With the Heraldr dispatcher that is a few seconds: the work then
  happens in herdr under the usual conventions (queue, `-main` agent,
  `/start` worktree and tab, push notification on questions). The
  scheduler does not follow the agent.
- **Go**, single binary: launchd runs it with a minimal environment, a
  binary without runtime is the safest thing there. `robfig/cron/v3`
  for cron with timezones, `yaml.v3` for line numbers in errors and
  comment-preserving edits, bubbletea for the TUI.
- **One launchd agent, one long-lived daemon** reading every registered
  project, rather than one plist per job: catch-up, overlap and history
  need one place that knows every job, and launchd calendar intervals
  cannot express every cron.
- **A standalone TUI** for now; a herdr plugin (board overlay, run now)
  can come later on top of the same CLI.

## Concepts

- **Project**: a registered directory with a slug (its basename by
  default, unique). Usually a git checkout; `~` works too, for jobs that
  are not tied to a repo (they go to the Home workspace through the
  dispatcher).
- **Job**: name, schedule, prompt, optional command and precheck.
  Addressed as `<project>/<job>`.
- **Command**: a named argv template in the machine config.
- **Run**: one execution of a job's command, scheduled or manual.

## Files

| Path | Content |
|---|---|
| `<project>/.agent-scheduling.yml` | the project's jobs, versioned with the repo |
| `~/.config/agent-scheduling/config.yml` | commands, default command, notifications |
| `~/.config/agent-scheduling/projects.yml` | registry of projects (written by the CLI and TUI) |
| `~/.local/state/agent-scheduling/` | `state.json` (cursors), `runs/`, `history.jsonl`, `daemon.log`, `daemon.lock` |
| `~/Library/LaunchAgents/dev.agent-scheduling.plist` | the launchd agent, written by `ags install` |

Names (`.agent-scheduling.yml`, `ags`) are provisional.

## Machine config

```yaml
default_command: heraldr

commands:
  heraldr:
    argv: ["~/dotfiles/bin/heraldr-dispatch", "relay", "--cwd", "{project.path}"]
    stdin: prompt
  claude:
    argv: ["claude", "-p", "--model", "claude-opus-5-5", "--output-format", "json"]
    stdin: prompt
    cwd: "{project.path}"
    timeout: 45m

notifications:
  on: [failed, missed, precheck_error, invalid]
  channels: [macos]
```

Command fields:

| Field | Default | Meaning |
|---|---|---|
| `argv` | required | program and arguments; `~` expanded; placeholders `{project.path}`, `{project.name}`, `{job.name}`, `{run.id}`, `{run.scheduled_for}` |
| `stdin` | `prompt` | `prompt` (the rendered prompt on stdin) or `none` |
| `prompt_arg` | | alternative to stdin: the prompt is substituted where `{prompt}` appears in `argv` |
| `cwd` | project path | working directory |
| `env` | `{}` | extra environment |
| `timeout` | `5m` | the process is killed (SIGTERM then SIGKILL on its group) past it |

Every command also gets `AGS_PROJECT`, `AGS_PROJECT_PATH`, `AGS_JOB`,
`AGS_RUN_ID`, `AGS_SCHEDULED_FOR` and `AGS_TRIGGER` (`schedule`,
`catch_up`, `manual`) in its environment.

The Heraldr dispatcher today takes a herdr workspace id
(`dispatch.sh relay <workspace_id>`). When it moves to the dotfiles it
needs to accept a path and resolve the workspace itself (the one whose
cwd is the path, `~` meaning Home), so that the scheduler never knows
about herdr. That interface is the dispatcher's, defined when it moves.

## Project config: `.agent-scheduling.yml`

```yaml
version: 1
timezone: Europe/Paris

jobs:
  security-audit:
    schedule: "30 9 * * 1"
    prompt: |
      Weekly security audit. Run /security-review on the commits of the
      last 7 days, report only what is new compared to the last open
      issue labelled security-audit, and open one issue if there is
      something. If there is nothing, say so and stop.
    precheck: test -n "$(git log --since='7 days ago' --oneline origin/main)"

  test-audit:
    schedule: "0 10 1,15 * *"
    prompt_file: .agent-scheduling/test-audit.md

  code-audit:
    schedule: "0 14 1 * *"
    command: claude
    prompt_file: .agent-scheduling/code-audit.md
    enabled: false
```

Fields:

| Field | Default | Meaning |
|---|---|---|
| `version` | required | `1` |
| `timezone` | machine timezone | IANA name, for every job of the file |
| `jobs.<name>` | | `[a-z0-9-]{1,40}` |
| `schedule` | required | 5-field cron or `@hourly`, `@daily`, `@weekly`, `@monthly` |
| `prompt` / `prompt_file` | one required | inline text, or a path relative to the project |
| `command` | `default_command` | name of a command of the machine config |
| `precheck` | | shell command run in the project before the job |
| `catch_up` | `12h` | how late a missed occurrence may still run, `never` to disable |
| `enabled` | `true` | |

Unknown keys are errors. The prompt can use `{project.name}`,
`{job.name}`, `{run.scheduled_for}` and `{run.late_by}`; nothing else
is templated.

A project file holds no command, only a command name: whatever a repo
contains, it cannot choose what runs on the machine beyond its prompt
and its precheck.

## TUI

`ags` with no argument opens it.

- **Projects** (home screen): every registered project with its number
  of jobs, the next run and the last failure. `a` add a project (path
  prompt, `~` allowed), `d` remove it (with confirmation, history
  kept), `enter` open it.
- **Jobs of a project**: name, schedule in words ("Mondays 09:30"),
  command, next run, last run and its status, paused state. `n` new job,
  `e` edit (opens `$EDITOR` on the file at the job's line), `d` delete
  (with confirmation), `p` pause / resume, `r` run now, `enter` runs of
  the job.
- **Runs**: all projects or one job, newest first: time, trigger,
  status, duration, exit code; `enter` shows the rendered prompt, the
  precheck output and the command's stdout and stderr.
- A status bar shows whether the daemon is alive and its last tick, and
  config errors with file and line.

Edits made from the TUI (new job, delete, pause) are written to the
project file through the YAML node tree, so comments and order are
kept, and the result is validated before it replaces the file. Pause
state is the `enabled` key, so it is versioned like the rest.

## CLI

Everything the TUI does is also a command, for scripts and skills. All
accept `--json` and then print `{"ok": bool, "data": ..., "error":
{"code", "message"}}`. Exit codes: 0 ok, 1 error, 2 invalid config or
usage.

- `ags add <path> [--name slug]`, `ags remove <project>`,
  `ags projects`
- `ags validate [path]`: schema, prompt files present, command names
  known, next 3 occurrences of each job. Works on an unregistered path.
- `ags jobs [--project p]`: jobs of all projects, next and last run.
- `ags next <project/job> [-n 5]`
- `ags run <project/job> [--skip-precheck] [--dry-run]`: run now;
  `--dry-run` prints the precheck, the rendered prompt and the exact
  argv without running anything.
- `ags runs [--project p] [--job j] [--status s] [--since 7d]`,
  `ags show <run>`
- `ags pause <project[/job]>`, `ags resume <project[/job]>`
- `ags install`, `ags uninstall`: LaunchAgent (with the PATH of the
  installing shell) and skills linked into `~/.claude/skills`.
- `ags daemon`: the foreground loop launchd runs.
- `ags doctor`: LaunchAgent loaded, daemon alive, last tick, every
  project path present and its file valid, every command's program
  found.

`ags run`, `ags add` and the editing commands refuse to run when
`AGS_RUN_ID` is set, so a scheduled run cannot schedule more work.

## Daemon

- launchd `RunAtLoad` and `KeepAlive`; single instance through `flock`
  on `daemon.lock`.
- A tick every 30 s on the wall clock (macOS suspends monotonic timers
  during sleep). Each tick reloads the registry and any project file
  whose mtime changed, fires due jobs, reaps finished commands.
- An invalid project file keeps its last valid version in use; the
  error is recorded once, notified, and shown in the TUI until fixed.
- After a wake (a gap of more than two ticks) firing waits 60 s, for
  the network and herdr to come back.
- Commands run concurrently up to 4 at a time; a command running past
  its `timeout` is killed and the run marked `failed`.
- On restart, runs left `running` are marked `failed` with reason
  `interrupted`.

## Schedule semantics

- One cursor per job, the last occurrence decided on, persisted before
  the command starts: a crash means "not run", never "run twice".
- A new job starts at its next occurrence; nothing fires for the past.
- On each tick the latest occurrence between the cursor and now is the
  candidate; earlier ones are recorded as a single `missed` entry with
  their count.
- Up to two ticks late is on time. Later but within `catch_up`, the run
  fires with trigger `catch_up` and its lateness is available to the
  prompt (a Monday 09:30 audit caught up at 18:00 knows it). Beyond
  `catch_up` it is recorded `missed`.
- One run per job at a time: an occurrence due while the job's command
  is still running is recorded `skipped` (`overlap`). With the Heraldr
  dispatcher this never happens, since the command returns at once;
  the dispatcher's own queue keeps the agent from being interrupted.
- DST: a nonexistent time is skipped, a repeated hour fires once.

## Precheck

- A shell command run in the project directory, with the same `AGS_*`
  environment, timeout 60 s.
- Exit 0: the job runs. Exit 1: `skipped` (`precheck`), its stdout kept
  as the reason. Anything else or a timeout: `precheck_error`, not run,
  notified.
- `ags run` goes through it too unless `--skip-precheck`.

## Runs and history

- Statuses: `running`, `succeeded` (command exited 0), `failed`,
  `skipped` (`precheck`, `overlap`, `paused`), `precheck_error`,
  `missed`.
- A run records project, job, trigger, scheduled time, lateness, start
  and end, the command name and argv, exit code, the first 64 KiB of
  stdout and stderr, and the precheck output. With the Heraldr
  dispatcher, stdout is the dispatcher's job id, which links the run to
  its Heraldr job.
- One directory per run under `runs/<project>/<job>/<run id>/`
  (`run.json`, `prompt.md`, `stdout`, `stderr`, `precheck.log`) and an
  append-only `history.jsonl` at the state root.
- Keeps the last 100 runs per job; older run directories are deleted
  once a day.
- Notifications on `failed`, `missed`, `precheck_error` and config
  errors, through macOS notifications; one notification per distinct
  error, not per tick. A successful dispatch does not notify: the
  agent's own notifications take over from there.

## Skills

Shipped in `skills/`, linked by `ags install`, prefixed `ags-` to stay
clear of Claude Code's `/schedule` and `/loop`.

- **`ags-config`**: add or change jobs in the project's
  `.agent-scheduling.yml` from a request ("audit the tests every other
  Monday"): read the file, keep the other jobs untouched, translate the
  schedule into cron, write a prompt that says what to do when there is
  nothing to report, add a precheck when one avoids empty runs, then run
  `ags validate --json` until it passes and show `ags next`.
- **`ags-run`**: `ags run --dry-run` to check the prompt and argv, then
  run for real, and report the run status and where the work went
  (dispatcher job id, herdr tab).
- **`ags-doctor`**: answer "why did this job not run": `ags doctor`,
  `ags jobs`, `ags runs --since`, `ags show`, read skip reasons, missed
  counts and precheck output, propose the fix.

## Security

Unattended runs end up in agents that usually run with
`--dangerously-skip-permissions` (the `-main` agent, then `/start`
sessions launched with `dclaude`). Nobody watches when a prompt reads a
poisoned issue body or a dependency README. This spec does not try to
sandbox that; the decision is deliberate, for simplicity. What it keeps:

- commands are defined only in the machine config, a project file names
  one but cannot define one;
- a scheduled run cannot register or trigger other runs;
- prompts of audits are better written read-only ("report, open an
  issue, do not push"), which the `ags-config` skill does by default.

The permission profiles, sandbox settings and output executor explored
in the landscape review (GitHub Agentic Workflows, Claude Code
`dontAsk`/`auto` and sandbox) remain the way to go if this ever needs
hardening; they can live entirely in a command.

## Out of scope

- Following the agent after dispatch (outcome, findings, cost): the
  command's exit code is the run's result.
- Worktrees: the dispatcher's `/start` creates them, a `claude -p`
  command can pass `-w`.
- Permission profiles, sandboxing, trust of project files, outputs
  applied by the tool.
- Event triggers, remote machines, Linux and Windows.
- Jitter, global concurrency tuning, quota-aware scheduling, waking the
  Mac.
- A herdr plugin (board, run-now action): later, on top of the CLI.
- Any dependency on Heraldr itself: the dispatcher is just the default
  command, moved to the dotfiles.

## Open questions

1. Final names of the tool, CLI and project file.
2. Interface of the dispatcher once in the dotfiles: path instead of
   workspace id, `relay` envelope (explore, ask, `/start`) or raw
   prompt (`enqueue`), and what it does when the project has no herdr
   workspace open.
3. Should a job be able to choose between `relay` (the `-main` agent
   explores and dispatches) and a direct prompt, through two commands
   in the machine config?
4. Default `catch_up` of 12 h, or shorter?
5. `.agent-scheduling.yml` for `~`: is `~/.agent-scheduling.yml` fine,
   or should the root project's jobs live in the dotfiles?
