---
name: ags-doctor
description: Answer "why did this ags job not run" or "why did it fail" by reading the daemon's health, the job's configuration and its run history, then propose the fix. Use when the user says a scheduled agent job did not run, ran late, was skipped or failed, or asks whether the ags daemon works.
---

# ags-doctor

Read before concluding; every command accepts `--json`.

1. `ags doctor --json`: LaunchAgent loaded, daemon alive and its last
   tick, machine config valid, every command's program found, every
   project valid, config errors the daemon sees. A failing check is
   often the whole answer.
2. `ags jobs --project <project> --json`: is the job there, `enabled`,
   `approved`, its schedule in words, its timezone and next run.
3. `ags runs --job <project>/<job> --since 14d --json`: what the daemon
   decided for each occurrence:
   - `missed` with `missed_count`: the Mac was asleep or off past
     `catch_up` (12h by default);
   - `skipped (unapproved)`: the shared prompt, precheck or command
     changed, `ags approve <project>/<job>` shows the diff;
   - `skipped (paused)`: `enabled: false`, usually in `.ags.local.yml`;
   - `skipped (overlap)`: the previous run was still going;
   - `skipped (precheck)`: the precheck exited 1, its output is the
     reason;
   - `precheck_error`, `failed`: read the run.
4. `ags show <run id> --json`: rendered prompt, precheck output, stdout,
   stderr, exit code, argv.
5. If nothing was recorded at all: the job is new (nothing fires for
   the past), the project file is invalid (`ags validate <path>`), or
   the daemon was not running.

Propose the fix (approve, resume, correct the cron or the precheck,
raise `catch_up` or `timeout`, fix the command in the machine config)
and let the user apply changes to the machine config.
