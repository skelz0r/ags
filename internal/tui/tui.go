package tui

import (
	"fmt"
	"os"
	"os/exec"
	"sort"
	"strings"
	"syscall"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/skelz0r/ags/internal/cli"
	"github.com/skelz0r/ags/internal/paths"
)

type screen int

const (
	screenProjects screen = iota
	screenJobs
	screenRuns
	screenDetail
	screenApprove
)

var (
	titleStyle    = lipgloss.NewStyle().Bold(true)
	selectedStyle = lipgloss.NewStyle().Reverse(true)
	dimStyle      = lipgloss.NewStyle().Faint(true)
	errorStyle    = lipgloss.NewStyle().Foreground(lipgloss.Color("1"))
	okStyle       = lipgloss.NewStyle().Foreground(lipgloss.Color("2"))
	warnStyle     = lipgloss.NewStyle().Foreground(lipgloss.Color("3"))
)

type prompt struct {
	fields  []string
	values  []string
	current string
	submit  func(values []string) tea.Cmd
}

type confirm struct {
	question string
	yes      func() tea.Cmd
}

type model struct {
	c      client
	width  int
	height int

	screen   screen
	projects []project
	project  string
	jobs     []cli.JobView
	runs     []cli.RunView
	runsJob  string
	detail   []string
	approve  []pendingApproval
	cursor   map[screen]int
	scroll   int
	status   status
	message  string
	isError  bool
	prompt   *prompt
	confirm  *confirm
	previous []screen
}

type refreshMsg struct{}
type tickMsg time.Time
type doneMsg struct {
	message string
	err     error
}

func Run(app *cli.App) error {
	m := &model{c: client{base: app}, cursor: map[screen]int{}}
	m.reload()
	_, err := tea.NewProgram(m, tea.WithAltScreen()).Run()
	return err
}

func pathsOf(app *cli.App) paths.Paths { return paths.Resolve(app.Getenv) }

func (m *model) Init() tea.Cmd { return tick() }

func tick() tea.Cmd {
	return tea.Tick(5*time.Second, func(t time.Time) tea.Msg { return tickMsg(t) })
}

func (m *model) reload() {
	m.status = m.c.daemonStatus()
	projects, err := m.c.projects()
	if err != nil {
		m.fail(err)
	}
	m.projects = projects
	m.jobs = nil
	for _, p := range projects {
		if p.Name == m.project {
			m.jobs = p.Jobs
		}
	}
	if m.screen == screenRuns {
		filters := []string{}
		if m.runsJob != "" {
			filters = append(filters, "--job", m.runsJob)
		}
		m.runs, _ = m.c.runs(filters...)
	}
	m.clamp()
}

func (m *model) fail(err error) {
	m.message, m.isError = err.Error(), true
}

func (m *model) info(format string, args ...any) {
	m.message, m.isError = fmt.Sprintf(format, args...), false
}

func (m *model) rows() int {
	switch m.screen {
	case screenProjects:
		return len(m.projects)
	case screenJobs:
		return len(m.jobs)
	case screenRuns:
		return len(m.runs)
	}
	return 0
}

func (m *model) clamp() {
	n := m.rows()
	if m.cursor[m.screen] >= n {
		m.cursor[m.screen] = n - 1
	}
	if m.cursor[m.screen] < 0 {
		m.cursor[m.screen] = 0
	}
}

func (m *model) push(s screen) {
	m.previous = append(m.previous, m.screen)
	m.screen = s
	m.scroll = 0
}

func (m *model) back() {
	if len(m.previous) == 0 {
		return
	}
	m.screen = m.previous[len(m.previous)-1]
	m.previous = m.previous[:len(m.previous)-1]
	m.scroll = 0
}

func (m *model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		return m, nil
	case tickMsg:
		if m.prompt == nil && m.confirm == nil {
			m.reload()
		}
		return m, tick()
	case refreshMsg:
		m.reload()
		return m, nil
	case doneMsg:
		if msg.err != nil {
			m.fail(msg.err)
		} else if msg.message != "" {
			m.info("%s", msg.message)
		}
		m.reload()
		return m, nil
	case tea.KeyMsg:
		if msg.String() == "ctrl+c" {
			return m, tea.Quit
		}
		if m.prompt != nil {
			return m, m.updatePrompt(msg)
		}
		if m.confirm != nil {
			c := m.confirm
			m.confirm = nil
			if msg.String() == "y" {
				return m, c.yes()
			}
			m.info("cancelled")
			return m, nil
		}
		return m, m.updateKey(msg.String())
	}
	return m, nil
}

