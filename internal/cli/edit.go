package cli

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"strings"

	"github.com/skelz0r/ags/internal/config"
	"github.com/skelz0r/ags/internal/diff"
	"github.com/skelz0r/ags/internal/state"
)

func (a *App) writeEdit(t *target, file string, edit config.Edit) (*config.Project, error) {
	return a.writeEdits(t, map[string]config.Edit{file: edit}, []string{file})
}

func (a *App) writeEdits(t *target, edits map[string]config.Edit, order []string) (*config.Project, error) {
	updated := map[string][]byte{}
	for file, edit := range edits {
		data, err := os.ReadFile(file)
		if err != nil && !errors.Is(err, fs.ErrNotExist) {
			return nil, err
		}
		if len(strings.TrimSpace(string(data))) == 0 && file == t.project.SharedFile {
			data = []byte("version: 1\n")
		}
		if updated[file], err = config.ApplyEdit(data, edit); err != nil {
			return nil, failure(CodeError, "%s: %v", file, err)
		}
	}
	m, machineIssues := a.loadMachine()
	p, issues := config.Loader{Machine: m, Overrides: updated, SkipGit: true}.Load(t.project.Name, t.project.Dir)
	if issues = append(machineIssues, issues...); len(issues) > 0 {
		return nil, invalid(map[string]any{"errors": issues}, issues)
	}
	for _, file := range order {
		data, ok := updated[file]
		if !ok {
			continue
		}
		mode := os.FileMode(0o644)
		if info, err := os.Stat(file); err == nil {
			mode = info.Mode().Perm()
		}
		if err := config.WriteFileAtomic(file, data, mode); err != nil {
			return nil, err
		}
	}
	return p, nil
}

func (a *App) setEnabled(args []string, name string, enabled bool) (result, error) {
	f := newFlags(name)
	if err := f.parse(args, 1, 1); err != nil {
		return result{}, err
	}
	t, err := a.resolve(f.positional[0], false)
	if err != nil {
		return result{}, err
	}
	jobs := t.project.Jobs
	if t.job != nil {
		jobs = []*config.Job{t.job}
	}
	value := fmt.Sprint(enabled)
	var changed []string
	for _, j := range jobs {
		if j.Enabled == enabled {
			continue
		}
		if _, err := a.writeEdit(t, t.project.LocalFile, config.SetJobField(j.Name, "enabled", value)); err != nil {
			return result{}, err
		}
		changed = append(changed, j.Key())
	}
	verb := map[bool]string{true: "resumed", false: "paused"}[enabled]
	text := "nothing to do"
	if len(changed) > 0 {
		text = verb + " " + strings.Join(changed, ", ") + " in " + config.LocalFileName
	}
	if changed == nil {
		changed = []string{}
	}
	return result{data: map[string]any{verb: changed}, text: text}, nil
}

func (a *App) pause(args []string) (result, error)  { return a.setEnabled(args, "pause", false) }
func (a *App) resume(args []string) (result, error) { return a.setEnabled(args, "resume", true) }

type approvalView struct {
	Job         string `json:"job"`
	First       bool   `json:"first"`
	Diff        string `json:"diff"`
	Fingerprint string `json:"fingerprint"`
}

func (a *App) approve(args []string) (result, error) {
	f := newFlags("approve")
	yes := f.Bool("yes", false, "")
	reviewed := f.String("fingerprints", "", "")
	if err := f.parse(args, 1, 1); err != nil {
		return result{}, err
	}
	t, err := a.resolve(f.positional[0], false)
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
	jobs := t.project.Jobs
	if t.job != nil {
		jobs = []*config.Job{t.job}
	}
	var pending []*config.Job
	views := []approvalView{}
	var b strings.Builder
	for _, j := range jobs {
		if st.Approved(j) {
			continue
		}
		pending = append(pending, j)
		previous, seen := st.Approvals[j.Key()]
		v := approvalView{Job: j.Key(), First: !seen, Fingerprint: j.Fingerprint}
		if seen {
			v.Diff = diff.Lines(previous.Text, j.ApprovalText)
		} else {
			v.Diff = diff.Lines("", j.ApprovalText)
		}
		views = append(views, v)
		fmt.Fprintf(&b, "--- %s", j.Key())
		if v.First {
			b.WriteString(" (never approved)")
		}
		fmt.Fprintf(&b, "\n%s\n", v.Diff)
	}
	if len(pending) == 0 {
		return result{data: map[string]any{"approved": []string{}, "pending": views}, text: "nothing to approve"}, nil
	}
	if *reviewed != "" {
		var current []string
		for _, v := range views {
			current = append(current, v.Fingerprint)
		}
		if strings.Join(current, ",") != *reviewed {
			return result{}, &Error{Code: CodeError, Message: "the jobs to approve changed since they were reviewed: review the diff again", exit: ExitError, data: map[string]any{"approved": []string{}, "pending": views}}
		}
	}
	if !*yes {
		if a.json || !a.IsTTY {
			return result{}, &Error{Code: CodeUsage, Message: fmt.Sprintf("%d job(s) to approve: review the diff and pass --yes", len(pending)), exit: ExitInvalid, data: map[string]any{"approved": []string{}, "pending": views}}
		}
		fmt.Fprint(a.Stdout, b.String())
		if !a.confirm(fmt.Sprintf("Approve %d job(s)?", len(pending))) {
			return result{}, failure(CodeError, "not approved")
		}
		b.Reset()
	}
	if err := approveJobs(store, pending, a.Now()); err != nil {
		return result{}, err
	}
	approved := make([]string, len(pending))
	for i, j := range pending {
		approved[i] = j.Key()
	}
	fmt.Fprintf(&b, "approved %s", strings.Join(approved, ", "))
	return result{data: map[string]any{"approved": approved, "pending": views}, text: b.String()}, nil
}

