package cli

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"text/tabwriter"
	"time"

	"github.com/skelz0r/ags/internal/config"
	"github.com/skelz0r/ags/internal/runner"
	"github.com/skelz0r/ags/internal/state"
)

type dryRun struct {
	Job      string            `json:"job"`
	Command  string            `json:"command"`
	Precheck string            `json:"precheck"`
	Prompt   string            `json:"prompt"`
	Argv     []string          `json:"argv"`
	Cwd      string            `json:"cwd"`
	Stdin    string            `json:"stdin"`
	Timeout  string            `json:"timeout"`
	Env      map[string]string `json:"env"`
	Approved bool              `json:"approved"`
}

func (a *App) runJob(args []string) (result, error) {
	f := newFlags("run")
	skipPrecheck := f.Bool("skip-precheck", false, "")
	dry := f.Bool("dry-run", false, "")
	if err := f.parse(args, 1, 1); err != nil {
		return result{}, err
	}
	if !*dry {
		if err := a.refuseInsideRun("run"); err != nil {
			return result{}, err
		}
	}
	t, err := a.resolve(f.positional[0], true)
	if err != nil {
		return result{}, err
	}
	store, err := a.store()
	if err != nil {
		return result{}, err
	}
	st, err := store.Read()
	if err != nil {
		return result{}, err
	}
	now := a.Now()
	run := runner.NewRun(t.project, t.job, state.TriggerManual, now, now)
	rn := &runner.Runner{Store: store, Machine: a.machine, Notifier: a.Notifier, Now: a.Now}
	if *dry {
		rc := rn.Context(run)
		rc.ID = "dry-run"
		prompt := config.RenderPrompt(a.machine, t.project, t.job, rc)
		inv := config.Invoke(a.machine, t.project, t.job, rc, prompt)
		d := dryRun{Job: t.job.Key(), Command: t.job.CommandName, Precheck: t.job.Precheck, Prompt: prompt, Argv: inv.Argv, Cwd: inv.Dir, Stdin: inv.Command.Stdin, Timeout: inv.Command.Timeout.String(), Env: map[string]string{}, Approved: st.Approved(t.job)}
		for _, kv := range inv.Env {
			k, v, _ := strings.Cut(kv, "=")
			d.Env[k] = v
		}
		if *skipPrecheck {
			d.Precheck = ""
		}
		var b strings.Builder
		fmt.Fprintf(&b, "precheck: %s\n", orNone(d.Precheck))
		fmt.Fprintf(&b, "argv: %s\n", quoteArgv(d.Argv))
		fmt.Fprintf(&b, "cwd: %s\nstdin: %s\ntimeout: %s\n", d.Cwd, d.Stdin, d.Timeout)
		if !d.Approved {
			b.WriteString("approval: not approved, a scheduled run would be skipped (unapproved)\n")
		}
		fmt.Fprintf(&b, "--- prompt\n%s", prompt)
		return result{data: d, text: b.String()}, nil
	}
	err = store.Update(func(st *state.State) error {
		if !st.Approved(t.job) {
			return failure(CodeUnapproved, "%s is not approved in its current version: ags approve %s", t.job.Key(), t.job.Key())
		}
		running, _ := store.ListRuns(state.Filter{Project: t.project.Name, Job: t.job.Name, Status: state.StatusRunning})
		for _, r := range running {
			if state.ProcessAlive(r.OwnerPID) {
				return failure(CodeBusy, "%s is already running (run %s)", t.job.Key(), r.ID)
			}
		}
		return store.SaveRun(run)
	})
	var refused *Error
	if err != nil && !errors.As(err, &refused) {
		ended := a.Now()
		run.Status, run.Reason, run.EndedAt = state.StatusFailed, "state not saved: "+err.Error(), &ended
		store.SaveRun(run)
	}
	if err != nil {
		return result{}, err
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if !a.json {
		fmt.Fprintf(a.Stderr, "running %s (run %s)\n", t.job.Key(), run.ID)
	}
	run = rn.Execute(ctx, runner.Request{Project: t.project, Job: t.job, Run: run, SkipPrecheck: *skipPrecheck})
	view := runView(store, run)
	text := fmt.Sprintf("%s %s in %s\n%s", run.ID, StatusLabel(run.Status, run.Reason), run.Duration().Round(time.Millisecond), store.ReadRunFile(run, "stdout"))
	if run.Status == state.StatusFailed || run.Status == state.StatusPrecheckError {
		return result{}, &Error{Code: CodeRunFailed, Message: fmt.Sprintf("run %s %s: %s", run.ID, run.Status, run.Reason), exit: ExitError, data: view}
	}
	return result{data: view, text: text}, nil
}

func orNone(s string) string {
	if s == "" {
		return "(none)"
	}
	return s
}

func quoteArgv(argv []string) string {
	out := make([]string, len(argv))
	for i, arg := range argv {
		if arg == "" || strings.ContainsAny(arg, " \t\n'\"$`\\|&;<>(){}*?[]#~") {
			out[i] = "'" + strings.ReplaceAll(arg, "'", `'\''`) + "'"
		} else {
			out[i] = arg
		}
	}
	return strings.Join(out, " ")
}

func (a *App) runs(args []string) (result, error) {
	f := newFlags("runs")
	project := f.String("project", "", "")
	job := f.String("job", "", "")
	status := f.String("status", "", "")
	since := f.String("since", "", "")
	limit := f.Int("limit", 50, "")
	if err := f.parse(args, 0, 0); err != nil {
		return result{}, err
	}
	if p, j := splitTarget(*job); j != "" {
		*project, *job = p, j
	}
	filter := state.Filter{Project: *project, Job: *job, Status: *status, Limit: *limit}
	if *since != "" {
		d, err := config.ParseDuration(*since)
		if err != nil {
			return result{}, usageError("--since: %v", err)
		}
		filter.Since = a.Now().Add(-d)
	}
	store, err := a.store()
	if err != nil {
		return result{}, err
	}
	runs, err := store.ListRuns(filter)
	if err != nil {
		return result{}, err
	}
	views := []RunView{}
	var b strings.Builder
	w := tabwriter.NewWriter(&b, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "RUN\tJOB\tSCHEDULED\tTRIGGER\tSTATUS\tDURATION\tEXIT")
	for _, r := range runs {
		v := runView(store, r)
		views = append(views, v)
		exit := "-"
		if r.ExitCode != nil {
			exit = fmt.Sprint(*r.ExitCode)
		}
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\t%s\t%s\n", r.ID, r.Key(), v.LastScheduled().Local().Format("2006-01-02 15:04"), orDash(r.Trigger), v.Label(), durationText(r), exit)
	}
	w.Flush()
	return result{data: views, text: b.String()}, nil
}

func orDash(s string) string {
	if s == "" {
		return "-"
	}
	return s
}

func durationText(r *state.Run) string {
	if r.StartedAt == nil || r.EndedAt == nil {
		return "-"
	}
	return r.Duration().Round(time.Second).String()
}

type runDetail struct {
	RunView
	Prompt      string `json:"prompt"`
	PrecheckLog string `json:"precheck_log"`
	Stdout      string `json:"stdout"`
	Stderr      string `json:"stderr"`
}

func (a *App) show(args []string) (result, error) {
	f := newFlags("show")
	if err := f.parse(args, 1, 1); err != nil {
		return result{}, err
	}
	store, err := a.store()
	if err != nil {
		return result{}, err
	}
	r, err := store.FindRun(f.positional[0])
	if err != nil {
		return result{}, failure(CodeNotFound, "%v", err)
	}
	d := runDetail{
		RunView:     runView(store, r),
		Prompt:      store.ReadRunFile(r, "prompt.md"),
		PrecheckLog: store.ReadRunFile(r, "precheck.log"),
		Stdout:      store.ReadRunFile(r, "stdout"),
		Stderr:      store.ReadRunFile(r, "stderr"),
	}
	return result{data: d, text: RunDetailText(d.RunView, d.Prompt, d.PrecheckLog, d.Stdout, d.Stderr)}, nil
}

func RunDetailText(v RunView, prompt, precheck, stdout, stderr string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "run       %s\njob       %s/%s\nstatus    %s\n", v.ID, v.Project, v.Job, StatusLabel(v.Status, v.Reason))
	if v.Reason != "" {
		fmt.Fprintf(&b, "reason    %s\n", v.Reason)
	}
	fmt.Fprintf(&b, "trigger   %s\nscheduled %s (late by %s)\n", orDash(v.Trigger), v.ScheduledFor.Local().Format(time.RFC3339), v.LateBy)
	if v.MissedCount > 0 {
		fmt.Fprintf(&b, "missed    %d occurrence(s), the last at %s\n", v.MissedCount, v.MissedLast.Local().Format(time.RFC3339))
	}
	if v.SkippedCount > 1 {
		fmt.Fprintf(&b, "repeated  %d occurrences, the last at %s\n", v.SkippedCount, v.SkippedLast.Local().Format(time.RFC3339))
	}
	if v.StartedAt != nil {
		fmt.Fprintf(&b, "started   %s\n", v.StartedAt.Local().Format(time.RFC3339))
	}
	if v.EndedAt != nil {
		fmt.Fprintf(&b, "ended     %s (%s)\n", v.EndedAt.Local().Format(time.RFC3339), time.Duration(v.DurationSeconds*float64(time.Second)).Round(time.Millisecond))
	}
	switch {
	case len(v.Argv) > 0:
		fmt.Fprintf(&b, "command   %s: %s\n", v.Command, quoteArgv(v.Argv))
	case v.Command != "":
		fmt.Fprintf(&b, "command   %s\n", v.Command)
	}
	if v.ExitCode != nil {
		fmt.Fprintf(&b, "exit      %d\n", *v.ExitCode)
	}
	fmt.Fprintf(&b, "dir       %s\n", v.Dir)
	for _, section := range []struct{ name, body string }{{"prompt", prompt}, {"precheck", precheck}, {"stdout", stdout}, {"stderr", stderr}} {
		if section.body != "" {
			fmt.Fprintf(&b, "\n--- %s\n%s", section.name, strings.TrimRight(section.body, "\n")+"\n")
		}
	}
	return b.String()
}