func (m *model) updatePrompt(msg tea.KeyMsg) tea.Cmd {
	p := m.prompt
	switch msg.Type {
	case tea.KeyEsc:
		m.prompt = nil
		m.info("cancelled")
	case tea.KeyEnter:
		p.values = append(p.values, strings.TrimSpace(p.current))
		p.current = ""
		if len(p.values) == len(p.fields) {
			m.prompt = nil
			return p.submit(p.values)
		}
	case tea.KeyBackspace:
		if r := []rune(p.current); len(r) > 0 {
			p.current = string(r[:len(r)-1])
		}
	case tea.KeySpace:
		p.current += " "
	case tea.KeyRunes:
		p.current += string(msg.Runes)
	}
	return nil
}

func (m *model) selectedProject() *project {
	i := m.cursor[screenProjects]
	if i < len(m.projects) {
		return &m.projects[i]
	}
	return nil
}

func (m *model) selectedJob() *cli.JobView {
	i := m.cursor[screenJobs]
	if i < len(m.jobs) {
		return &m.jobs[i]
	}
	return nil
}

func (m *model) run(message string, args ...string) tea.Cmd {
	return func() tea.Msg {
		_, err := m.c.call(nil, args...)
		return doneMsg{message: message, err: err}
	}
}

func (m *model) updateKey(key string) tea.Cmd {
	switch key {
	case "q":
		if m.screen == screenProjects {
			return tea.Quit
		}
		m.back()
		return nil
	case "esc", "backspace", "left", "h":
		m.back()
		return nil
	case "up", "k":
		if m.screen == screenDetail || m.screen == screenApprove {
			m.scroll = max(0, m.scroll-1)
		} else {
			m.cursor[m.screen]--
			m.clamp()
		}
		return nil
	case "down", "j":
		if m.screen == screenDetail || m.screen == screenApprove {
			m.scroll++
		} else {
			m.cursor[m.screen]++
			m.clamp()
		}
		return nil
	case "R":
		m.push(screenRuns)
		m.runsJob = ""
		m.reload()
		return nil
	}
	switch m.screen {
	case screenProjects:
		return m.projectsKey(key)
	case screenJobs:
		return m.jobsKey(key)
	case screenRuns:
		if key == "enter" && m.cursor[screenRuns] < len(m.runs) {
			text, err := m.c.detail(m.runs[m.cursor[screenRuns]].ID)
			if err != nil {
				m.fail(err)
				return nil
			}
			m.detail = strings.Split(text, "\n")
			m.push(screenDetail)
		}
	case screenApprove:
		if key == "y" {
			target := m.project
			var reviewed []string
			for _, p := range m.approve {
				reviewed = append(reviewed, p.Fingerprint)
			}
			m.back()
			return m.run("approved", "approve", target, "--yes", "--fingerprints", strings.Join(reviewed, ","))
		}
	}
	return nil
}

func (m *model) projectsKey(key string) tea.Cmd {
	p := m.selectedProject()
	switch key {
	case "enter", "right", "l":
		if p != nil {
			m.project = p.Name
			m.cursor[screenJobs] = 0
			m.push(screenJobs)
			m.reload()
		}
	case "a":
		m.prompt = &prompt{fields: []string{"path of the project (~ allowed)"}, submit: func(v []string) tea.Cmd {
			return m.run("added "+v[0], "add", v[0])
		}}
	case "d":
		if p != nil {
			name := p.Name
			m.confirm = &confirm{question: "remove project " + name + " (history kept)?", yes: func() tea.Cmd {
				return m.run("removed "+name, "remove", name)
			}}
		}
	}
	return nil
}

func (m *model) jobsKey(key string) tea.Cmd {
	j := m.selectedJob()
	switch key {
	case "enter", "right", "l":
		if j != nil {
			m.runsJob = j.Key
			m.push(screenRuns)
			m.reload()
		}
	case "n":
		project := m.project
		m.prompt = &prompt{fields: []string{"shared or local (s/l)", "job name", "schedule (cron)", "prompt"}, submit: func(v []string) tea.Cmd {
			args := []string{"new", project + "/" + v[1], "--schedule", v[2], "--prompt", v[3]}
			if strings.HasPrefix(strings.ToLower(v[0]), "l") {
				args = append(args, "--local")
			}
			return m.run("added "+v[1], args...)
		}}
	case "e":
		if j != nil {
			return m.edit(j)
		}
	case "d":
		if j != nil {
			key := j.Key
			m.confirm = &confirm{question: fmt.Sprintf("delete %s from %s?", key, j.File), yes: func() tea.Cmd {
				return m.run("deleted "+key, "delete", key, "--yes")
			}}
		}
	case "p":
		if j != nil {
			if j.Enabled {
				return m.run("paused "+j.Key, "pause", j.Key)
			}
			return m.run("resumed "+j.Key, "resume", j.Key)
		}
	case "r":
		if j != nil {
			return m.runNow(j.Key)
		}
	case "A":
		pending, err := m.c.pending(m.project)
		if err != nil {
			m.fail(err)
			return nil
		}
		if len(pending) == 0 {
			m.info("nothing to approve")
			return nil
		}
		m.approve = pending
		m.push(screenApprove)
	}
	return nil
}

