package cli

import (
	"bufio"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/skelz0r/ags/internal/config"
	"github.com/skelz0r/ags/internal/notify"
	"github.com/skelz0r/ags/internal/paths"
	"github.com/skelz0r/ags/internal/runner"
	"github.com/skelz0r/ags/internal/state"
)

const (
	ExitOK      = 0
	ExitError   = 1
	ExitInvalid = 2

	CodeUsage         = "usage"
	CodeInvalidConfig = "invalid_config"
	CodeNotFound      = "not_found"
	CodeInsideRun     = "inside_run"
	CodeUnapproved    = "unapproved"
	CodeBusy          = "busy"
	CodeRunFailed     = "run_failed"
	CodeError         = "error"
)

type App struct {
	Getenv   func(string) string
	Stdin    io.Reader
	Stdout   io.Writer
	Stderr   io.Writer
	Now      func() time.Time
	IsTTY    bool
	Notifier runner.Notifier
	OpenTUI  func(*App) error
	Exe      func() (string, error)

	json    bool
	paths   paths.Paths
	reader  *bufio.Reader
	machine *config.Machine
}

type Error struct {
	Code    string `json:"code"`
	Message string `json:"message"`
	exit    int
	data    any
}

func (e *Error) Error() string { return e.Message }

func usageError(format string, args ...any) *Error {
	return &Error{Code: CodeUsage, Message: fmt.Sprintf(format, args...), exit: ExitInvalid}
}

func failure(code, format string, args ...any) *Error {
	return &Error{Code: code, Message: fmt.Sprintf(format, args...), exit: ExitError}
}

func invalid(data any, issues config.Issues) *Error {
	return &Error{Code: CodeInvalidConfig, Message: issues.Error(), exit: ExitInvalid, data: data}
}

type result struct {
	data any
	text string
}

type handler func(a *App, args []string) (result, error)

type command struct {
	run     handler
	editing bool
	summary string
}

var commands map[string]command

func init() {
	commands = map[string]command{
		"add":       {run: (*App).add, editing: true, summary: "add <path> [--name slug] [--approve]"},
		"remove":    {run: (*App).remove, editing: true, summary: "remove <project>"},
		"projects":  {run: (*App).projects, summary: "projects"},
		"validate":  {run: (*App).validate, summary: "validate [path]"},
		"jobs":      {run: (*App).jobs, summary: "jobs [--project p]"},
		"next":      {run: (*App).next, summary: "next <project/job> [-n 5]"},
		"run":       {run: (*App).runJob, summary: "run <project/job> [--skip-precheck] [--dry-run]"},
		"runs":      {run: (*App).runs, summary: "runs [--project p] [--job j] [--status s] [--since 7d] [--limit n]"},
		"show":      {run: (*App).show, summary: "show <run>"},
		"pause":     {run: (*App).pause, editing: true, summary: "pause <project[/job]>"},
		"resume":    {run: (*App).resume, editing: true, summary: "resume <project[/job]>"},
		"approve":   {run: (*App).approve, editing: true, summary: "approve <project[/job]> [--yes] [--fingerprints f1,f2]"},
		"new":       {run: (*App).newJob, editing: true, summary: "new <project/job> --schedule cron --prompt text [--local] [--command c] [--precheck sh]"},
		"edit":      {run: (*App).edit, editing: true, summary: "edit <project/job>"},
		"delete":    {run: (*App).deleteJob, editing: true, summary: "delete <project/job> [--yes]"},
		"install":   {run: (*App).install, editing: true, summary: "install"},
		"uninstall": {run: (*App).uninstall, editing: true, summary: "uninstall"},
		"daemon":    {run: (*App).daemon, summary: "daemon"},
		"doctor":    {run: (*App).doctor, summary: "doctor"},
	}
}

func Main(args []string, openTUI func(*App) error) int {
	app := &App{
		Getenv:  os.Getenv,
		Stdin:   os.Stdin,
		Stdout:  os.Stdout,
		Stderr:  os.Stderr,
		Now:     time.Now,
		IsTTY:   isTerminal(os.Stdin) && isTerminal(os.Stdout),
		Exe:     os.Executable,
		OpenTUI: openTUI,
	}
	app.Notifier = notify.MacOS{Machine: func() *config.Machine {
		m, _ := config.LoadMachine(paths.FromEnv().MachineConfig())
		return m
	}}
	return app.Run(args)
}

func (a *App) Run(args []string) int {
	var rest []string
	for _, arg := range args {
		if arg == "--json" {
			a.json = true
			continue
		}
		rest = append(rest, arg)
	}
	if a.Now == nil {
		a.Now = time.Now
	}
	if a.Exe == nil {
		a.Exe = os.Executable
	}
	a.paths = paths.Resolve(a.Getenv)
	a.reader = bufio.NewReader(a.Stdin)

	if len(rest) == 0 {
		if a.json || a.OpenTUI == nil {
			return a.emit(result{}, usageError("usage: ags <command>, see ags help"))
		}
		if err := a.OpenTUI(a); err != nil {
			fmt.Fprintln(a.Stderr, "ags:", err)
			return ExitError
		}
		return ExitOK
	}
	name := rest[0]
	if name == "help" || name == "--help" || name == "-h" {
		return a.emit(result{text: helpText()}, nil)
	}
	cmd, ok := commands[name]
	if !ok {
		return a.emit(result{}, usageError("unknown command %q, see ags help", name))
	}
	if cmd.editing {
		if err := a.refuseInsideRun(name); err != nil {
			return a.emit(result{}, err)
		}
	}
	res, err := cmd.run(a, rest[1:])
	return a.emit(res, err)
}

