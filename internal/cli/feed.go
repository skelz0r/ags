package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/signal"
	"sort"
	"strings"
	"syscall"
	"time"

	"github.com/mattn/go-runewidth"
	"golang.org/x/sys/unix"

	"github.com/skelz0r/ags/internal/config"
	"github.com/skelz0r/ags/internal/state"
)

const (
	DefaultPollInterval = time.Second

	FeedAgent = "agent"
	FeedAgs   = "ags"

	FeedNoReport = "no_report"

	feedKeyWidth   = 32
	feedLabelWidth = 9
	feedMinSummary = 30
	feedNarrowPad  = 4
)

type FeedEntry struct {
	At      time.Time `json:"at"`
	Run     string    `json:"run"`
	Project string    `json:"project"`
	Job     string    `json:"job"`
	Source  string    `json:"source"`
	Status  string    `json:"status"`
	Summary string    `json:"summary,omitempty"`
	Ref     string    `json:"ref,omitempty"`
}

func (e FeedEntry) hidden() bool {
	return e.Source == FeedAgent && e.Status == state.ReportNothing || e.Source == FeedAgs && e.Status == state.StatusSkipped
}

type feed struct {
	store   *state.Store
	machine *config.Machine
	project string
	job     string
	all     bool

	shown   map[string]bool
	pending map[string]*state.Run
	file    os.FileInfo
	offset  int64
	tail    []byte
}

const feedTailCheck = 256

func (a *App) feed(args []string) (result, error) {
	f := newFlags("feed")
	project := f.String("project", "", "")
	job := f.String("job", "", "")
	since := f.String("since", "24h", "")
	all := f.Bool("all", false, "")
	noFollow := f.Bool("no-follow", false, "")
	if err := f.parse(args, 0, 0); err != nil {
		return result{}, err
	}
	if p, j := splitTarget(*job); j != "" {
		*project, *job = p, j
	}
	window, err := config.ParseDuration(*since)
	if err != nil {
		return result{}, usageError("--since: %v", err)
	}
	store, err := a.store()
	if err != nil {
		return result{}, err
	}
	m, _ := a.loadMachine()
	fd := &feed{store: store, machine: m, project: *project, job: *job, all: *all, shown: map[string]bool{}, pending: map[string]*state.Run{}}
	out := a.feedWriter()

	now := a.Now()
	runs, err := fd.read()
	if err != nil {
		return result{}, err
	}
	from := now.Add(-window)
	backlog := append(fd.batch(runs, now), fd.overdue(now)...)
	sort.SliceStable(backlog, func(i, j int) bool { return backlog[i].At.Before(backlog[j].At) })
	for _, e := range backlog {
		if e.At.Before(from) {
			continue
		}
		if err := out(e); err != nil {
			return result{}, err
		}
	}
	if *noFollow {
		return result{streamed: true}, nil
	}

	ctx := a.Context
	if ctx == nil {
		ctx = context.Background()
	}
	ctx, stop := signal.NotifyContext(ctx, os.Interrupt, syscall.SIGTERM)
	defer stop()
	interval := a.PollInterval
	if interval <= 0 {
		interval = DefaultPollInterval
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return result{streamed: true}, nil
		case <-ticker.C:
		}
		now := a.Now()
		runs, err := fd.read()
		if err != nil {
			return result{}, err
		}
		for _, e := range append(fd.batch(runs, now), fd.overdue(now)...) {
			if err := out(e); err != nil {
				return result{}, err
			}
		}
	}
}

func (f *feed) batch(runs []*state.Run, now time.Time) []FeedEntry {
	latest := map[string]*state.Run{}
	var order []string
	for _, r := range runs {
		if latest[r.ID] == nil {
			order = append(order, r.ID)
		}
		latest[r.ID] = r
	}
	var entries []FeedEntry
	for _, id := range order {
		entries = append(entries, f.entries(latest[id], now)...)
	}
	sort.SliceStable(entries, func(i, j int) bool { return entries[i].At.Before(entries[j].At) })
	return entries
}

