# agent-scheduling: specification

A small personal scheduler for coding agents on a Mac. Register
projects, give each one jobs (a name, a schedule, a prompt), and a
daemon runs a command with that prompt at the right time. A TUI lists
every project and its jobs; a CLI and a few Claude Code skills let an
agent edit and check the config. The landscape behind these choices is
in [veille.md](veille.md).

The model is Orca's automations (name, prompt, provider, precheck)
with one change: instead of a provider, a job runs a **command**,
defined once in the machine config. `claude -p` is one command; a
script that hands the prompt to an agent already running in a terminal
multiplexer is another. The tool does not know or care which.

The project file says *what* to do, in words that hold for anyone who
clones the repo. *How* (which agent, which skills, which issue
tracker) belongs to each developer: their machine config (command and
preprompt) and an unversioned `.agent-scheduling.local.yml`.

## Decisions

- **Build a small tool**, do not extend DnzzL/herdr-automations: its
  scope is one global YAML and it starts agents itself. Here the tool
  only schedules and runs a command; how the agent is started belongs
  to the command.
- **Command, not provider.** Named commands live in the machine config;
  a job references one by name or uses the default. Changing how runs
  are started never touches the projects.
- **A run is over when the command exits.** A `claude -p` command runs
  the whole job; a command that only hands off to another agent returns
  in seconds. The scheduler does not follow the agent after that.
- **Shared intent, personal setup.** The versioned project file is
  agnostic of the developer's stack; preprompts and the local file
  adapt it to one machine.
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
  are not tied to a repo.
- **Job**: name, schedule, prompt, optional command and precheck.
  Addressed as `<project>/<job>`.
- **Command**: a named argv template in the machine config.
- **Preprompt**: text put before every job's prompt, at machine,
  project or local level.
- **Run**: one execution of a job's command, scheduled or manual.

## Files

| Path | Content | Versioned |
|---|---|---|
| `<project>/.agent-scheduling.yml` | the project's jobs and preprompt | with the repo |
| `<project>/.agent-scheduling.local.yml` | personal overrides and jobs | no, gitignored |
| `~/.config/agent-scheduling/config.yml` | commands, default command, preprompt, notifications | in the dotfiles if wanted |
| `~/.config/agent-scheduling/projects.yml` | registry of projects (written by the CLI and TUI) | no |
| `~/.local/state/agent-scheduling/` | `state.json` (cursors), `runs/`, `history.jsonl`, `daemon.log`, `daemon.lock` | no |
| `~/Library/LaunchAgents/dev.agent-scheduling.plist` | the launchd agent, written by `ags install` | no |

Names (`.agent-scheduling.yml`, `ags`) are provisional.

## Machine config

```yaml
default_command: claude

preprompt: |
  This run was started by a scheduler; nobody is watching. Do not ask
  questions: if something blocks you, stop and say why. Open issues
  with gh. For a security audit, use the /security-review skill.

commands:
  claude:
    argv: ["claude", "-p", "--model", "claude-opus-5-5", "--output-format", "json"]
    stdin: prompt
    timeout: 45m
  handoff:
    argv: ["~/dotfiles/bin/dispatch", "relay", "--cwd", "{project.path}"]
    stdin: prompt

notifications:
  on: [failed, missed, precheck_error, invalid]
  channels: [macos]
```

`handoff` stands for any script that gives the prompt to an agent
already running elsewhere and exits; its interface is its own business.

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

## Project config: `.agent-scheduling.yml`

```yaml
version: 1
timezone: Europe/Paris

preprompt: |
  The main branch is main. Issues of scheduled audits carry the label
  of the job name.

jobs:
  security-audit:
    schedule: "30 9 * * 1"
    prompt: |
      Audit the security of the commits of the last 7 days. Report only
      what is new compared to the last open issue labelled
      security-audit, and open one issue if there is something. If
      there is nothing, say so and stop. Do not change any code.
    precheck: test -n "$(git log --since='7 days ago' --oneline origin/main)"

  test-audit:
    schedule: "0 10 1,15 * *"
    prompt_file: .agent-scheduling/test-audit.md

  code-audit:
    schedule: "0 14 1 * *"
    prompt_file: .agent-scheduling/code-audit.md
    enabled: false
```

