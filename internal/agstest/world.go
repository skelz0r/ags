package agstest

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/skelz0r/ags/internal/cli"
	"github.com/skelz0r/ags/internal/daemon"
	"github.com/skelz0r/ags/internal/paths"
)

const MachineConfig = `default_command: record
preprompt: Machine preprompt.
precheck_timeout: 1s
commands:
  record:
    argv: ["sh", "-c", "cat; echo done"]
    timeout: 10s
    report_within: never
  agent:
    argv: ["sh", "-c", "cat > /dev/null; echo handed-off"]
    timeout: 10s
  waiting-agent:
    argv: ["sh", "-c", "cat > /dev/null; while [ ! -e \"$AGS_JOB_STATE_DIR/go\" ]; do sleep 0.05; done"]
    timeout: 10s
  slow:
    argv: ["sleep", "30"]
    stdin: none
    timeout: 1m
  short-timeout:
    argv: ["sleep", "30"]
    stdin: none
    timeout: 1s
  stubborn:
    argv: ["sh", "-c", "(trap '' TERM; exec sh -c 'echo $$ > child.pid; exec sleep 30') & wait"]
    stdin: none
    timeout: 1s
  failing:
    argv: ["sh", "-c", "echo broken >&2; exit 3"]
  prompt-arg:
    argv: ["sh", "-c", "printf '%s' \"$1\"", "sh", "{prompt}"]
    prompt_arg: true
    report_within: never
  path-and-prompt:
    argv: ["sh", "-c", "printf '%s' \"$1\"", "sh", "{project.path}|{prompt}"]
    prompt_arg: true
    report_within: never
  last-success:
    argv: ["sh", "-c", "echo \"$AGS_LAST_SUCCESS_AT\"; test ! -e fail"]
    stdin: none
  memory:
    argv: ["sh", "-c", "cat \"$AGS_JOB_STATE_DIR/seen\""]
    stdin: none
notifications:
  on: [failed, missed, precheck_error, invalid, unapproved]
  channels: [desktop]
`

var Paris = mustLoad("Europe/Paris")

func mustLoad(name string) *time.Location {
	loc, err := time.LoadLocation(name)
	if err != nil {
		panic(err)
	}
	return loc
}

type Notification struct {
	Event, Title, Message string
}

type Recorder struct {
	mu    sync.Mutex
	Notes []Notification
}

func (r *Recorder) Notify(event, title, message string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.Notes = append(r.Notes, Notification{event, title, message})
}

func (r *Recorder) Count(event string) int {
	r.mu.Lock()
	defer r.mu.Unlock()
	n := 0
	for _, note := range r.Notes {
		if note.Event == event {
			n++
		}
	}
	return n
}

type World struct {
	T          *testing.T
	Root       string
	Home       string
	ProjectDir string
	Paths      paths.Paths
	Now        time.Time
	Notes      *Recorder
	Env        map[string]string
	daemons    []*daemon.Daemon
}

func New(t *testing.T) *World {
	t.Helper()
	root := t.TempDir()
	w := &World{
		T:          t,
		Root:       root,
		Home:       filepath.Join(root, "home"),
		ProjectDir: filepath.Join(root, "proj"),
		Notes:      &Recorder{},
		Now:        time.Date(2026, 10, 19, 8, 0, 0, 0, Paris),
	}
	w.Env = map[string]string{"AGS_HOME": filepath.Join(root, "ags"), "HOME": w.Home, "PATH": os.Getenv("PATH")}
	w.Paths = paths.Resolve(w.getenv)
	for _, dir := range []string{w.Home, w.ProjectDir, w.Paths.ConfigDir} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	w.Write(w.Paths.MachineConfig(), MachineConfig)
	git := exec.Command("git", "init", "-q", w.ProjectDir)
	if out, err := git.CombinedOutput(); err != nil {
		t.Fatalf("git init: %v %s", err, out)
	}
	w.Write(filepath.Join(w.ProjectDir, ".gitignore"), ".ags.local.yml\n")
	t.Cleanup(func() {
		for _, d := range w.daemons {
			d.Stop()
		}
	})
	return w
}

func (w *World) getenv(key string) string { return w.Env[key] }

func (w *World) Write(path, content string) {
	w.T.Helper()
	if !filepath.IsAbs(path) {
		path = filepath.Join(w.ProjectDir, path)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		w.T.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		w.T.Fatal(err)
	}
}

func (w *World) SharedFile(content string) {
	w.Write(".ags.yml", content)
}

