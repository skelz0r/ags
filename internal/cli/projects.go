package cli

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/skelz0r/ags/internal/config"
	"github.com/skelz0r/ags/internal/paths"
	"github.com/skelz0r/ags/internal/state"
)

var slugPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]{0,63}$`)

func slugify(name string) string {
	name = strings.ToLower(strings.TrimPrefix(name, "."))
	name = regexp.MustCompile(`[^a-z0-9._-]+`).ReplaceAllString(name, "-")
	return strings.Trim(name, "-")
}

type projectView struct {
	Name     string        `json:"name"`
	Path     string        `json:"path"`
	Jobs     []JobView     `json:"jobs"`
	Errors   config.Issues `json:"errors,omitempty"`
	Warnings config.Issues `json:"warnings,omitempty"`
}

func (a *App) add(args []string) (result, error) {
	f := newFlags("add")
	name := f.String("name", "", "")
	approve := f.Bool("approve", false, "")
	if err := f.parse(args, 1, 1); err != nil {
		return result{}, err
	}
	dir, err := a.expandPath(f.positional[0])
	if err != nil {
		return result{}, err
	}
	if info, err := os.Stat(dir); err != nil || !info.IsDir() {
		return result{}, failure(CodeNotFound, "%s is not a directory", dir)
	}
	if *name == "" {
		*name = slugify(filepath.Base(dir))
		if dir == a.Getenv("HOME") {
			*name = "home"
		}
	}
	if !slugPattern.MatchString(*name) {
		return result{}, usageError("invalid project name %q: use [a-z0-9._-]", *name)
	}
	reg, err := a.registry()
	if err != nil {
		return result{}, err
	}
	if existing := reg.Find(*name); existing != nil {
		return result{}, failure(CodeError, "a project named %q is already registered at %s: pass --name", *name, existing.Path)
	}
	if existing := reg.FindPath(dir); existing != nil {
		return result{}, failure(CodeError, "%s is already registered as %q", dir, existing.Name)
	}
	m, machineIssues := a.loadMachine()
	p, issues := config.Loader{Machine: m}.Load(*name, dir)
	if all := append(machineIssues, issues...); len(all) > 0 {
		return result{}, invalid(map[string]any{"errors": all}, all)
	}
	reg.Add(config.Entry{Name: *name, Path: dir})
	if err := reg.Save(a.paths.Registry()); err != nil {
		return result{}, err
	}
	store, err := a.store()
	if err != nil {
		return result{}, err
	}
	var text strings.Builder
	fmt.Fprintf(&text, "added %s (%s), %d job(s)\n", *name, paths.Abbreviate(dir, a.Getenv("HOME")), len(p.Jobs))
	for _, w := range p.Warnings {
		fmt.Fprintf(&text, "warning: %s\n", w)
	}
	var pending []*config.Job
	for _, j := range p.Jobs {
		if j.NeedsApproval() {
			pending = append(pending, j)
		}
	}
	approved := []string{}
	if len(pending) > 0 && !*approve && !a.json && a.IsTTY {
		for _, j := range pending {
			fmt.Fprintf(a.Stdout, "--- %s\n%s\n", j.Key(), j.ApprovalText)
		}
		*approve = a.confirm(fmt.Sprintf("Approve these %d shared job(s)?", len(pending)))
	}
	if *approve {
		if err := approveJobs(store, pending, a.Now()); err != nil {
			return result{}, err
		}
		for _, j := range pending {
			approved = append(approved, j.Key())
		}
		if len(pending) > 0 {
			fmt.Fprintf(&text, "approved %d shared job(s)\n", len(pending))
		}
	} else if len(pending) > 0 {
		fmt.Fprintf(&text, "%d shared job(s) wait for approval: ags approve %s\n", len(pending), *name)
	}
	st, _ := store.Read()
	view := projectView{Name: *name, Path: dir, Warnings: p.Warnings, Jobs: []JobView{}}
	for _, j := range p.Jobs {
		view.Jobs = append(view.Jobs, jobView(j, st, a.Now(), 1, a.Getenv("HOME")))
	}
	return result{data: map[string]any{"project": view, "approved": approved}, text: text.String()}, nil
}

func approveJobs(store *state.Store, jobs []*config.Job, now time.Time) error {
	return store.Update(func(st *state.State) error {
		for _, j := range jobs {
			st.Approvals[j.Key()] = state.Approval{Fingerprint: j.Fingerprint, Text: j.ApprovalText, ApprovedAt: now}
		}
		return nil
	})
}

func (a *App) remove(args []string) (result, error) {
	f := newFlags("remove")
	if err := f.parse(args, 1, 1); err != nil {
		return result{}, err
	}
	reg, err := a.registry()
	if err != nil {
		return result{}, err
	}
	name := f.positional[0]
	if !reg.Remove(name) {
		return result{}, failure(CodeNotFound, "project %q is not registered", name)
	}
	if err := reg.Save(a.paths.Registry()); err != nil {
		return result{}, err
	}
	return result{data: map[string]any{"removed": name}, text: "removed " + name + ", its history is kept"}, nil
}

func (a *App) projects(args []string) (result, error) {
	f := newFlags("projects")
	if err := f.parse(args, 0, 0); err != nil {
		return result{}, err
	}
	views, err := a.loadAll("")
	if err != nil {
		return result{}, err
	}
	var b strings.Builder
	w := tabwriter.NewWriter(&b, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "PROJECT\tJOBS\tPATH\tSTATUS")
	for _, v := range views {
		status := "ok"
		if len(v.Errors) > 0 {
			status = "invalid: " + v.Errors[0].String()
		}
		fmt.Fprintf(w, "%s\t%d\t%s\t%s\n", v.Name, len(v.Jobs), paths.Abbreviate(v.Path, a.Getenv("HOME")), status)
	}
	w.Flush()
	return result{data: views, text: b.String()}, nil
}

func (a *App) loadAll(only string) ([]projectView, error) {
	reg, err := a.registry()
	if err != nil {
		return nil, err
	}
	if only != "" && reg.Find(only) == nil {
		return nil, failure(CodeNotFound, "project %q is not registered", only)
	}
	store, err := a.store()
	if err != nil {
		return nil, err
	}
	st, err := store.Read()
	if err != nil {
		return nil, err
	}
	m, machineIssues := a.loadMachine()
	views := []projectView{}
	for _, entry := range reg.Projects {
		if only != "" && entry.Name != only {
			continue
		}
		view := projectView{Name: entry.Name, Path: entry.Path, Jobs: []JobView{}}
		p, issues := config.Loader{Machine: m, SkipGit: true}.Load(entry.Name, entry.Path)
		if all := append(append(config.Issues{}, machineIssues...), issues...); len(all) > 0 {
			view.Errors = all
		} else {
			view.Warnings = p.Warnings
			for _, j := range p.Jobs {
				jv := jobView(j, st, a.Now(), 1, a.Getenv("HOME"))
				if runs, _ := store.ListRuns(state.Filter{Project: j.Project, Job: j.Name, Limit: 1}); len(runs) > 0 {
					rv := runView(store, runs[0])
					jv.LastRun = &rv
				}
				view.Jobs = append(view.Jobs, jv)
			}
		}
		views = append(views, view)
	}
	return views, nil
}

type validation struct {
	Project  string        `json:"project"`
	Path     string        `json:"path"`
	Valid    bool          `json:"valid"`
	Errors   config.Issues `json:"errors"`
	Warnings config.Issues `json:"warnings"`
	Jobs     []JobView     `json:"jobs"`
}

func (a *App) validate(args []string) (result, error) {
	f := newFlags("validate")
	if err := f.parse(args, 0, 1); err != nil {
		return result{}, err
	}
	target := "."
	if len(f.positional) == 1 {
		target = f.positional[0]
	}
	dir, err := a.expandPath(target)
	if err != nil {
		return result{}, err
	}
	name := slugify(filepath.Base(dir))
	if reg, err := a.registry(); err == nil {
		if entry := reg.FindPath(dir); entry != nil {
			name = entry.Name
		}
	}
	m, machineIssues := a.loadMachine()
	p, issues := config.Loader{Machine: m}.Load(name, dir)
	v := validation{Project: name, Path: dir, Errors: append(machineIssues, issues...), Warnings: config.Issues{}, Jobs: []JobView{}}
	if v.Errors == nil {
		v.Errors = config.Issues{}
	}
	var st *state.State
	if store, err := a.store(); err == nil {
		st, _ = store.Read()
	}
	if p != nil {
		v.Warnings = append(v.Warnings, p.Warnings...)
		for _, j := range p.Jobs {
			v.Jobs = append(v.Jobs, jobView(j, st, a.Now(), 3, a.Getenv("HOME")))
		}
	}
	v.Valid = len(v.Errors) == 0
	var b strings.Builder
	for _, issue := range v.Errors {
		fmt.Fprintf(&b, "error: %s\n", issue)
	}
	for _, issue := range v.Warnings {
		fmt.Fprintf(&b, "warning: %s\n", issue)
	}
	for _, j := range v.Jobs {
		fmt.Fprintf(&b, "%s (%s, %s, command %s)%s\n", j.Name, j.Origin, j.Description, j.Command, flagsOf(j))
		for _, t := range j.Next {
			fmt.Fprintf(&b, "  %s\n", formatTime(t, p.Location))
		}
	}
	if !v.Valid {
		return result{}, &Error{Code: CodeInvalidConfig, Message: v.Errors.Error(), exit: ExitInvalid, data: v}
	}
	if len(v.Jobs) == 0 {
		b.WriteString("valid, no jobs\n")
	}
	return result{data: v, text: b.String()}, nil
}

func flagsOf(j JobView) string {
	var out []string
	if !j.Enabled {
		out = append(out, "paused")
	}
	if !j.Approved {
		out = append(out, "unapproved")
	}
	if len(out) == 0 {
		return ""
	}
	return " [" + strings.Join(out, ", ") + "]"
}

func formatTime(t time.Time, loc *time.Location) string {
	if loc != nil {
		t = t.In(loc)
	}
	return t.Format("Mon 2006-01-02 15:04 MST")
}

func (a *App) jobs(args []string) (result, error) {
	f := newFlags("jobs")
	project := f.String("project", "", "")
	if err := f.parse(args, 0, 0); err != nil {
		return result{}, err
	}
	views, err := a.loadAll(*project)
	if err != nil {
		return result{}, err
	}
	jobs := []JobView{}
	var b strings.Builder
	w := tabwriter.NewWriter(&b, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "JOB\tSCHEDULE\tCOMMAND\tORIGIN\tNEXT\tLAST")
	var errs config.Issues
	for _, v := range views {
		errs = append(errs, v.Errors...)
		for _, j := range v.Jobs {
			jobs = append(jobs, j)
			next := "-"
			if len(j.Next) > 0 {
				next = j.Next[0].Format("Mon 01-02 15:04")
			}
			last := "-"
			if j.LastRun != nil {
				last = j.LastRun.ScheduledFor.Local().Format("01-02 15:04") + " " + StatusLabel(j.LastRun.Status, j.LastRun.Reason)
			}
			fmt.Fprintf(w, "%s%s\t%s\t%s\t%s\t%s\t%s\n", j.Key, flagsOf(j), j.Description, j.Command, j.Origin, next, last)
		}
	}
	w.Flush()
	for _, e := range errs {
		fmt.Fprintf(&b, "error: %s\n", e)
	}
	if errs == nil {
		errs = config.Issues{}
	}
	return result{data: map[string]any{"jobs": jobs, "errors": errs}, text: b.String()}, nil
}

func (a *App) next(args []string) (result, error) {
	f := newFlags("next")
	n := f.Int("n", 5, "")
	if err := f.parse(args, 1, 1); err != nil {
		return result{}, err
	}
	t, err := a.resolve(f.positional[0], true)
	if err != nil {
		return result{}, err
	}
	times := t.job.Schedule.NextN(a.Now(), *n)
	var b strings.Builder
	for _, tm := range times {
		fmt.Fprintln(&b, formatTime(tm, t.project.Location))
	}
	if times == nil {
		times = []time.Time{}
	}
	return result{data: map[string]any{"job": t.job.Key(), "schedule": t.job.ScheduleExpr, "timezone": t.project.Timezone, "next": times}, text: b.String()}, nil
}