Fields:

| Field | Default | Meaning |
|---|---|---|
| `version` | required | `1` |
| `timezone` | machine timezone | IANA name, for every job of the file |
| `preprompt` / `preprompt_file` | | text put before every job's prompt of the project |
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
contains, it cannot choose what program runs on the machine.

## Local config: `.agent-scheduling.local.yml`

Same schema, `version` optional, never versioned. It is read after the
project file and merged into it:

- `timezone` replaces the project's;
- `preprompt` is added after the project's;
- a job that exists in the project file gets its fields replaced key by
  key (`command`, `schedule`, `enabled`, `precheck`, `catch_up`,
  `prompt`);
- a job that does not exist there is a personal job and needs a full
  definition.

```yaml
preprompt: |
  Dispatch any fix through /start, never on the main checkout.

jobs:
  security-audit:
    command: handoff
  code-audit:
    enabled: true
  deps-triage:
    schedule: "0 9 * * 5"
    prompt: Triage the open dependency update pull requests.
```

`ags validate` and `ags doctor` warn when the local file is not ignored
by git (`git check-ignore`).

## Prompt rendering

The prompt sent to the command is, separated by blank lines: the
machine preprompt, the project preprompt, the local preprompt, then the
job's prompt, templated. `ags run --dry-run` prints it exactly.

## TUI

`ags` with no argument opens it.

- **Projects** (home screen): every registered project with its number
  of jobs, the next run and the last failure. `a` add a project (path
  prompt, `~` allowed), `d` remove it (with confirmation, history
  kept), `enter` open it.