func (f *feed) read() ([]*state.Run, error) {
	file, err := os.Open(f.store.Paths.History())
	if errors.Is(err, fs.ErrNotExist) {
		f.file, f.offset, f.tail = nil, 0, nil
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return nil, err
	}
	if f.file == nil || !os.SameFile(f.file, info) || info.Size() < f.offset || !f.tailIntact(file) {
		f.offset, f.tail = 0, nil
	}
	f.file = info
	if info.Size() == f.offset {
		return nil, nil
	}
	data, err := io.ReadAll(io.NewSectionReader(file, f.offset, info.Size()-f.offset))
	if err != nil {
		return nil, err
	}
	end := bytes.LastIndexByte(data, '\n')
	if end < 0 {
		return nil, nil
	}
	f.offset += int64(end + 1)
	f.tail = append(f.tail, data[:end+1]...)
	f.tail = f.tail[max(0, len(f.tail)-feedTailCheck):]
	var runs []*state.Run
	for _, line := range bytes.Split(data[:end], []byte("\n")) {
		r := &state.Run{}
		if json.Unmarshal(line, r) == nil && r.ID != "" && f.matches(r) {
			runs = append(runs, r)
		}
	}
	return runs, nil
}

func (f *feed) tailIntact(file *os.File) bool {
	if len(f.tail) == 0 {
		return true
	}
	got := make([]byte, len(f.tail))
	_, err := file.ReadAt(got, f.offset-int64(len(f.tail)))
	return err == nil && bytes.Equal(got, f.tail)
}

func (f *feed) matches(r *state.Run) bool {
	return (f.project == "" || r.Project == f.project) && (f.job == "" || r.Job == f.job)
}

func (f *feed) entries(r *state.Run, now time.Time) []FeedEntry {
	var out []FeedEntry
	add := func(kind string, e FeedEntry) {
		key := strings.Join([]string{r.ID, kind, e.At.Format(time.RFC3339Nano), e.Status, e.Summary, e.Ref}, "\x00")
		if f.shown[key] {
			return
		}
		f.shown[key] = true
		e.Run, e.Project, e.Job = r.ID, r.Project, r.Job
		if f.all || !e.hidden() {
			out = append(out, e)
		}
	}
	switch r.Status {
	case state.StatusFailed, state.StatusPrecheckError, state.StatusMissed, state.StatusSkipped:
		add("run", FeedEntry{At: endedAt(r), Source: FeedAgs, Status: r.Status, Summary: oneLine(r.Reason)})
	case state.StatusSucceeded:
		if !r.AwaitsReport {
			add("run", FeedEntry{At: endedAt(r), Source: FeedAgs, Status: r.Status})
		}
	}
	if r.Report != nil {
		delete(f.pending, r.ID)
		add("report", FeedEntry{At: r.Report.At, Source: FeedAgent, Status: r.Report.Status, Summary: oneLine(r.Report.Summary), Ref: r.Report.Ref})
		return out
	}
	if r.Status == state.StatusSucceeded && r.AwaitsReport && r.StartedAt != nil && f.reportWithin(r) > 0 {
		f.pending[r.ID] = r
	}
	return out
}

func (f *feed) overdue(now time.Time) []FeedEntry {
	var out []FeedEntry
	for _, r := range f.pending {
		out = append(out, f.overdueRun(r, now)...)
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].At.Before(out[j].At) })
	return out
}

func (f *feed) overdueRun(r *state.Run, now time.Time) []FeedEntry {
	deadline := r.StartedAt.Add(f.reportWithin(r))
	if !now.After(deadline) {
		return nil
	}
	delete(f.pending, r.ID)
	key := r.ID + "/" + FeedNoReport
	if f.shown[key] {
		return nil
	}
	f.shown[key] = true
	summary := fmt.Sprintf("no ags report within %s of the hand-off", config.FormatDuration(f.reportWithin(r)))
	return []FeedEntry{{At: deadline, Run: r.ID, Project: r.Project, Job: r.Job, Source: FeedAgs, Status: FeedNoReport, Summary: summary}}
}

