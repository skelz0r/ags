# ags

Schedule coding agents on a Mac. Register projects, give each one jobs
(a name, a cron schedule, a prompt) in a versioned `.ags.yml`, and a
launchd daemon runs a command of your choice with that prompt on time,
catching up after sleep. A TUI lists every project, its jobs and runs.

See [docs/spec.md](docs/spec.md).