func (m *model) edit(j *cli.JobView) tea.Cmd {
	editor := m.c.base.Getenv("EDITOR")
	if editor == "" {
		editor = "vi"
	}
	var target struct {
		File string `json:"file"`
		Line int    `json:"line"`
	}
	if _, err := m.c.call(&target, "edit", j.Key); err != nil {
		m.fail(err)
		return nil
	}
	parts := strings.Fields(editor)
	cmd := exec.Command(parts[0], append(parts[1:], fmt.Sprintf("+%d", target.Line), target.File)...)
	project := m.project
	return tea.ExecProcess(cmd, func(err error) tea.Msg {
		if err != nil {
			return doneMsg{err: err}
		}
		for _, p := range m.projects {
			if p.Name == project {
				if _, err := m.c.call(nil, "validate", p.Path); err != nil {
					return doneMsg{err: err}
				}
			}
		}
		return doneMsg{message: "saved and valid"}
	})
}

func (m *model) runNow(key string) tea.Cmd {
	return func() tea.Msg {
		exe, err := m.c.base.Exe()
		if err != nil {
			return doneMsg{err: err}
		}
		devnull, _ := os.Open(os.DevNull)
		cmd := exec.Command(exe, "run", key)
		cmd.Stdin, cmd.Stdout, cmd.Stderr = devnull, devnull, devnull
		cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
		if err := cmd.Start(); err != nil {
			return doneMsg{err: err}
		}
		go cmd.Wait()
		return doneMsg{message: "started " + key + " in the background, see its runs"}
	}
}

func (m *model) View() string {
	var b strings.Builder
	body := m.body()
	b.WriteString(body)
	b.WriteString("\n")
	b.WriteString(m.footer())
	return b.String()
}

func (m *model) body() string {
	switch m.screen {
	case screenJobs:
		return m.jobsView()
	case screenRuns:
		return m.runsView()
	case screenDetail:
		return m.scrolled(titleStyle.Render("Run")+"\n", m.detail)
	case screenApprove:
		var lines []string
		for _, p := range m.approve {
			head := "--- " + p.Job
			if p.First {
				head += " (never approved)"
			}
			lines = append(lines, titleStyle.Render(head))
			for _, l := range strings.Split(strings.TrimRight(p.Diff, "\n"), "\n") {
				switch {
				case strings.HasPrefix(l, "+ "):
					l = okStyle.Render(l)
				case strings.HasPrefix(l, "- "):
					l = errorStyle.Render(l)
				}
				lines = append(lines, l)
			}
		}
		return m.scrolled(titleStyle.Render("Approve "+m.project)+dimStyle.Render("  y approve all, esc cancel")+"\n", lines)
	}
	return m.projectsView()
}

func (m *model) scrolled(header string, lines []string) string {
	visible := max(1, m.height-4)
	if m.scroll > max(0, len(lines)-visible) {
		m.scroll = max(0, len(lines)-visible)
	}
	end := min(len(lines), m.scroll+visible)
	return header + strings.Join(lines[m.scroll:end], "\n")
}

func (m *model) table(header string, rows []string, selected int) string {
	var b strings.Builder
	b.WriteString(dimStyle.Render(header) + "\n")
	visible := max(1, m.height-5)
	start := 0
	if selected >= visible {
		start = selected - visible + 1
	}
	for i := start; i < len(rows) && i < start+visible; i++ {
		row := rows[i]
		if i == selected {
			row = selectedStyle.Render(row)
		}
		b.WriteString(row + "\n")
	}
	return b.String()
}

func (m *model) projectsView() string {
	var rows []string
	for _, p := range m.projects {
		next, lastFailure := "-", "-"
		var soonest time.Time
		for _, j := range p.Jobs {
			if len(j.Next) > 0 && j.Enabled && (soonest.IsZero() || j.Next[0].Before(soonest)) {
				soonest = j.Next[0]
			}
			if j.LastRun != nil && (j.LastRun.Status == "failed" || j.LastRun.Status == "precheck_error") {
				lastFailure = j.Name + " " + j.LastRun.ScheduledFor.Local().Format("01-02 15:04")
			}
		}
		if !soonest.IsZero() {
			next = soonest.Local().Format("Mon 01-02 15:04")
		}
		jobs := fmt.Sprint(len(p.Jobs))
		if len(p.Errors) > 0 {
			jobs = "invalid"
		}
		rows = append(rows, fmt.Sprintf("%-24s %-8s %-16s %s", trunc(p.Name, 24), jobs, next, lastFailure))
	}
	header := fmt.Sprintf("%-24s %-8s %-16s %s", "PROJECT", "JOBS", "NEXT RUN", "LAST FAILURE")
	out := titleStyle.Render("Projects") + dimStyle.Render("  enter open, a add, d remove, R all runs, q quit") + "\n" + m.table(header, rows, m.cursor[screenProjects])
	if p := m.selectedProject(); p != nil {
		for _, e := range p.Errors {
			out += errorStyle.Render(e.String()) + "\n"
		}
	}
	return out
}

