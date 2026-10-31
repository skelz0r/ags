---
name: ags-run
description: Run an ags job now and report the result - check the rendered prompt and exact argv with a dry run first, then run it for real and summarize the run status and the command's output. Use when the user asks to "run the audit now", "lance le job", "test this ags job", or to try a job they just configured.
---

# ags-run

1. Identify the job as `<project>/<job>` (`ags jobs --json` lists them).
2. Dry run: `ags run <project>/<job> --dry-run --json`. Check the
   precheck, the rendered prompt (machine, project and local preprompts
   then the job's prompt) and the argv. Report anything surprising
   before going further; if `approved` is false, the user has to run
   `ags approve` first.
3. Run: `ags run <project>/<job> --json`. Add `--skip-precheck` only
   when the user asked for it. It runs in the foreground until the
   command exits or hits its timeout.
4. Report: status (`succeeded`, `failed`, `skipped (precheck)`,
   `precheck_error`), reason, exit code, duration, and the useful part
   of stdout and stderr (`ags show <run id> --json` has them in full).
   When the precheck stopped the run, the exit code is the precheck's
   and its stdout and stderr are in `precheck_log`.
   A hand-off command returns at once: its stdout often holds an id of
   what it handed off to, and the agent sends its outcome later with
   `ags report`, shown by `ags show <run id>` once it arrives; to
   watch for it, the user can keep `ags feed --job <project/job>`
   open in a terminal tab.

`ags run` refuses inside a scheduled run (`AGS_RUN_ID` set).
