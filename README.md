# ags

Schedule coding agents on a Mac. Register projects, give each one jobs
(a name, a cron schedule, a prompt) in a versioned `.ags.yml`, and a
launchd daemon runs a command of your choice with that prompt on time,
catching up after sleep. A TUI lists every project, its jobs and runs.

See [docs/spec.md](docs/spec.md).

## Install

```sh
go install ./cmd/ags
ags install          # LaunchAgent dev.ags, skills, starter ~/.config/ags/config.yml
ags add ~/work/repo  # shows the shared jobs and asks to approve them
ags doctor
```

`ags` alone opens the TUI; `ags help` lists the commands. Set
`AGS_HOME=/tmp/ags-try` to experiment without touching the real config,
state or LaunchAgent.