- **Jobs of a project**: name, schedule in words ("Mondays 09:30"),
  command, origin (shared or local), next run, last run and its status,
  paused state. `n` new job, `e` edit (opens `$EDITOR` on the file at
  the job's line), `d` delete (with confirmation), `p` pause / resume,
  `r` run now, `enter` runs of the job.
- **Runs**: all projects or one job, newest first: time, trigger,
  status, duration, exit code; `enter` shows the rendered prompt, the
  precheck output and the command's stdout and stderr.
- A status bar shows whether the daemon is alive and its last tick, and
  config errors with file and line.

Edits are written through the YAML node tree, so comments and order are
kept, and the result is validated before it replaces the file. Pause
and resume write `enabled` to the local file: pausing a job is personal
and does not touch the repo. A new job asks whether it is shared or
local; delete edits the file the job comes from.

## CLI

Everything the TUI does is also a command, for scripts and skills. All
accept `--json` and then print `{"ok": bool, "data": ..., "error":
{"code", "message"}}`. Exit codes: 0 ok, 1 error, 2 invalid config or
usage.

- `ags add <path> [--name slug]`, `ags remove <project>`,
  `ags projects`
- `ags validate [path]`: schema of both files, prompt files present,
  command names known, local file ignored, next 3 occurrences of each
  job. Works on an unregistered path.
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
  project path present and its files valid, every command's program
  found.

`ags run`, `ags add` and the editing commands refuse to run when
`AGS_RUN_ID` is set, so a scheduled run does not schedule more work by
accident.

## Daemon

- launchd `RunAtLoad` and `KeepAlive`; single instance through `flock`
  on `daemon.lock`.
- A tick every 30 s on the wall clock (macOS suspends monotonic timers
  during sleep). Each tick reloads the registry and any project or
  local file whose mtime changed, fires due jobs, reaps finished
  commands.
- An invalid file keeps the last valid version of the project in use;
  the error is recorded once, notified, and shown in the TUI until
  fixed.
- After a wake (a gap of more than two ticks) firing waits 60 s, for
  the network to come back.
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
  is still running is recorded `skipped` (`overlap`). A hand-off command
  returns at once, so overlap after the hand-off is the receiving
  agent's concern.
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
  stdout and stderr, and the precheck output. A hand-off command can
  print an id on stdout to link the run to whatever it handed off to.
- One directory per run under `runs/<project>/<job>/<run id>/`
  (`run.json`, `prompt.md`, `stdout`, `stderr`, `precheck.log`) and an
  append-only `history.jsonl` at the state root. The state directory
  is created `0700`.
- Keeps the last 100 runs per job; older run directories are deleted
  once a day.
- Notifications on `failed`, `missed`, `precheck_error` and config
  errors, through macOS notifications; one notification per distinct
  error, not per tick. A succeeded run does not notify.

## Skills

Shipped in `skills/`, linked by `ags install`, prefixed `ags-` to stay
clear of Claude Code's `/schedule` and `/loop`.

- **`ags-config`**: add or change jobs from a request ("audit the tests
  every other Monday"): ask or infer whether the job is shared or
  local, keep the other jobs untouched, translate the schedule into
  cron, write a shared prompt without naming a tool of this machine
  (that goes to the preprompts), say what to do when there is nothing
  to report, add a precheck when one avoids empty runs, then run `ags
  validate --json` until it passes and show `ags next`.
- **`ags-run`**: `ags run --dry-run` to check the prompt and argv, then
  run for real, and report the run status and the command's output.
- **`ags-doctor`**: answer "why did this job not run": `ags doctor`,
  `ags jobs`, `ags runs --since`, `ags show`, read skip reasons, missed
  counts and precheck output, propose the fix.

## Security

What runs without anyone watching, and who decides it:

| What runs | Decided by | Where |
|---|---|---|
| the command (the program) | you | machine config only |
| the precheck (a shell line) | whoever can commit to the repo, or you | project or local file |
| the prompt, then whatever the agent does with it | whoever can commit to the repo, you, and any text the agent reads | files, then issues, code, dependencies |

What follows from it:

- **Registering a project trusts its file.** Nothing runs for a repo
  until `ags add`; after that, a `git pull` that changes a job or a
  precheck changes what runs on the next occurrence, with no review.
  A precheck is a shell line executed by the daemon itself, like a git
  hook or a Makefile target. For a shared repo, that means every
  collaborator who can push can run code and prompts on your machine.
- **The agent is the real exposure.** A scheduled agent usually runs
  with `--dangerously-skip-permissions` or close to it, with your
  credentials (gh token, SSH keys, cloud CLIs). A prompt injected
  through an issue body, a PR comment or a dependency README can make
  it push, open issues, or send files out, and nobody sees it happen.
- **The tool does not sandbox it.** That is deliberate. Restricting the
  agent is the command's job: `claude -p` takes `--permission-mode
  dontAsk` with `--allowedTools`, `--disallowedTools`, sandbox
  settings and `--max-budget-usd`; the landscape review has the
  details. A cautious command is just another entry in the machine
  config.

What the tool guarantees, and nothing more:

- a repo cannot define the program that runs, only name a command of
  your machine config;
- `ags` refuses to add or trigger work from inside a run (`AGS_RUN_ID`
  set): that prevents accidents, not an agent determined to edit the
  YAML;
- run directories, which hold prompts and outputs, are readable by you
  only.

## Out of scope

- Following the agent after the command exits (outcome, findings,
  cost): the command's exit code is the run's result.
- Worktrees: the command's business (`claude -p -w`, or the agent the
  prompt is handed to).
- Permission profiles, sandboxing, approval of project files, outputs
  applied by the tool.
- Event triggers, remote machines, Linux and Windows.
- Jitter, global concurrency tuning, quota-aware scheduling, waking the
  Mac.
- A herdr plugin (board, run-now action): later, on top of the CLI.
- Any personal dispatcher or hand-off script: it is a command like any
  other, maintained outside this project.

## Open questions

1. Final names of the tool, CLI and project file.
2. Approve changes of the shared file: when a pull changes a job's
   prompt or precheck, should the job wait until it is accepted in the
   TUI (a hash stored in the state)? Cheap, and it closes the
   "collaborator pushes a job" case; without it, registering a shared
   repo means trusting every pusher.
3. Should a command be able to carry its own preprompt (a `claude -p`
   command needs "do not ask questions", a hand-off to an interactive
   agent does not)?
4. Default `catch_up` of 12 h, or shorter?
5. Jobs of `~`: `~/.agent-scheduling.yml` with no local file, or should
   they live in the dotfiles?
