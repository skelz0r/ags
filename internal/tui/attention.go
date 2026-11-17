package tui

import (
	"fmt"
	"os/exec"
	"runtime"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/skelz0r/ags/internal/cli"
)

type attentionRow struct {
	item  *cli.AttentionItem
	entry *cli.FeedEntry
}

func (m *model) attentionRow() attentionRow {
	i := m.cursor[screenAttention]
	switch {
	case i < 0:
	case i < len(m.attention.Items):
		return attentionRow{item: &m.attention.Items[i]}
	case i < len(m.attention.Items)+len(m.attention.Recent):
		return attentionRow{entry: &m.attention.Recent[i-len(m.attention.Items)]}
	}
	return attentionRow{}
}

func (r attentionRow) job() string {
	switch {
	case r.item != nil:
		return r.item.Key
	case r.entry != nil:
		return r.entry.Project + "/" + r.entry.Job
	}
	return ""
}

func (r attentionRow) run() string {
	switch {
	case r.item != nil:
		return r.item.Run
	case r.entry != nil:
		return r.entry.Run
	}
	return ""
}

func (r attentionRow) ref() string {
	switch {
	case r.item != nil:
		return r.item.Ref
	case r.entry != nil:
		return r.entry.Ref
	}
	return ""
}

func (m *model) jobByKey(key string) *cli.JobView {
	for i := range m.projects {
		for j := range m.projects[i].Jobs {
			if m.projects[i].Jobs[j].Key == key {
				return &m.projects[i].Jobs[j]
			}
		}
	}
	return nil
}

func (m *model) attentionKey(key string) tea.Cmd {
	row := m.attentionRow()
	switch key {
	case "P", "tab":
		m.cursor[screenProjects] = 0
		m.push(screenProjects)
		m.reload()
		return nil
	case "enter", "right", "l":
		switch {
		case row.item != nil && row.item.Kind == cli.AttentionUnapproved:
			return m.review(row.item.Key)
		case row.run() != "":
			text, err := m.c.detail(row.run())
			if err != nil {
				m.fail(err)
				return nil
			}
			m.detail = strings.Split(text, "\n")
			m.push(screenDetail)
		case row.item != nil && row.item.Project != "":
			m.openProject(row.item.Project)
		}
		return nil
	case "J":
		if row.item != nil && row.item.Project != "" {
			m.openProject(row.item.Project)
		} else if row.entry != nil {
			m.openProject(row.entry.Project)
		}
		return nil
	case "o":
		return m.openRef(row.ref())
	}
	job := row.job()
	if job == "" || (row.item != nil && row.item.Job == "") {
		return nil
	}
	switch key {
	case "r":
		return m.runNow(job)
	case "p":
		j := m.jobByKey(job)
		if j == nil {
			m.fail(fmt.Errorf("%s is not a job of a valid project", job))
			return nil
		}
		if j.Enabled {
			return m.run("paused "+job, "pause", job)
		}
		return m.run("resumed "+job, "resume", job)
	case "A":
		return m.review(job)
	}
	return nil
}

func (m *model) openProject(name string) {
	m.project = name
	m.cursor[screenJobs] = 0
	m.push(screenJobs)
	m.reload()
}

func (m *model) openRef(ref string) tea.Cmd {
	if ref == "" {
		m.info("no ref on this line")
		return nil
	}
	if !strings.HasPrefix(ref, "https://") && !strings.HasPrefix(ref, "http://") {
		m.info("ref: %s", ref)
		return nil
	}
	opener := "xdg-open"
	if runtime.GOOS == "darwin" {
		opener = "open"
	}
	return func() tea.Msg {
		if err := exec.Command(opener, ref).Run(); err != nil {
			return doneMsg{err: fmt.Errorf("%s %s: %v", opener, ref, err)}
		}
		return doneMsg{message: "opened " + ref}
	}
}

func kindStyle(kind string) func(...string) string {
	switch kind {
	case cli.AttentionBlocked, cli.AttentionNoReport, cli.AttentionMissed, cli.AttentionUnapproved:
		return warnStyle.Render
	case "done":
		return okStyle.Render
	}
	return errorStyle.Render
}

const attentionFormat = "%-30s %-13s %-24s %s"

func (m *model) attentionView() string {
	now := m.c.base.Now()
	room := max(20, m.width-71)
	selected := m.cursor[screenAttention]
	var rows []string
	add := func(where, kind, label, when, detail string) {
		label = fmt.Sprintf("%-13s", label)
		if len(rows) != selected {
			label = kindStyle(kind)(label)
		}
		rows = append(rows, fmt.Sprintf("%-30s %s %-24s %s", trunc(where, 30), label, trunc(when, 24), trunc(detail, room)))
	}
	for _, i := range m.attention.Items {
		add(i.Where(), i.Kind, cli.AttentionLabel(i.Kind), i.Headline(now), i.Detail())
	}
	for _, e := range m.attention.Recent {
		add(e.Project+"/"+e.Job, "done", "done", e.At.Local().Format("01-02 15:04"), e.Summary)
	}
	title := titleStyle.Render("Attention") + dimStyle.Render("  enter details, r run now, p pause/resume, A approve, o open ref, J jobs, P projects, R all runs, q quit") + "\n"
	if len(rows) == 0 {
		return title + "\n" + okStyle.Render("Nothing waits for you.") + dimStyle.Render(" P projects, R all runs") + "\n"
	}
	header := fmt.Sprintf(attentionFormat, "WHERE", "WHAT", "SINCE", "DETAIL")
	out := title + m.table(header, rows, selected)
	row := m.attentionRow()
	if ref := row.ref(); ref != "" {
		out += dimStyle.Render("→ "+ref) + "\n"
	}
	return out
}