func (a *App) refuseInsideRun(name string) error {
	if runID := a.Getenv("AGS_RUN_ID"); runID != "" {
		return failure(CodeInsideRun, "ags %s refuses to run inside a scheduled run (AGS_RUN_ID=%s)", name, runID)
	}
	return nil
}

func helpText() string {
	var b strings.Builder
	b.WriteString("ags: schedule coding agents\n\nusage:\n  ags                 open the TUI\n")
	for _, name := range []string{"add", "remove", "projects", "validate", "jobs", "next", "run", "runs", "show", "pause", "resume", "approve", "new", "edit", "delete", "install", "uninstall", "daemon", "doctor"} {
		fmt.Fprintf(&b, "  ags %s\n", commands[name].summary)
	}
	b.WriteString("\nevery command accepts --json: {\"ok\", \"data\", \"error\": {\"code\", \"message\"}}\nexit codes: 0 ok, 1 error, 2 invalid config or usage\n")
	return b.String()
}

type envelope struct {
	OK    bool   `json:"ok"`
	Data  any    `json:"data"`
	Error *Error `json:"error"`
}

func (a *App) emit(res result, err error) int {
	var cliErr *Error
	if err != nil && !errors.As(err, &cliErr) {
		cliErr = failure(CodeError, "%v", err)
	}
	if a.json {
		env := envelope{OK: cliErr == nil, Data: res.data, Error: cliErr}
		if cliErr != nil && cliErr.data != nil {
			env.Data = cliErr.data
		}
		enc := json.NewEncoder(a.Stdout)
		enc.SetIndent("", "  ")
		enc.SetEscapeHTML(false)
		enc.Encode(env)
	} else {
		if res.text != "" {
			fmt.Fprint(a.Stdout, strings.TrimRight(res.text, "\n")+"\n")
		}
		if cliErr != nil {
			fmt.Fprintln(a.Stderr, "ags: "+cliErr.Message)
		}
	}
	if cliErr != nil {
		return cliErr.exit
	}
	return ExitOK
}

type flags struct {
	*flag.FlagSet
	positional []string
}

func newFlags(name string) *flags {
	fs := flag.NewFlagSet(name, flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	return &flags{FlagSet: fs}
}

func (f *flags) parse(args []string, minPos, maxPos int) error {
	for {
		if err := f.FlagSet.Parse(args); err != nil {
			return usageError("%s: %v", f.Name(), err)
		}
		args = f.FlagSet.Args()
		if len(args) == 0 {
			break
		}
		f.positional = append(f.positional, args[0])
		args = args[1:]
	}
	if len(f.positional) < minPos || len(f.positional) > maxPos {
		return usageError("usage: ags %s", commands[f.Name()].summary)
	}
	return nil
}

func (a *App) store() (*state.Store, error) {
	return state.Open(a.paths)
}

func (a *App) loadMachine() (*config.Machine, config.Issues) {
	m, issues := config.LoadMachine(a.paths.MachineConfig())
	a.machine = m
	return m, issues
}

func (a *App) registry() (*config.Registry, error) {
	return config.LoadRegistry(a.paths.Registry())
}

func (a *App) expandPath(p string) (string, error) {
	p = paths.ExpandHome(p, a.Getenv("HOME"))
	abs, err := filepath.Abs(p)
	if err != nil {
		return "", err
	}
	if resolved, err := filepath.EvalSymlinks(abs); err == nil {
		abs = resolved
	}
	return abs, nil
}

func (a *App) confirm(question string) bool {
	if !a.IsTTY || a.json {
		return false
	}
	fmt.Fprintf(a.Stdout, "%s [y/N] ", question)
	line, _ := a.reader.ReadString('\n')
	answer := strings.ToLower(strings.TrimSpace(line))
	return answer == "y" || answer == "yes"
}

type target struct {
	entry   config.Entry
	project *config.Project
	job     *config.Job
	jobName string
}

func splitTarget(s string) (string, string) {
	project, job, _ := strings.Cut(s, "/")
	return project, job
}

func (a *App) resolve(spec string, needJob bool) (*target, error) {
	projectName, jobName := splitTarget(spec)
	if needJob && jobName == "" {
		return nil, usageError("expected <project>/<job>, got %q", spec)
	}
	reg, err := a.registry()
	if err != nil {
		return nil, err
	}
	entry := reg.Find(projectName)
	if entry == nil {
		return nil, failure(CodeNotFound, "project %q is not registered, see ags projects", projectName)
	}
	m, machineIssues := a.loadMachine()
	p, issues := config.Loader{Machine: m}.Load(entry.Name, entry.Path)
	if all := append(machineIssues, issues...); len(all) > 0 {
		return nil, invalid(map[string]any{"errors": all}, all)
	}
	t := &target{entry: *entry, project: p, jobName: jobName}
	if jobName != "" {
		t.job = p.Job(jobName)
		if t.job == nil {
			return nil, failure(CodeNotFound, "job %q not found in project %q", jobName, projectName)
		}
	}
	return t, nil
}

func isTerminal(f *os.File) bool {
	info, err := f.Stat()
	return err == nil && info.Mode()&os.ModeCharDevice != 0
}
