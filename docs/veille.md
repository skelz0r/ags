# Landscape review: scheduling coding agents on a Mac

Snapshot taken on 2026-10-22. Stars, push dates and archive state come
from the GitHub API. Behaviour comes from READMEs and docs unless a line
says "read in code". Anything marked *unverified* was not confirmed.

## TL;DR

- Nothing on the market matches the target: per-project versioned
  config, a registry of projects, a precheck, herdr-visible runs and a
  safe unattended permission model. Every tool covers two or three of
  these.
- DnzzL/herdr-automations is well built but its scope is explicitly
  "one global YAML, the plugin never runs your prompt itself, no
  notification on success". Per-project config, headless runs and an
  outputs policy go against its README and ADRs. Contributing is only
  worth it for two bugs found while reading its code (see its card).
- huketo/herdr-cron has the best agent-facing design (JSON CLI, stable
  error codes, `validate --next`, deterministic jitter, `blocked` as a
  terminal state, circuit breaker) but is one week old, has one star
  and keeps a single global `jobs.yaml`. Read its spec before coding,
  do not build on it.
- The ideas worth the most come from closed products: GitHub Agentic
  Workflows (the agent never writes, a scoped executor applies typed
  "safe outputs", explicit `noop`, `close-older-issues`), Devin
  preflight (a script returns a JSON run/skip decision) and Claude Code
  Desktop (one catch-up run for the latest missed slot, skipped runs
  recorded with a reason).
- Recommendation: build a small new tool, steal liberally, open two
  bug issues on herdr-automations.

## Method

- `gh search repos` and `gh search code` with about thirty queries
  (claude/codex/opencode/gemini + cron/schedule/automations/launchd,
  herdr, tmux, `automations.yaml`, `.claude/schedules`), GitHub topics,
  the awesome-herdr list and the prior-art survey of herdr-cron.
- READMEs read through `gh api`. Source read for herdr-automations
  (full), Orca (automations module), Open Run (missed fires),
  dortort/claude-code-scheduler (schema).
- Official docs for the hosted products (Anthropic, OpenAI, Cursor,
  Devin, GitHub, Google, Warp, Factory, Amp).
- Local: herdr 0.9.3 (`herdr --skill`, `herdr api schema`), Claude Code
  2.1.308 (`claude --help`), the installed herdr plugins (heraldr,
  space-agents).

Not found: `butt` and `Husky` (as a scheduler) do not show up in repo,
code or web search; `claude-scheduler/claude_scheduler` does not exist
(closest: jshchnz/claude-code-scheduler, smanchajm/claude-scheduler).
Links welcome.

## Summary table

Maintenance: *active* = pushed in the last month, *slowing* = 1 to 6
months, *stale* = more.

