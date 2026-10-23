package config

import (
	"os"
	"strings"
	"time"

	"github.com/skelz0r/ags/internal/paths"
)

type RunContext struct {
	ID           string
	Trigger      string
	ScheduledFor time.Time
	LateBy       time.Duration
}

func RenderPrompt(m *Machine, p *Project, j *Job, rc RunContext) string {
	templated := strings.NewReplacer(
		"{project.name}", p.Name,
		"{job.name}", j.Name,
		"{run.scheduled_for}", rc.ScheduledFor.In(p.Location).Format(time.RFC3339),
		"{run.late_by}", FormatDuration(rc.LateBy),
	).Replace(j.Prompt)
	var parts []string
	for _, part := range []string{m.Preprompt, p.Preprompt, p.LocalPreprompt, templated} {
		if trimmed := strings.TrimSpace(part); trimmed != "" {
			parts = append(parts, strings.TrimRight(part, "\n"))
		}
	}
	return strings.Join(parts, "\n\n") + "\n"
}

type Invocation struct {
	Command *Command
	Argv    []string
	Dir     string
	Stdin   string
	Env     []string
}

func Invoke(m *Machine, p *Project, j *Job, rc RunContext, prompt string) Invocation {
	cmd := m.Commands[j.CommandName]
	home := os.Getenv("HOME")
	placeholders := strings.NewReplacer(
		"{project.path}", p.Dir,
		"{project.name}", p.Name,
		"{job.name}", j.Name,
		"{run.id}", rc.ID,
		"{run.scheduled_for}", rc.ScheduledFor.In(p.Location).Format(time.RFC3339),
	)
	argv := make([]string, len(cmd.Argv))
	for i, arg := range cmd.Argv {
		arg = paths.ExpandHome(arg, home)
		if cmd.PromptArg {
			arg = strings.ReplaceAll(arg, "{prompt}", prompt)
		}
		argv[i] = placeholders.Replace(arg)
	}
	dir := p.Dir
	if cmd.Cwd != "" {
		dir = placeholders.Replace(paths.ExpandHome(cmd.Cwd, home))
	}
	inv := Invocation{Command: cmd, Argv: argv, Dir: dir, Env: RunEnv(p, j, rc)}
	for k, v := range cmd.Env {
		inv.Env = append(inv.Env, k+"="+placeholders.Replace(paths.ExpandHome(v, home)))
	}
	if cmd.Stdin == StdinPrompt {
		inv.Stdin = prompt
	}
	return inv
}

func RunEnv(p *Project, j *Job, rc RunContext) []string {
	return []string{
		"AGS_PROJECT=" + p.Name,
		"AGS_PROJECT_PATH=" + p.Dir,
		"AGS_JOB=" + j.Name,
		"AGS_RUN_ID=" + rc.ID,
		"AGS_SCHEDULED_FOR=" + rc.ScheduledFor.In(p.Location).Format(time.RFC3339),
		"AGS_TRIGGER=" + rc.Trigger,
	}
}
