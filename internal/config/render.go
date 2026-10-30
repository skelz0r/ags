package config

import (
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/skelz0r/ags/internal/paths"
)

const PrecheckOutputPlaceholder = "{precheck.output}"

type RunContext struct {
	ID             string
	Trigger        string
	ScheduledFor   time.Time
	LateBy         time.Duration
	JobStateDir    string
	LastSuccessAt  time.Time
	PrecheckOutput string
	AGSHome        string
}

func RenderPrompt(m *Machine, p *Project, j *Job, rc RunContext) string {
	templated := strings.NewReplacer(
		"{project.name}", p.Name,
		"{job.name}", j.Name,
		"{run.id}", rc.ID,
		"{run.scheduled_for}", rc.ScheduledFor.In(p.Location).Format(time.RFC3339),
		"{run.late_by}", FormatDuration(rc.LateBy),
		PrecheckOutputPlaceholder, rc.PrecheckOutput,
	).Replace(j.Prompt)
	var parts []string
	for _, part := range []string{m.Preprompt, p.Preprompt, p.LocalPreprompt, templated, reportInstruction(m, j, rc)} {
		if trimmed := strings.TrimSpace(part); trimmed != "" {
			parts = append(parts, strings.TrimRight(part, "\n"))
		}
	}
	return strings.Join(parts, "\n\n") + "\n"
}

func reportInstruction(m *Machine, j *Job, rc RunContext) string {
	if cmd := m.Commands[j.CommandName]; cmd == nil || !cmd.Reports() {
		return ""
	}
	command := "ags report " + rc.ID
	if rc.AGSHome != "" {
		command = "AGS_HOME=" + rc.AGSHome + " " + command
	}
	return fmt.Sprintf(`This is ags run %s. When you are done, report its outcome, even when there was nothing to do or you are blocked, as your last step:

    %s --status <status> --summary "<what you did or concluded, one or two sentences>" [--ref <commit, pull request or URL>]

Status: done (the work is done: give the commit or pull request as --ref), nothing (nothing to do, or not concerned), failed (you tried and could not), blocked (it needs a human: say what).
`, rc.ID, command)
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
	values := []string{
		"{project.path}", p.Dir,
		"{project.name}", p.Name,
		"{job.name}", j.Name,
		"{run.id}", rc.ID,
		"{run.scheduled_for}", rc.ScheduledFor.In(p.Location).Format(time.RFC3339),
	}
	placeholders := strings.NewReplacer(values...)
	argPlaceholders := placeholders
	if cmd.PromptArg {
		argPlaceholders = strings.NewReplacer(append(values, "{prompt}", prompt)...)
	}
	argv := make([]string, len(cmd.Argv))
	for i, arg := range cmd.Argv {
		argv[i] = argPlaceholders.Replace(paths.ExpandHome(arg, home))
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
		"AGS_JOB_STATE_DIR=" + rc.JobStateDir,
		"AGS_LAST_SUCCESS_AT=" + formatLastSuccess(rc.LastSuccessAt),
	}
}

func formatLastSuccess(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.UTC().Format(time.RFC3339)
}