func (w *World) LocalFile(content string) {
	w.Write(".ags.local.yml", content)
}

type Result struct {
	Exit   int
	Stdout string
	Stderr string
	OK     bool            `json:"ok"`
	Data   json.RawMessage `json:"data"`
	Error  *struct {
		Code    string `json:"code"`
		Message string `json:"message"`
	} `json:"error"`
}

func (r Result) Decode(t *testing.T, v any) {
	t.Helper()
	if err := json.Unmarshal(r.Data, v); err != nil {
		t.Fatalf("decode %s: %v", r.Data, err)
	}
}

func (w *World) CLI(args ...string) Result {
	w.T.Helper()
	var stdout, stderr bytes.Buffer
	app := &cli.App{
		Getenv:   w.getenv,
		Stdin:    strings.NewReader(""),
		Stdout:   &stdout,
		Stderr:   &stderr,
		Now:      func() time.Time { return w.Now },
		Notifier: w.Notes,
	}
	res := Result{Exit: app.Run(args)}
	res.Stdout, res.Stderr = stdout.String(), stderr.String()
	for _, arg := range args {
		if arg == "--json" {
			if err := json.Unmarshal(stdout.Bytes(), &res); err != nil {
				w.T.Fatalf("ags %v: invalid JSON %q: %v", args, stdout.String(), err)
			}
		}
	}
	return res
}

func (w *World) MustCLI(args ...string) Result {
	w.T.Helper()
	res := w.CLI(args...)
	if res.Exit != 0 {
		w.T.Fatalf("ags %v exited %d: %s%s", args, res.Exit, res.Stdout, res.Stderr)
	}
	return res
}

func (w *World) Register() {
	w.T.Helper()
	w.MustCLI("add", w.ProjectDir, "--name", "proj", "--approve")
}

func (w *World) Daemon() *daemon.Daemon {
	w.T.Helper()
	if err := os.MkdirAll(w.Paths.StateDir, 0o700); err != nil {
		w.T.Fatal(err)
	}
	log, err := os.OpenFile(w.Paths.DaemonLog(), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		w.T.Fatal(err)
	}
	w.T.Cleanup(func() { log.Close() })
	d, err := daemon.New(daemon.Options{Paths: w.Paths, Notifier: w.Notes, Log: log, Now: func() time.Time { return w.Now }})
	if err != nil {
		w.T.Fatal(err)
	}
	w.daemons = append(w.daemons, d)
	return d
}

func (w *World) Tick(d *daemon.Daemon, at time.Time) {
	w.T.Helper()
	w.Now = at
	if err := d.Tick(at); err != nil {
		w.T.Fatal(err)
	}
}

func (w *World) TickEvery(d *daemon.Daemon, from, to time.Time, step time.Duration) {
	w.T.Helper()
	for at := from; !at.After(to); at = at.Add(step) {
		w.Tick(d, at)
		d.Wait()
	}
}

type Run struct {
	ID           string     `json:"id"`
	Job          string     `json:"job"`
	Trigger      string     `json:"trigger"`
	Status       string     `json:"status"`
	Reason       string     `json:"reason"`
	ScheduledFor time.Time  `json:"scheduled_for"`
	LateBy       string     `json:"late_by"`
	MissedCount  int        `json:"missed_count"`
	SkippedCount int        `json:"skipped_count"`
	SkippedLast  *time.Time `json:"skipped_last"`
	ExitCode     *int       `json:"exit_code"`
}

func (w *World) Runs(filters ...string) []Run {
	w.T.Helper()
	res := w.MustCLI(append([]string{"runs", "--json", "--limit", "10000"}, filters...)...)
	var runs []Run
	res.Decode(w.T, &runs)
	return runs
}

func (w *World) Show(id string) map[string]any {
	w.T.Helper()
	var detail map[string]any
	w.MustCLI("show", id, "--json").Decode(w.T, &detail)
	return detail
}

func Statuses(runs []Run) []string {
	out := make([]string, len(runs))
	for i := len(runs) - 1; i >= 0; i-- {
		label := runs[i].Status
		if runs[i].Reason != "" {
			label += " (" + strings.SplitN(runs[i].Reason, ":", 2)[0] + ")"
		}
		if runs[i].SkippedCount > 1 {
			label += fmt.Sprintf(" x%d", runs[i].SkippedCount)
		}
		out[len(runs)-1-i] = label
	}
	return out
}

func At(day, hour, minute, second int) time.Time {
	return time.Date(2026, 10, day, hour, minute, second, 0, Paris)
}
