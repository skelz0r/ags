package cli

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"sort"
	"strings"
	"syscall"
	"time"

	"github.com/skelz0r/ags"
	"github.com/skelz0r/ags/internal/config"
	"github.com/skelz0r/ags/internal/daemon"
	"github.com/skelz0r/ags/internal/paths"
	"github.com/skelz0r/ags/internal/service"
	"github.com/skelz0r/ags/internal/state"
)

const starterConfig = `default_command: claude

preprompt: |
  This run was started by ags; nobody is watching. Do not ask
  questions: if something blocks you, stop and say why.

commands:
  claude:
    argv: ["claude", "-p", "--output-format", "json"]
    stdin: prompt
    timeout: 45m

notifications:
  on: [failed, missed, precheck_error, invalid, unapproved]
  channels: [desktop]
`

func (a *App) executable() (string, error) {
	exe, err := a.Exe()
	if err != nil {
		return "", err
	}
	if resolved, err := filepath.EvalSymlinks(exe); err == nil {
		exe = resolved
	}
	if strings.Contains(exe, "/go-build") {
		return "", failure(CodeError, "ags runs from a temporary go build (%s): install it first (go install ./cmd/ags)", exe)
	}
	return exe, nil
}

func (a *App) daemonService(exe string) service.Daemon {
	env := map[string]string{"PATH": a.Getenv("PATH"), "HOME": a.Getenv("HOME")}
	if a.paths.Isolated {
		env["AGS_HOME"] = a.Getenv("AGS_HOME")
	} else {
		env["XDG_CONFIG_HOME"] = filepath.Dir(a.paths.ConfigDir)
		env["XDG_STATE_HOME"] = filepath.Dir(a.paths.StateDir)
	}
	return service.Daemon{Program: []string{exe, "daemon"}, Env: env, LogPath: a.paths.DaemonLog()}
}

func (a *App) install(args []string) (result, error) {
	f := newFlags("install")
	if err := f.parse(args, 0, 0); err != nil {
		return result{}, err
	}
	exe, err := a.executable()
	if err != nil {
		return result{}, err
	}
	var b strings.Builder
	if _, err := os.Stat(a.paths.MachineConfig()); errors.Is(err, fs.ErrNotExist) {
		if err := config.WriteFileAtomic(a.paths.MachineConfig(), []byte(starterConfig), 0o644); err != nil {
			return result{}, err
		}
		fmt.Fprintf(&b, "wrote a starter %s\n", a.paths.MachineConfig())
	}
	if _, err := a.store(); err != nil {
		return result{}, err
	}
	skills, err := a.installSkills()
	if err != nil {
		return result{}, err
	}
	fmt.Fprintf(&b, "linked skills %s into %s\n", strings.Join(skills, ", "), a.paths.ClaudeSkills)
	if err := config.WriteFileAtomic(a.paths.ServiceFile(), a.daemonService(exe).Definition(), 0o644); err != nil {
		return result{}, err
	}
	fmt.Fprintf(&b, "wrote %s (daemon: %s daemon)\n", a.paths.ServiceFile(), exe)
	loaded := false
	if a.paths.Isolated {
		fmt.Fprintf(&b, "AGS_HOME is set: the %s is not loaded\n", service.Kind)
	} else {
		if err := service.Load(a.paths.ServiceFile()); err != nil {
			return result{}, err
		}
		loaded = true
		fmt.Fprintf(&b, "%s %s loaded\n", service.Kind, service.Name)
	}
	return result{data: map[string]any{"service_file": a.paths.ServiceFile(), "executable": exe, "skills": skills, "loaded": loaded}, text: b.String()}, nil
}

func (a *App) installSkills() ([]string, error) {
	entries, err := fs.ReadDir(ags.Skills, "skills")
	if err != nil {
		return nil, err
	}
	var names []string
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		name := e.Name()
		copyDir := filepath.Join(a.paths.SkillsCopy(), name)
		if err := os.RemoveAll(copyDir); err != nil {
			return nil, err
		}
		err := fs.WalkDir(ags.Skills, "skills/"+name, func(path string, d fs.DirEntry, err error) error {
			if err != nil || d.IsDir() {
				return err
			}
			data, err := ags.Skills.ReadFile(path)
			if err != nil {
				return err
			}
			return config.WriteFileAtomic(filepath.Join(a.paths.SkillsCopy(), strings.TrimPrefix(path, "skills/")), data, 0o644)
		})
		if err != nil {
			return nil, err
		}
		link := filepath.Join(a.paths.ClaudeSkills, name)
		if info, err := os.Lstat(link); err == nil {
			if info.Mode()&os.ModeSymlink == 0 {
				return nil, failure(CodeError, "%s exists and is not a link made by ags", link)
			}
			os.Remove(link)
		}
		if err := os.MkdirAll(a.paths.ClaudeSkills, 0o755); err != nil {
			return nil, err
		}
		if err := os.Symlink(copyDir, link); err != nil {
			return nil, err
		}
		names = append(names, name)
	}
	return names, nil
}