func (f *feed) reportWithin(r *state.Run) time.Duration {
	return reportWithin(f.machine, r)
}

func endedAt(r *state.Run) time.Time {
	if r.EndedAt != nil {
		return *r.EndedAt
	}
	return r.ScheduledFor
}

func (a *App) feedWriter() func(FeedEntry) error {
	if a.json {
		enc := json.NewEncoder(a.Stdout)
		enc.SetEscapeHTML(false)
		return func(e FeedEntry) error { return enc.Encode(envelope{OK: true, Data: e}) }
	}
	fd, terminal := a.Stdout.(interface{ Fd() uintptr })
	color := terminal && isCharDevice(fd) && a.Getenv("NO_COLOR") == ""
	return func(e FeedEntry) error {
		width := 0
		if terminal {
			width = terminalWidth(fd.Fd())
		}
		_, err := fmt.Fprint(a.Stdout, FeedLine(e, width, color))
		return err
	}
}

func isCharDevice(fd interface{ Fd() uintptr }) bool {
	var st unix.Stat_t
	return unix.Fstat(int(fd.Fd()), &st) == nil && st.Mode&unix.S_IFMT == unix.S_IFCHR
}

func terminalWidth(fd uintptr) int {
	ws, err := unix.IoctlGetWinsize(int(fd), unix.TIOCGWINSZ)
	if err != nil {
		return 0
	}
	return int(ws.Col)
}

func FeedLabel(e FeedEntry) string {
	switch {
	case e.Status == FeedNoReport:
		return "no report"
	case e.Source == FeedAgent:
		return e.Status
	case e.Status == state.StatusPrecheckError:
		return "ERROR"
	case e.Status == state.StatusSucceeded, e.Status == state.StatusSkipped:
		return e.Status
	}
	return strings.ToUpper(e.Status)
}

func feedColor(e FeedEntry) string {
	switch {
	case e.Status == state.ReportNothing, e.Status == state.StatusSkipped:
		return "2"
	case e.Status == state.ReportDone, e.Status == state.StatusSucceeded:
		return "32"
	case e.Status == state.ReportBlocked, e.Status == FeedNoReport:
		return "33"
	case e.Source == FeedAgent:
		return "31"
	}
	return "1;31"
}

func FeedLine(e FeedEntry, width int, color bool) string {
	key, label := e.Project+"/"+e.Job, FeedLabel(e)
	stamp := e.At.Local().Format("01-02 15:04")
	indent := runewidth.StringWidth(stamp) + 2 + max(feedKeyWidth, runewidth.StringWidth(key)) + 1 + feedLabelWidth + 1
	narrow := width > 0 && width-indent < feedMinSummary
	head := fmt.Sprintf("%s  %s %s", stamp, runewidth.FillRight(key, feedKeyWidth), runewidth.FillRight(label, feedLabelWidth))
	if narrow {
		indent = feedNarrowPad
		head = stamp + "  " + key + " " + label
	}
	if color {
		head = strings.Replace(head, label, "\x1b["+feedColor(e)+"m"+label+"\x1b[0m", 1)
	}
	var b strings.Builder
	b.WriteString(head)
	if narrow {
		b.WriteString("\n" + strings.Repeat(" ", indent))
	} else {
		b.WriteString(" ")
	}
	pad := "\n" + strings.Repeat(" ", indent)
	room := 0
	if width > 0 {
		room = width - indent
	}
	b.WriteString(strings.Join(wrap(e.Summary, room), pad))
	if e.Ref != "" {
		b.WriteString(pad + "→ " + e.Ref)
	}
	return strings.TrimRight(b.String(), " ") + "\n"
}

func wrap(text string, width int) []string {
	words := strings.Fields(text)
	if width <= 0 || len(words) == 0 {
		return []string{strings.Join(words, " ")}
	}
	var lines []string
	line := ""
	for _, w := range words {
		switch {
		case line == "":
			line = w
		case runewidth.StringWidth(line)+1+runewidth.StringWidth(w) <= width:
			line += " " + w
		default:
			lines = append(lines, line)
			line = w
		}
	}
	return append(lines, line)
}