func (a *App) newJob(args []string) (result, error) {
	f := newFlags("new")
	schedule := f.String("schedule", "", "")
	prompt := f.String("prompt", "", "")
	promptFile := f.String("prompt-file", "", "")
	command := f.String("command", "", "")
	precheck := f.String("precheck", "", "")
	catchUp := f.String("catch-up", "", "")
	local := f.Bool("local", false, "")
	if err := f.parse(args, 1, 1); err != nil {
		return result{}, err
	}
	if *schedule == "" || (*prompt == "") == (*promptFile == "") {
		return result{}, usageError("usage: ags %s", commands["new"].summary)
	}
	projectName, jobName := splitTarget(f.positional[0])
	if jobName == "" {
		return result{}, usageError("expected <project>/<job>, got %q", f.positional[0])
	}
	t, err := a.resolve(projectName, false)
	if err != nil {
		return result{}, err
	}
	t.jobName = jobName
	if t.project.Job(t.jobName) != nil {
		return result{}, failure(CodeError, "job %s/%s already exists", t.project.Name, t.jobName)
	}
	fields := []config.KV{{Key: "schedule", Value: *schedule}}
	for _, kv := range []config.KV{{Key: "prompt", Value: *prompt}, {Key: "prompt_file", Value: *promptFile}, {Key: "command", Value: *command}, {Key: "precheck", Value: *precheck}, {Key: "catch_up", Value: *catchUp}} {
		if kv.Value != "" {
			fields = append(fields, kv)
		}
	}
	file := t.project.SharedFile
	if *local {
		file = t.project.LocalFile
	}
	p, err := a.writeEdit(t, file, config.AddJob(t.jobName, fields))
	if err != nil {
		return result{}, err
	}
	job := p.Job(t.jobName)
	text := fmt.Sprintf("added %s to %s", job.Key(), file)
	if job.NeedsApproval() {
		store, err := a.store()
		if err != nil {
			return result{}, err
		}
		if err := approveJobs(store, []*config.Job{job}, a.Now()); err != nil {
			return result{}, err
		}
		text += ", approved since you wrote it"
	}
	st := (*state.State)(nil)
	if store, err := a.store(); err == nil {
		st, _ = store.Read()
	}
	return result{data: jobView(job, st, a.Now(), 3, a.Getenv("HOME")), text: text}, nil
}

func (a *App) edit(args []string) (result, error) {
	f := newFlags("edit")
	if err := f.parse(args, 1, 1); err != nil {
		return result{}, err
	}
	t, err := a.resolve(f.positional[0], true)
	if err != nil {
		return result{}, err
	}
	if a.json || !a.IsTTY {
		return result{data: map[string]any{"file": t.job.File, "line": t.job.Line}, text: fmt.Sprintf("%s:%d", t.job.File, t.job.Line)}, nil
	}
	if err := OpenEditor(a.Getenv("EDITOR"), t.job.File, t.job.Line); err != nil {
		return result{}, err
	}
	return a.validate([]string{t.project.Dir})
}

func OpenEditor(editor, file string, line int) error {
	if editor == "" {
		editor = "vi"
	}
	parts := strings.Fields(editor)
	args := append(parts[1:], fmt.Sprintf("+%d", line), file)
	cmd := exec.Command(parts[0], args...)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
	return cmd.Run()
}

func (a *App) deleteJob(args []string) (result, error) {
	f := newFlags("delete")
	yes := f.Bool("yes", false, "")
	if err := f.parse(args, 1, 1); err != nil {
		return result{}, err
	}
	t, err := a.resolve(f.positional[0], true)
	if err != nil {
		return result{}, err
	}
	if !*yes && !a.confirm(fmt.Sprintf("Delete %s from %s?", t.job.Key(), t.job.File)) {
		return result{}, usageError("pass --yes to delete %s", t.job.Key())
	}
	edits := map[string]config.Edit{t.job.File: config.DeleteJob(t.job.Name)}
	if t.job.Origin == config.OriginShared && len(t.job.LocalOverrides) > 0 {
		edits[t.project.LocalFile] = config.DeleteJob(t.job.Name)
	}
	if _, err := a.writeEdits(t, edits, []string{t.job.File, t.project.LocalFile}); err != nil {
		return result{}, err
	}
	return result{data: map[string]any{"deleted": t.job.Key(), "file": t.job.File}, text: "deleted " + t.job.Key() + " from " + t.job.File}, nil
}