func (a *App) uninstall(args []string) (result, error) {
	f := newFlags("uninstall")
	if err := f.parse(args, 0, 0); err != nil {
		return result{}, err
	}
	var b strings.Builder
	if !a.paths.Isolated {
		if err := service.Unload(); err != nil {
			return result{}, err
		}
	}
	if err := os.Remove(a.paths.ServiceFile()); err == nil {
		fmt.Fprintf(&b, "removed %s\n", a.paths.ServiceFile())
	}
	links, _ := filepath.Glob(filepath.Join(a.paths.ClaudeSkills, "ags-*"))
	for _, link := range links {
		if target, err := os.Readlink(link); err == nil && strings.HasPrefix(target, a.paths.SkillsCopy()) {
			os.Remove(link)
			fmt.Fprintf(&b, "removed %s\n", link)
		}
	}
	b.WriteString("config, registry and history are kept")
	return result{data: map[string]any{"service_file": a.paths.ServiceFile()}, text: b.String()}, nil
}

func (a *App) daemon(args []string) (result, error) {
	f := newFlags("daemon")
	if err := f.parse(args, 0, 0); err != nil {
		return result{}, err
	}
	if _, err := a.store(); err != nil {
		return result{}, err
	}
	log, err := os.OpenFile(a.paths.DaemonLog(), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return result{}, err
	}
	defer log.Close()
	d, err := daemon.New(daemon.Options{Paths: a.paths, Notifier: a.Notifier, Log: log, Now: a.Now})
	if err != nil {
		return result{}, err
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := d.Run(ctx); err != nil {
		return result{}, failure(CodeBusy, "%v", err)
	}
	return result{text: "daemon stopped"}, nil
}

type check struct {
	Name   string `json:"name"`
	OK     bool   `json:"ok"`
	Detail string `json:"detail"`
}

func DaemonAlive(st *state.State, now time.Time) (bool, string) {
	if st.Daemon.LastTick.IsZero() {
		return false, "never ticked"
	}
	age := now.Sub(st.Daemon.LastTick).Round(time.Second)
	if !state.ProcessAlive(st.Daemon.PID) {
		return false, fmt.Sprintf("pid %d not running, last tick %s ago", st.Daemon.PID, age)
	}
	if age > 3*daemon.TickInterval {
		return false, fmt.Sprintf("pid %d alive but last tick %s ago", st.Daemon.PID, age)
	}
	return true, fmt.Sprintf("pid %d, last tick %s ago", st.Daemon.PID, age)
}

func brokenPrechecks(store *state.Store, now time.Time) []*state.Run {
	runs, _ := store.ListRuns(state.Filter{})
	since := now.Add(-7 * 24 * time.Hour)
	latest := map[string]*state.Run{}
	for _, r := range runs {
		if precheckRanAt(r).Before(since) || !precheckRan(store, r) {
			continue
		}
		if prev := latest[r.Key()]; prev == nil || precheckRanAt(r).After(precheckRanAt(prev)) {
			latest[r.Key()] = r
		}
	}
	var broken []*state.Run
	for _, r := range latest {
		if r.Status == state.StatusPrecheckError {
			broken = append(broken, r)
		}
	}
	sort.Slice(broken, func(i, j int) bool { return broken[i].Key() < broken[j].Key() })
	return broken
}

func unreportedRuns(store *state.Store, m *config.Machine, now time.Time) []*state.Run {
	runs, _ := store.ListRuns(state.Filter{Status: state.StatusSucceeded})
	var unreported []*state.Run
	for _, r := range runs {
		if !r.AwaitsReport || r.Report != nil || r.StartedAt == nil {
			continue
		}
		age, within := now.Sub(*r.StartedAt), reportWithin(m, r)
		if within > 0 && age > within && age <= 7*24*time.Hour {
			unreported = append(unreported, r)
		}
	}
	sort.Slice(unreported, func(i, j int) bool { return unreported[i].ID < unreported[j].ID })
	return unreported
}

func reportWithin(m *config.Machine, r *state.Run) time.Duration {
	if cmd := m.Commands[r.Command]; cmd != nil {
		return cmd.ReportWithin
	}
	return config.DefaultReportWithin
}

func precheckRan(store *state.Store, r *state.Run) bool {
	switch {
	case r.Status == state.StatusPrecheckError, r.Status == state.StatusSkipped && strings.HasPrefix(r.Reason, state.ReasonPrecheck):
		return true
	case r.Status == state.StatusSucceeded, r.Status == state.StatusFailed:
		_, err := os.Stat(filepath.Join(store.RunDir(r), "precheck.log"))
		return err == nil
	}
	return false
}

func precheckRanAt(r *state.Run) time.Time {
	switch {
	case r.SkippedLast != nil:
		return *r.SkippedLast
	case r.StartedAt != nil:
		return *r.StartedAt
	}
	return r.ScheduledFor
}

func (a *App) doctor(args []string) (result, error) {
	f := newFlags("doctor")
	if err := f.parse(args, 0, 0); err != nil {
		return result{}, err
	}
	var checks []check
	add := func(name string, ok bool, format string, args ...any) {
		checks = append(checks, check{Name: name, OK: ok, Detail: fmt.Sprintf(format, args...)})
	}
	if _, err := os.Stat(a.paths.ServiceFile()); err != nil {
		add("service", false, "%s missing: run ags install", a.paths.ServiceFile())
	} else if a.paths.Isolated {
		add("service", true, "%s written, not loaded (AGS_HOME set)", a.paths.ServiceFile())
	} else if service.Loaded() {
		add("service", true, "%s %s loaded", service.Kind, service.Name)
	} else {
		add("service", false, "%s %s not loaded: run ags install", service.Kind, service.Name)
	}
	store, err := a.store()
	if err != nil {
		return result{}, err
	}
	st, err := store.Read()
	if err != nil {
		return result{}, err
	}
	alive, detail := DaemonAlive(st, a.Now())
	add("daemon", alive, "%s", detail)

	m, issues := a.loadMachine()
	switch {
	case !m.Exists:
		add("machine config", false, "%s missing: run ags install", a.paths.MachineConfig())
	case len(issues) > 0:
		add("machine config", false, "%s", issues.Error())
	default:
		add("machine config", true, "%s, default command %s", a.paths.MachineConfig(), orNone(m.DefaultCommand))
	}
	for name, cmd := range m.Commands {
		program := paths.ExpandHome(cmd.Argv[0], a.Getenv("HOME"))
		if path, err := exec.LookPath(program); err != nil {
			add("command "+name, false, "%s not found in PATH", program)
		} else {
			add("command "+name, true, "%s", path)
		}
	}
	reg, err := a.registry()
	if err != nil {
		add("registry", false, "%v", err)
	} else {
		for _, entry := range reg.Projects {
			p, issues := config.Loader{Machine: m}.Load(entry.Name, entry.Path)
			switch {
			case len(issues) > 0:
				add("project "+entry.Name, false, "%s", issues.Error())
			case len(p.Warnings) > 0:
				add("project "+entry.Name, false, "%d job(s); %s", len(p.Jobs), p.Warnings.Error())
			default:
				unapproved := 0
				for _, j := range p.Jobs {
					if !st.Approved(j) {
						unapproved++
					}
				}
				add("project "+entry.Name, unapproved == 0, "%s, %d job(s), %d unapproved", paths.Abbreviate(entry.Path, a.Getenv("HOME")), len(p.Jobs), unapproved)
			}
		}
	}
	for key, e := range st.ConfigErrors {
		add("daemon sees "+key, false, "since %s: %s", e.Since.Local().Format(time.RFC3339), e.Message)
	}
	for _, r := range brokenPrechecks(store, a.Now()) {
		add("precheck "+r.Key(), false, "%s at %s: %s, see ags show %s", r.Status, precheckRanAt(r).Local().Format(time.RFC3339), r.Reason, r.ID)
	}
	for _, r := range unreportedRuns(store, m, a.Now()) {
		add("report "+r.Key(), false, "run %s started %s, no ags report after %s, see ags show %s", r.ID, r.StartedAt.Local().Format(time.RFC3339), config.FormatDuration(reportWithin(m, r)), r.ID)
	}
	var b strings.Builder
	failed := 0
	for _, c := range checks {
		mark := "ok  "
		if !c.OK {
			mark = "FAIL"
			failed++
		}
		fmt.Fprintf(&b, "%s %s: %s\n", mark, c.Name, c.Detail)
	}
	if failed > 0 {
		return result{text: b.String()}, &Error{Code: CodeError, Message: fmt.Sprintf("%d check(s) failed", failed), exit: ExitError, data: checks}
	}
	return result{data: checks, text: b.String()}, nil
}