| Tool | Kind | ★ | Last push | Maintenance | Verdict |
|---|---|---|---|---|---|
| [DnzzL/herdr-automations](https://github.com/DnzzL/herdr-automations) | herdr plugin, Go | 19 | 2026-10-20 | active, 1 maintainer | Reference for herdr launching; scope too narrow |
| [huketo/herdr-cron](https://github.com/huketo/herdr-cron) | herdr + OS schedulers, Go | 1 | 2026-10-20 | active, 1 week old | Best agent CLI design |
| [mikedclarke/herdr-shepherd](https://github.com/mikedclarke/herdr-shepherd) | herdr plugin, Go | 7 | 2026-10-09 | active | Gate command, working hours |
| [duyet/herdr-desk](https://github.com/duyet/herdr-desk) | herdr plugin, TS | 1 | 2026-10-13 | active | Best per-repo config model |
| [terry-li-hm/herdr-bots](https://github.com/terry-li-hm/herdr-bots) | launchd + herdr, Go | 0 | 2026-10-02 | prerelease | Strictest safety model |
| [husniadil/herdr-sched](https://github.com/husniadil/herdr-sched) | herdr plugin, Go | 5 | 2026-08-30 | slowing | Policy gate, cursor before fire |
| [bon5co/bermuda](https://github.com/bon5co/bermuda) | herdr plugin, Go | 13 | 2026-10-21 | active | Park blocked runs, sentinel |
| [ram4-dev/herdr-automations](https://github.com/ram4-dev/herdr-automations) | herdr plugin, Bun | 1 | 2026-08-13 | slowing | JSON Schema, herdr event triggers |
| [mrcndz/herdr-routines](https://github.com/mrcndz/herdr-routines) | herdr plugin, Python | 12 | 2026-07-18 | stale-ish | `pre` hook |
| [aneym/herdr-routines](https://github.com/aneym/herdr-routines) | herdr plugin, JS | 0 | 2026-10-07 | active | ok/noop tokens, auto-pause |
| [qluto/collie](https://github.com/qluto/collie) | herdr night queue, TS | 2 | 2026-09-14 | slowing | Fresh remote base, presence gate |
| [dortort/claude-code-scheduler](https://github.com/dortort/claude-code-scheduler) | Claude plugin, launchd/cron | 3 | 2026-10-19 | active | Per-repo file with trust boundary |
| [jshchnz/claude-code-scheduler](https://github.com/jshchnz/claude-code-scheduler) | Claude plugin (upstream) | 512 | 2026-01-19 | stale | Popular, naive on permissions |
| [T0UGH/agent-cron](https://github.com/T0UGH/agent-cron) | Agent SDK daemon | 1 | 2026-03-08 | stale | `.md` tasks, `HEARTBEAT_OK` |
| [triuzzi/crontui](https://github.com/triuzzi/crontui) | cron TUI with Claude tasks | 7 | 2026-06-16 | slowing | `setup-token`, `dontAsk` recipe |
| [kylemclaren/claude-tasks](https://github.com/kylemclaren/claude-tasks) | TUI cron, Go | 29 | 2026-01-16 | stale | Usage threshold gate |
| [tornikegomareli/claude-scheduler](https://github.com/tornikegomareli/claude-scheduler) | menu bar app, Swift | 8 | 2026-07-07 | slowing | Session id kept for `--resume` |
| [danielmaier42/dispatchq-macos](https://github.com/danielmaier42/dispatchq-macos) | macOS queue, Go | 3 | 2026-10-22 | active | `pmset` wake, rate-limit resume |
| [different-ai/opencode-scheduler](https://github.com/different-ai/opencode-scheduler) | opencode plugin | 506 | 2026-02-23 | stale | Denies "question" prompts |
| [jdormit/opencode-tasks](https://github.com/jdormit/opencode-tasks) | opencode tasks | 7 | 2026-10-15 | active | Per-task permission rules |
| [fookhsu/agents-cron](https://github.com/fookhsu/agents-cron) | pi/claude/codex | 2 | 2026-10-22 | active | Project store + global registry |
| [Mineru98/codex-cron-skill](https://github.com/Mineru98/codex-cron-skill) | Codex plugin | 13 | 2026-07-01 | slowing | Arg allowlist, tmux delivery |
| [geekforbrains/loopsie](https://github.com/geekforbrains/loopsie) | loops, Rust | 8 | 2026-09-29 | slowing | Gate stdout fed to the prompt |
| [openslop/nightshift](https://github.com/openslop/nightshift) | shell, nightly audits | 62 | 2026-10-22 | active | 13 audit jobs, PR overlap guard |
| [orwa-mahmoud/nightshift](https://github.com/orwa-mahmoud/nightshift) | long-run contract | 77 | 2026-10-22 | active | Receipts, "not a sandbox" |
| [dennisadriaans/openrun](https://github.com/dennisadriaans/openrun) | local web app | 29 | 2026-09-28 | slowing | 15 min catch-up grace (read in code) |
| [agentuse/agentuse](https://github.com/agentuse/agentuse) | own runtime | 207 | 2026-10-17 | active | `doctor`, mock-model tests |
| [dtormoen/tsk-tsk](https://github.com/dtormoen/tsk-tsk) | container sandbox | 171 | 2026-07-29 | slowing | No `.env` leak, egress proxy |
| [takurot/agent-scheduler](https://github.com/takurot/agent-scheduler) | issue queue + Docker | 3 | 2026-10-21 | active | Hardened container |
| [stablyai/orca](https://github.com/stablyai/orca) | desktop app, TS | 102k | 2026-10-22 | very active | Precheck, args allowlist |
| [dagucloud/dagu](https://github.com/dagucloud/dagu) | workflow engine | 4.4k | 2026-10-22 | very active | Too heavy; `harness.run` |
| [flohoss/gocron](https://github.com/flohoss/gocron) | YAML jobs + web UI | 615 | 2026-10-17 | active | Not agent-aware |
| [DavidWells/cron-burgundy](https://github.com/DavidWells/cron-burgundy) | launchd per job | 1 | 2026-09-27 | slowing | Wake hook, DarkWake notes |
| [sderosiaux/launchdeck](https://github.com/sderosiaux/launchdeck) | launchd inventory | 20 | 2026-09-01 | slowing | Debug companion |
| [ivolkoff/launchdeck](https://github.com/ivolkoff/launchdeck) | launchctl TUI | 0 | 2026-07-18 | slowing | Debug companion |
| [intellekthq/launchk](https://github.com/intellekthq/launchk) | launchd TUI | 139 | 2025-06-07 | stale | Debug companion |
| [antoniorodr/cronboard](https://github.com/antoniorodr/cronboard) | cron TUI | 1.5k | 2026-09-25 | active | Not agent-aware |
| [iannuttall/clockwork](https://github.com/iannuttall/clockwork) | launchd menu bar | 46 | 2026-08-03 | slowing | `explain-cron` |

Hosted products are covered in their own section below.

## herdr plugins

herdr itself (herdrdev/herdr, 45k★) has a real plugin system:
`herdr-plugin.toml` with `[[build]]`, `[[startup]]` (run on each server
start), `[[actions]]`, `[[panes]]` (overlay or popup) and `[[events]]`
(`pane.agent_status_changed`, `pane.exited`, `worktree.created`...).
Agent state (`idle`, `working`, `blocked`, `done`, `unknown`) comes from
hooks installed by `herdr integration install`. Notifications are
in-TUI toasts only (`herdr notification show`), not macOS notifications.

### DnzzL/herdr-automations

MIT, Go 1.26, about 4.3k lines, 15 releases in 10 weeks, one maintainer
who answers within a day and often rewrites contributions. The dnl555
fork has nothing upstream lacks (its only branch was merged as v0.8.1).

- Config: one global `automations.yaml` in the plugin config dir.
  Fields: `name`, `cron` (5 fields or `@daily`), `repo`, `workspace`
  (`worktree` | `root` | `existing` + `workspace_id`), `session`,
  `agent`, `model`, `prompt` or `workflow`, `mcp_config`, `agent_args`
  (verbatim, no filtering), `timeout_minutes` (60), `catch_up_minutes`
  (120, -1 = never), `disabled`. No timezone, env, precheck or outputs.
  A broken entry becomes a diagnostic with a line number, the others
  still load.
- Daemon: started by the plugin `[[startup]]` hook, single instance by
  `flock`, 30 s wall-clock tick, re-reads the YAML each tick, re-execs
  itself when its binary changes. It exits when no herdr session is
  left: no herdr server, no scheduler.
- Schedule: persisted last occurrence per job; first sighting registers
  without firing; on wake, the latest due occurrence fires if within the
  catch-up window, older ones are recorded as one `missed` entry. The
  state advances before firing (no replay after a crash). Overlap: the
  same job in flight is `skipped`, never queued; the guard is in memory,
  so a manual `run` in another process escapes it. Cron collisions over
  the next 7 days are warned. No jitter.
- Launch: `herdr worktree create --branch auto/<slug>-YYYYMMDD-HHMM`,
  `herdr agent start --kind claude -- <args>`, `herdr agent prompt
  --wait --until working`, with retries on a busy pane and a stalled
  prompt, then `herdr agent wait` until the timeout.
- History: append-only `history.jsonl`, latest line per `run_id` wins.
  No output, cost, duration or branch recorded.
- Board overlay (bubbletea): run now, pause, open the entry in
  `$EDITOR`, focus the run's pane, cleanup with confirmation.
- Cleanup: manual only; removes `auto/*` worktrees already merged into
  the default branch (`merge-base --is-ancestor`), never forces; squash
  merges are kept.
- Skill: teaches the agent to append entries, always set `model`, and
  check with `list`, `run`, `history`.
- Two bugs found by reading (not run): a run stuck on an approval prompt
  is recorded `done` (`agent wait` returns on `blocked` and that is
  treated as success); `catch_up_minutes: -1` marks on-time runs as
  missed because ticks land 0 to 30 s late (Orca fixed the same bug by
  tolerating two ticks).
- Contributing: precheck is plausible if raised as an issue first.
  Per-project files, headless mode, outputs and success notifications
  collide with the README ("one YAML file", "never runs your prompt
  itself") and ADR 0007. A previous field request was declined on scope.

Steal: wall-clock scheduling, cursor advanced before firing, `missed`
records, retries around `agent start`/`agent prompt`, merged-only
cleanup. Avoid: daemon lifetime tied to herdr, `blocked` = success,
nothing captured from the run.

### huketo/herdr-cron

MIT, Go, normative specs in `docs/spec/` (overview with a prior-art
survey, job model, storage, CLI, herdr integration, agent skill).

- Config: comment-preserving `jobs.yaml` (writes go through the YAML
  node tree, invalid writes aborted before rename), `defaults` block,
  unknown keys are a load error.
- Schedule: `cron` (5 or 6 fields, `CRON_TZ=`), `every`, `at`; IANA
  timezone; deterministic jitter `FNV1a64(id) mod min(interval/2,
  30m)`; catch-up `latest` (one run) / `off` / `all` with a window;
  skipped overlaps recorded as `skipped/overlap`.
- Agent runs: non-configurable "no human is watching" preamble,
  `no_op_marker`, `max_runs_per_day`, `blocked` is terminal (never
  retried, always notified, exit code 3), auto-disable after 3
  consecutive failures.
- Drivers: one `run-once <id>` primitive behind a daemon, a foreground
  pane, or OS schedulers (one LaunchAgent / systemd timer per job,
  refuses schedules it cannot translate exactly).
- Agent surface: JSON envelope by default, stable error codes with
  "did you mean", `validate --schedule ... --next 5`, `schema`, a skill
  printed byte-identical from the binary.

Steal: almost all of the CLI contract, jitter, `blocked` handling,
circuit breaker, unknown keys as errors. Avoid: global file only, no
precheck command.

### mikedclarke/herdr-shepherd

- One TOML file per action, re-read every 30 s; a broken file only
  disables itself and shows its parse error on the board; new actions
  are written `enabled = false`.
- Kinds: heartbeat (interval + working hours), routine (presets incl.
  `monthly` capped at day 28, cron), script.
- Gate: exit 0 runs, exit 75 skips silently (one `skipped` record);
  any other failure notifies and runs the agent anyway ("a gate is a
  filter, not a dependency").
- No backfill: more than 10 min late is dropped. Kernel file lock per
  action, valid across processes.

Steal: gate exit-code contract, working hours, per-file isolation of
errors. Avoid: running the agent when the gate is broken (costly).

### duyet/herdr-desk

- Per-repo `.herdr-desk.json` with a published JSON Schema and
  `validate`. Group configs in a parent directory need `"group": true`.
  Precedence: env > repo > group > machine > defaults, with `config
  explain --repo DIR` showing which layer set each value.
- Discovery: open the repo once as a herdr workspace, the desk remembers
  it.
- `watch` command printing NDJSON events with stable ids, deduped for 7
  days, batched into one run.
- Agent ladder: fall back to the next agent when one cannot start
  (logged out, rate limited).
- Remote prompts pinned by sha256, `prompts check` reports drift.
- Admitted pitfall: the repo config is read from the working tree "at
  whatever commit it is on", so a merged fix does nothing on a stale
  checkout.

Steal: JSON Schema + `validate`, `config explain`, hash pinning.

### terry-li-hm/herdr-bots

- launchd-supervised scheduler, SQLite state, macOS only.
- Config file must be a regular 0600 file owned by the user; unknown
  fields rejected; monotonic `revision` per job.
- Fresh worktree mandatory, root rejected; `run_if_changed`; capacity
  and disk headroom gate; unread-work guard (pause the job once N
  finished runs are unread).
- Claude launched without shell or web tools, file tools confined to
  the worktree, model attestation; post-run scope receipt, explicitly
  "not OS containment". Never pushes, merges or messages.
- Infrastructure result, agent result and verifier verdict stored
  separately.

Steal: unread guard, separate infra/agent/verifier outcomes,
`run_if_changed`. Avoid: the ceremony (revisions, 0600) for a personal
tool.

### Other herdr plugins

- husniadil/herdr-sched: all crons evaluated in UTC to avoid DST
  double runs; cursor moved before the action; HMAC webhooks on
  loopback; external `gate_command` answering allow/deny/defer, fail
  closed; CLI and MCP generated from one registry.
- bon5co/bermuda: a run that asks a question is parked, never dropped;
  daemon and sentinel revive each other; runs in a dedicated workspace
  tracked by id.
- ram4-dev/herdr-automations: JSON Schema for the YAML, herdr event
  triggers (`pane.agent_status_changed` with a match), invalid reload
  keeps the last good config, secret redaction in logs. Its skill
  verifies a second autonomous run, because approvals granted once make
  only the first run pass.
- mrcndz/herdr-routines: `pre` hook (non-zero skips), catch-up once
  within 7 days, overlap skipped while the agent is `working`. Notes
  that `workspace.focused` autostart never fires without a client.
- aneym/herdr-routines: `ok_token` / `noop_token`, notify after 3
  failures, auto-pause after 7 days, notes file as memory, `run
  --dry-run` prints the exact prompt.
- qluto/collie: worktree forked from the remote default branch fetched
  just before ("local main may be a week old at 3 a.m."), `post_create`
  to copy `.env`, quota gate from `/usage`, presence gate (input idle
  time), "merging the branch is the review". Notes that
  `bypassPermissions` shows a startup dialog nobody answers.

## Claude Code, Codex and opencode schedulers

### dortort/claude-code-scheduler

Fork of jshchnz/claude-code-scheduler (512★, stale since January).

- JSON config at two levels: `~/.claude/schedules.json` (trusted) and
  `<project>/.claude/schedules.json` (untrusted). Global wins on id
  collision. `skipPermissions` is always stripped from project files and
  an env blocklist (PATH, DYLD_*, NODE_OPTIONS...) applies: the right
  trust boundary.
- One launchd plist per job (crontab block on Linux); no catch-up beyond
  what launchd does; `mkdir` lock per task.
- Worktree option commits with `git add -u` and blocks sensitive files
  (`.env`, `.pem`, `.key`).
- Slash commands `/scheduler:add|list|edit|run|logs|history`.

Steal: trust boundary between repo and machine config. Avoid: one plist
per job (sync drift, launchd cron limits).

### Others

- T0UGH/agent-cron: tasks as `tasks/*.md` with front matter, exact
  output `HEARTBEAT_OK` marks a no-op; timezone hardcoded to
  Asia/Shanghai; no output by design.
- triuzzi/crontui: auth through `claude setup-token` stored in a 0600
  file "so they don't depend on the macOS keychain, which can hang with
  no TTY"; `dontAsk` plus `--allow` rules that fail fast instead of
  hanging; resolves `claude` from `/opt/homebrew/bin` because cron has a
  minimal PATH.
- kylemclaren/claude-tasks: skips scheduled tasks when Anthropic usage
  is above a threshold (80% by default).
- tornikegomareli/claude-scheduler: `claude -p --output-format
  stream-json`, stores duration, tokens, cost and the session id so any
  run reopens with `claude --resume`.
- danielmaier42/dispatchq-macos: `pmset` wake at the next due time,
  holds the Mac awake during a run, resumes the same session after a
  rate limit, `auth_error` never retried, built-in "you are headless,
  decide rather than ask" prompt, prompt review against the actual
  machine.
- different-ai/opencode-scheduler: jobs scoped by workdir; forces
  opencode to deny "question" prompts so jobs never hang.
- jdormit/opencode-tasks: ordered allow/deny permission rules per task
  in the front matter; installer captures a sanitized PATH.
- fookhsu/agents-cron: store inside the project
  (`.agents/cron/`) plus a global registry; management commands are
  inert inside a scheduled session, so a run cannot schedule more work.
- Mineru98/codex-cron-skill: default-deny allowlist on passthrough
  args (`--dangerously-*` refused); can paste into a live tmux pane
  before falling back to `codex exec`.
- geekforbrains/loopsie: gate stdout (16 KiB max) is injected into the
  prompt; postrun hook gets exit code and output tail; stdin closed so
  interactive agents fail fast.
- openslop/nightshift: 13 one-page audit jobs (security, quality, bugs,
  docs...) sharing a `_common.md` (read the repo guides, learn the
  owner's taste from past PR comments, run checks, open a PR or say
  why not); skips the night if it starts after `WINDOW_END_HOUR`;
  drops changes to files touched by an open PR.
- orwa-mahmoud/nightshift: punch list, receipts, morning summary; says
  its guards "are hardening, not a sandbox".
- dennisadriaans/openrun: one catch-up if the newest missed occurrence
  is within 15 min, otherwise a visible `missed` (read in code);
  execution dir pinned to a base commit; binds 127.0.0.1 only.
- agentuse/agentuse: `.agentuse` Markdown agents with `schedule:` in
  front matter, `doctor`, `test workflow --mock-model` before enabling
  a schedule.
- dtormoen/tsk-tsk and takurot/agent-scheduler: real isolation (repo
  copied without gitignored files, container, egress allowlist proxy,
  dropped capabilities). Useful later if runs move to containers.
- Smaller: amitection/looper (persists `/loop` crons across sessions),
  Vigil (notify only on state change), rahulmehta25/sleepwalker
  (PreToolUse hook defers `git push`, `gh pr create`, POSTs to a morning
  queue), rahulbansal16/agent-cron (session-start hook lists active
  jobs), daniel3303/ClaudeCodeCron (drives the interactive TUI to stay
  on the subscription).

## Orca automations (stablyai/orca)

MIT, Electron/TypeScript, 102k★, releases almost daily.

- Model: name, prompt, agent, `extraAgentArgs`, `precheck {command,
  timeoutSeconds}`, `workspaceMode` (`existing` | `new_per_run`),
  `baseBranch`, `reuseSession`, `timezone`, `rrule` (cron or RRULE),
  `missedRunGraceMinutes` (720 by default).
- Cron and a small RRULE subset (HOURLY/DAILY/WEEKLY) parsed by hand.
  `timezone` is stored but the occurrence maths uses the process
  timezone (*unverified*, found by grep).
- Missed runs: one `skipped_missed` record when past `grace + 2 ticks`
  (the two-tick tolerance fixed every run being skipped with zero grace).
- Overlap: serialised only until dispatch, so a new occurrence can start
  while the previous agent still works.
- Precheck: shell command in the target cwd, timeout 1 to 600 s, SIGTERM
  then SIGKILL on the process group, stdout/stderr tails kept on the
  run; pass = exit 0. Only for scheduled triggers: "run now" bypasses
  it. Its stdout is not passed to the prompt.
- Extra args: allowlist per agent (claude: `--model`, `--effort`,
  `--add-dir`), permission flags refused because "extras come from any
  paired client, so they must not reach the host's permission posture".
- Statuses include `skipped_precheck`, `skipped_missed`,
  `skipped_unavailable`, `skipped_needs_interactive_auth`,
  `dispatch_failed`; identical refusals folded into one row; output
  snapshot and usage (tokens, estimated USD) stored; 100 final runs kept
  per automation.

Steal: precheck runner details, permission posture owned by the host,
folded repeated refusals, retention cap. Avoid: run now skipping the
precheck, overlap after dispatch.

## Generic schedulers and launchd tools

- dagucloud/dagu: YAML DAGs with `overlap_policy`, `catchup_window`,
  `preconditions`, lifecycle handlers and a `harness.run` action for 17
  agent CLIs. Its own README: "Now you operate a second system." Notes
  that `claude --bare` skips the keychain, so subscription logins fail.
- flohoss/gocron (YAML + web UI + SQLite, `job_defaults`, pre/post
  commands), go-co-op/gocron (Go library), antoniorodr/cronboard (cron
  TUI, Telegram on failure): solid but not agent-aware.
- DavidWells/cron-burgundy: one plist per job, `sleepwatcher` wake hook
  to run missed jobs; documents DarkWake (jobs firing while the Mac looks
  asleep) and launchd's inability to express ranges, steps or day names.
- sderosiaux/launchdeck, ivolkoff/launchdeck, intellekthq/launchk,
  iannuttall/clockwork: launchd inspectors. Useful to debug "why did my
  daemon not run", nothing to reuse.

## Hosted and closed-source products

### Claude Code

- Desktop local scheduled tasks
  ([docs](https://code.claude.com/docs/en/desktop-scheduled-tasks)):
  presets or cron in local time, hourly minimum in the form; checks
  every minute while the app is open; deterministic per-task delay. On
  wake, looks back 7 days and starts exactly one catch-up for the most
  recent missed time, with a notification, and warns that a 9 a.m. task
  may run at 11 p.m. Runs in the folder as is (uncommitted changes
  included) unless the worktree box is ticked. In manual permission mode
  a missing permission stalls the run. History lists skipped runs with
  the reason (asleep, previous run in progress). Prompt stored as
  `~/.claude/scheduled-tasks/<name>/SKILL.md`, schedule and folder
  stored elsewhere.
- Cloud scheduled tasks, ex-routines
  ([docs](https://code.claude.com/docs/en/routines)): fresh clone,
  pushes to `claude/` branches, hourly minimum, start several minutes
  late on the hour (advice: 9:07). No permission mode to choose, every
  included connector usable with writes. API fire payload wrapped as
  untrusted and "can't act as approval or consent". "A green status does
  not mean the work your prompt describes succeeded." Turned off after
  72 h of expired GitHub auth.
- `/loop` and session crons: up to 50 per session, jitter, no catch-up,
  recurring tasks expire after 7 days.
- GitHub Action (`anthropics/claude-code-action@v1`) with `on:
  schedule`: no shell or GitHub write until granted through
  `--allowedTools`.
- Headless `claude -p` ([CLI reference](https://code.claude.com/docs/en/cli-reference),
  checked locally on 2.1.308):
  - `--permission-mode manual|acceptEdits|plan|auto|dontAsk|bypassPermissions`.
    `-p` starts in manual and denies (does not stall) what needs
    approval. `dontAsk` denies everything not pre-allowed, "the most
    predictable option for CI". `auto` uses a classifier that blocks
    `curl | bash`, force push, exfiltration, and "does not guarantee
    safety". `--permission-prompts none` removes `AskUserQuestion` and
    reports `permission_denials`.
  - `--allowedTools` / `--disallowedTools` rules, `--tools`,
    `--max-turns`, `--max-budget-usd` (client-side estimate, can
    overshoot), `--output-format json|stream-json`, `--json-schema`
    (result in `structured_output`), `--settings`, `--mcp-config` +
    `--strict-mcp-config`, `--setting-sources`, `--no-session-persistence`,
    `--session-id`, `-w/--worktree`, `--bg` (background session listed
    by `claude agents`, reopened with `claude attach`).
  - `--bare` skips hooks, skills, plugins, MCP and CLAUDE.md and needs
    an API key (no OAuth). Without it, `-p` runs the repo's
    `.claude/settings.json` hooks and `.mcp.json` servers without a
    trust dialog.
  - Init event reports `mcp_server_errors` and `plugin_errors`: a run
    can be failed when a server did not load.
- Sandbox ([docs](https://code.claude.com/docs/en/sandboxing)): off by
  default, Seatbelt on macOS, covers Bash and its children only (file
  tools, MCP and hooks run outside). Writes limited to cwd and added
  dirs; reads cover most of the machine, `~/.ssh` and `~/.aws` included,
  unless `filesystem.denyRead` is set; network through a proxy with an
  empty allowlist by default. Strict mode: `allowUnsandboxedCommands:
  false` and `failIfUnavailable: true`.
- Billing caveat: several sources say that since 2026-06-15 `claude -p`
  and Agent SDK usage draws on a separate monthly credit pool billed at
  API prices while interactive sessions stay on the subscription; one
  says the change was paused. Status *unverified*, to check on
  Anthropic's help center: it weighs on the choice of runner.

### OpenAI Codex and ChatGPT

- Codex app scheduled tasks (ex-automations,
  [docs](https://learn.chatgpt.com/docs/automations.md?surface=app)):
  standalone (fresh chat per run) or in-chat (continue a thread);
  presets or RRULE; local project or new worktree per run; run with the
  default sandbox and `approval_policy = "never"`. The Scheduled view is
  an inbox with unread badges; pinning a run keeps its worktree,
  archiving cleans it. Docs warn that frequent schedules create many
  worktrees. Local tasks need the app open and the Mac awake.
- `codex exec`: read-only sandbox by default, `--sandbox
  workspace-write`, `--json` events, `-o` last message to a file,
  `--output-schema`, named permission profiles.
- ChatGPT scheduled tasks: caps on active tasks per plan, at most hourly,
  push notification per completion. Not code-aware.

### GitHub

- Agentic Workflows, `gh aw`
  ([safe outputs](https://github.github.com/gh-aw/reference/safe-outputs/),
  [triggers](https://github.github.com/gh-aw/reference/triggers/)):
  Markdown with YAML front matter compiled and linted into a locked
  Actions workflow. The agent job is read-only; it emits typed requests
  (create issue, comment, PR, labels...) that separate jobs with scoped
  tokens apply, with `max` per type, `title-prefix`, `labels`,
  `close-older-issues`, `expires`. `noop` must be called when nothing
  is needed; `report-missing-tool` lets the agent say what it lacked;
  `staged: true` previews without writing. Triggers: fuzzy schedules
  scattered deterministically (`daily between 9:00 and 17:00`), cron
  with `timezone`, `skip-if-match` / `skip-if-no-match` on a GitHub
  search, `stop-after`. Threat detection job over the patch and outputs
  before applying. Network firewall with a domain allowlist. Linux
  runners only.
- Copilot coding agent and automations: pushes only to `copilot/*`
  branches, cannot approve or merge, firewall on by default; automation
  form lets you tick only the tools the task needs.
- Plain Actions `schedule`: default branch only, disabled after 60 days
  of inactivity in public repos.

### Cursor, Devin, Jules and others

- Cursor Automations ([help](https://cursor.com/help/ai-features/automations)):
  schedule plus many event triggers, repo setting none / one / many,
  "run as" me or a service account, tool allowlist per automation,
  memories file (with a poisoning warning). Pitfall: when an MCP server
  rejects credentials, nothing is notified and the run continues
  without those tools.
- Devin Automations ([docs](https://docs.devin.ai/product-guides/automations)):
  RRULE with a timezone per schedule. Preflight script before any
  session receives `EVENT_FILE`, `STATE_FILE` (persisted JSON, 64 KiB),
  `LAST_RUN_FILE`, and writes `{"run": true}`, `{"run": false,
  "reason": ...}` or `{"items": [...]}` (one session per item, deduped
  by id). Budget cap, rate limit, queue with concurrency, error status
  after consecutive failures, notify on failure by default, skipped runs
  in the activity tab. Pitfall: "Run now" skips preflight.
- Google Jules scheduled tasks: daily/weekly, VM per run, PRs as output,
  no edit (delete and recreate). The Stitch team ran a "pod" of narrow
  daily agents (performance, security, accessibility, test coverage).
- Warp Oz scheduled agents: `oz schedule create|list|get|pause|update|delete`,
  a skill (`repo:skill`) can be the prompt, updates apply to future runs.
- Factory automations: per-automation folder (`HEARTBEAT.md`,
  `reports/`, `memory/`, `skills/`, a dashboard), auto-pause after 5
  failures or 10 days of missed runs, "missed" = not started within 15
  min. Plain-language times converted once to UTC, so they drift with
  DST.
- Amp automations: completion condition ("stop when X is fixed"), pause
  on first failure.
- Linear Loops and CodeRabbit scheduled reports: triage loops that
  dispatch to coding agents; reports delivered to Slack or email.

## Takeaways by topic

| Topic | Take | Avoid |
|---|---|---|
| Config format | YAML per repo with JSON Schema, `validate`, unknown keys rejected (herdr-cron, herdr-desk); prompts in Markdown files (gh-aw, agent-cron) | Config split between a file and app state (Claude Desktop); one global file (DnzzL) |
| Trust | Machine config owns permissions, repo config only picks a profile (dortort, Orca) | Repo file able to add `--dangerously-skip-permissions` (DnzzL `agent_args`) |
| Timezone | Explicit IANA timezone, local by default (herdr-cron, gh-aw) | UTC-only or undocumented (Factory, Devin legacy, Warp) |
| Sleep / catch-up | Wall clock; one catch-up for the latest missed slot within a window; older slots recorded `missed` (Claude Desktop, DnzzL, Open Run) | Replaying a backlog (Dagu), catch-up landing in the middle of the day without guard |
| Overlap | Skip and record (herdr-cron); lock that holds across processes (shepherd) | In-memory guard (DnzzL), overlap after dispatch (Orca) |
| Jitter | Deterministic per job id, avoid :00 (herdr-cron, gh-aw, Claude) | Everything at 09:00 |
| Precheck | Command with exit-code contract, timeout, stdout passed to the prompt (Orca, loopsie, shepherd); built-in "changed since last success" (herdr-bots); JSON decision (Devin) | Run now bypassing the precheck (Orca, Devin); running the agent when the gate is broken (shepherd) |
| Isolation | Worktree from freshly fetched remote default branch, `.env` copy hook (collie) | Running in the live checkout (Claude Desktop default) |
| Permissions | `dontAsk` + allowlist or `auto` + strict sandbox; `blocked` is terminal and notified (herdr-cron) | `bypassPermissions` without sandbox; `blocked` recorded as done (DnzzL) |
| Outputs | Agent emits typed proposals, a deterministic executor applies them with caps; explicit `noop`; one live issue per job (gh-aw) | Agent holding write credentials; 52 weekly duplicate issues |
| Health | Separate infra status and task outcome; fail on MCP load errors; auto-pause after N failures; unread guard (Claude cloud, Cursor anti-pattern, herdr-bots) | Green status meaning "success" |
| History | Run directory with prompt, result, transcript, session id for `--resume` (tornikegomareli, Factory) | History without output or cost (DnzzL) |
| Cleanup | Noop runs auto-archived; findings kept as an inbox; merged-only removal, never forced (Codex, DnzzL) | Worktree explosion with frequent jobs |
| Discovery | Explicit registry (`add <path>`), no disk scan (herdr-desk, fookhsu) | Implicit discovery from wherever the plugin happens to run |
| Agent editing | JSON CLI with stable error codes, `schema`, `validate --next`, skill printed by the binary, scheduled runs cannot schedule (herdr-cron, fookhsu) | Skill copied once and drifting from the tool (DnzzL) |