func (m *model) jobsView() string {
	var rows []string
	for _, j := range m.jobs {
		last := "-"
		if j.LastRun != nil {
			last = j.LastRun.LastScheduled().Local().Format("01-02 15:04") + " " + cli.StatusLabel(j.LastRun.Status, j.LastRun.Reason)
		}
		next := "-"
		if len(j.Next) > 0 {
			next = j.Next[0].Local().Format("Mon 01-02 15:04")
		}
		var flags []string
		if !j.Enabled {
			flags = append(flags, "paused")
		}
		if !j.Approved {
			flags = append(flags, "unapproved")
		}
		rows = append(rows, fmt.Sprintf("%-22s %-26s %-10s %-7s %-16s %-30s %s", trunc(j.Name, 22), trunc(j.Description, 26), trunc(j.Command, 10), j.Origin, next, trunc(last, 30), strings.Join(flags, ",")))
	}
	header := fmt.Sprintf("%-22s %-26s %-10s %-7s %-16s %-30s %s", "JOB", "SCHEDULE", "COMMAND", "ORIGIN", "NEXT", "LAST RUN", "STATE")
	out := titleStyle.Render("Jobs of "+m.project) + dimStyle.Render("  enter runs, n new, e edit, d delete, p pause/resume, r run now, A approve, esc back") + "\n" + m.table(header, rows, m.cursor[screenJobs])
	for _, p := range m.projects {
		if p.Name != m.project {
			continue
		}
		for _, e := range p.Errors {
			out += errorStyle.Render(e.String()) + "\n"
		}
		for _, w := range p.Warnings {
			out += warnStyle.Render(w.String()) + "\n"
		}
	}
	return out
}

func (m *model) runsView() string {
	title := "All runs"
	if m.runsJob != "" {
		title = "Runs of " + m.runsJob
	}
	var rows []string
	for _, r := range m.runs {
		duration, exit := "-", "-"
		if r.EndedAt != nil && r.StartedAt != nil {
			duration = time.Duration(r.DurationSeconds * float64(time.Second)).Round(time.Second).String()
		}
		if r.ExitCode != nil {
			exit = fmt.Sprint(*r.ExitCode)
		}
		rows = append(rows, fmt.Sprintf("%-16s %-30s %-9s %-28s %-9s %s", r.LastScheduled().Local().Format("01-02 15:04:05"), trunc(r.Project+"/"+r.Job, 30), orDash(r.Trigger), trunc(r.Label(), 28), duration, exit))
	}
	header := fmt.Sprintf("%-16s %-30s %-9s %-28s %-9s %s", "TIME", "JOB", "TRIGGER", "STATUS", "DURATION", "EXIT")
	return titleStyle.Render(title) + dimStyle.Render("  enter details, esc back") + "\n" + m.table(header, rows, m.cursor[screenRuns])
}

func (m *model) footer() string {
	var parts []string
	if m.status.alive {
		parts = append(parts, okStyle.Render("daemon ok")+dimStyle.Render(" "+m.status.detail))
	} else {
		parts = append(parts, errorStyle.Render("daemon down")+dimStyle.Render(" "+m.status.detail))
	}
	errs := append([]string(nil), m.status.errors...)
	sort.Strings(errs)
	for _, e := range errs {
		parts = append(parts, errorStyle.Render(trunc(e, 80)))
	}
	line := strings.Join(parts, "  ")
	switch {
	case m.prompt != nil:
		line = titleStyle.Render(m.prompt.fields[len(m.prompt.values)]+": ") + m.prompt.current + "█"
	case m.confirm != nil:
		line = warnStyle.Render(m.confirm.question + " [y/N]")
	case m.message != "":
		style := okStyle
		if m.isError {
			style = errorStyle
		}
		line = style.Render(trunc(strings.ReplaceAll(m.message, "\n", " | "), max(20, m.width-2))) + "\n" + line
	}
	return line
}

func trunc(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n-1]) + "…"
}

func orDash(s string) string {
	if s == "" {
		return "-"
	}
	return s
}
